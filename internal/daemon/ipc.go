package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/crmne/hyprmoncfg/internal/apply"
	"github.com/crmne/hyprmoncfg/internal/appstatus"
	"github.com/crmne/hyprmoncfg/internal/buildinfo"
	"github.com/crmne/hyprmoncfg/internal/ipc"
	"github.com/crmne/hyprmoncfg/internal/lid"
	"github.com/crmne/hyprmoncfg/internal/profile"
	"github.com/crmne/hyprmoncfg/internal/profileio"
)

type pendingTransaction struct {
	id         string
	owner      string
	profile    profile.Profile
	snapshot   apply.RevertState
	deadline   time.Time
	monitorSet string
	timer      *time.Timer
}

func (s *Service) Status() (appstatus.Document, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	profiles, err := s.store.List()
	if err != nil {
		return appstatus.Document{}, err
	}
	monitors, err := s.client.Monitors(ctx)
	if err != nil {
		return appstatus.Document{}, err
	}
	rules, err := s.client.WorkspaceRules(ctx)
	if err != nil {
		return appstatus.Document{}, err
	}
	return appstatus.Build(buildinfo.Version, true, profiles, monitors, rules), nil
}

func (s *Service) Preview(owner string, params ipc.PreviewParams) (ipc.Transaction, error) {
	target, err := s.resolvePreviewProfile(params)
	if err != nil {
		return ipc.Transaction{}, err
	}
	timeout := time.Duration(params.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if timeout > 24*time.Hour {
		timeout = 24 * time.Hour
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.pendingMu.Lock()
	if s.pending != nil {
		activeOwner := s.pending.owner
		s.pendingMu.Unlock()
		return ipc.Transaction{}, fmt.Errorf("another interactive preview is active (%s)", activeOwner)
	}
	s.pendingMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	monitors, err := s.client.Monitors(ctx)
	if err != nil {
		return ipc.Transaction{}, err
	}
	effective := target
	if state, stateErr := lid.ReadState(ctx); stateErr == nil && state == lid.Closed {
		effective, _ = profile.ApplyClosedLidPolicy(target, monitors)
	}
	engine := s.engine
	engine.AllowUnmanagedOverwrite = params.AllowUnmanagedOverwrite
	snapshot, err := engine.Apply(ctx, effective, monitors, apply.ApplyModeInteractive)
	if err != nil {
		return ipc.Transaction{}, err
	}

	id, err := transactionID()
	if err != nil {
		revertCtx, revertCancel := context.WithTimeout(context.Background(), 8*time.Second)
		_ = engine.Revert(revertCtx, snapshot)
		revertCancel()
		return ipc.Transaction{}, err
	}
	deadline := time.Now().Add(timeout)
	pending := &pendingTransaction{
		id:         id,
		owner:      owner,
		profile:    effective,
		snapshot:   snapshot,
		deadline:   deadline,
		monitorSet: profile.MonitorSetHash(monitors),
	}
	s.pendingMu.Lock()
	s.pending = pending
	pending.timer = time.AfterFunc(timeout, func() { s.expirePreview(id, owner) })
	s.pendingMu.Unlock()

	s.signalChange()
	return ipc.Transaction{ID: id, Profile: effective, Deadline: deadline}, nil
}

func (s *Service) Confirm(owner string, params ipc.TransactionParams) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	pending, err := s.ownedPending(owner, params.TransactionID)
	if err != nil {
		return err
	}
	s.clearPending(pending.id)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.engine.PostApply(ctx, pending.profile); err != nil {
		s.cfg.Logf("post apply for %q failed: %v", pending.profile.Name, err)
	}
	s.setManualOverride(pending.monitorSet)
	s.signalChange()
	return nil
}

func (s *Service) Revert(owner string, params ipc.TransactionParams) error {
	return s.revertOwned(owner, params.TransactionID)
}

func (s *Service) Save(params ipc.SaveParams) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := profileio.SaveWithSidecars(s.store, params.Profile); err != nil {
		return err
	}
	s.signalChange()
	return nil
}

func (s *Service) Delete(params ipc.DeleteParams) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if strings.TrimSpace(params.Name) == "" {
		return errors.New("profile name is required")
	}
	if err := s.store.Delete(params.Name); err != nil {
		return err
	}
	s.signalChange()
	return nil
}

func (s *Service) Disconnect(owner string) {
	s.pendingMu.Lock()
	pending := s.pending
	s.pendingMu.Unlock()
	if pending == nil || pending.owner != owner {
		return
	}
	if err := s.revertOwned(owner, pending.id); err != nil {
		s.cfg.Logf("revert disconnected IPC preview: %v", err)
	}
}

func (s *Service) resolvePreviewProfile(params ipc.PreviewParams) (profile.Profile, error) {
	if params.Profile != nil {
		target := *params.Profile
		target.Normalize()
		if err := target.Validate(); err != nil {
			return profile.Profile{}, err
		}
		return target, nil
	}
	if strings.TrimSpace(params.ProfileName) == "" {
		return profile.Profile{}, errors.New("preview requires profile or profile_name")
	}
	return s.store.Load(params.ProfileName)
}

func (s *Service) ownedPending(owner string, id string) (*pendingTransaction, error) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.pending == nil {
		return nil, ipc.ErrTransactionUnavailable
	}
	if s.pending.id != id {
		return nil, fmt.Errorf("%w: unknown transaction %q", ipc.ErrTransactionUnavailable, id)
	}
	if s.pending.owner != owner {
		return nil, errors.New("interactive preview belongs to another client")
	}
	return s.pending, nil
}

func (s *Service) clearPending(id string) {
	s.pendingMu.Lock()
	if s.pending != nil && s.pending.id == id {
		if s.pending.timer != nil {
			s.pending.timer.Stop()
		}
		s.pending = nil
	}
	s.pendingMu.Unlock()
}

func (s *Service) revertOwned(owner string, id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	pending, err := s.ownedPending(owner, id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.engine.Revert(ctx, pending.snapshot); err != nil {
		return err
	}
	s.clearPending(id)
	s.signalChange()
	return nil
}

func (s *Service) expirePreview(id string, owner string) {
	if err := s.revertOwned(owner, id); err != nil && !errors.Is(err, ipc.ErrTransactionUnavailable) {
		s.cfg.Logf("auto-revert IPC preview: %v", err)
	}
}

func transactionID() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("generate transaction id: %w", err)
	}
	return hex.EncodeToString(token[:]), nil
}
