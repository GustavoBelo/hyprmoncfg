// Package hooks holds the parts of a console session that are not the display
// layout: audio on the TV, the lock screen held off, notifications silenced,
// the bar out of the way.
//
// Every hook returns an Undo that restores exactly what it changed, captured at
// the moment it changed it. None of them toggles blindly: a session that ends
// with the bar hidden because it was already hidden when the session started is
// a bug, and so is one that turns the lock screen back on for someone who had
// it off.
package hooks

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// Undo restores what a hook changed.
type Undo func(context.Context) error

// Env is what a hook needs to know about the session.
type Env struct {
	// TVName is the connector the session plays on ("HDMI-A-1").
	TVName string
	// TVDescription is the display's EDID description, used to find its audio
	// sink when the connector name alone is not enough.
	TVDescription string
	Logf          func(format string, args ...any)
}

func (e Env) logf(format string, args ...any) {
	if e.Logf != nil {
		e.Logf(format, args...)
	}
}

// Hook is one reversible change a console session makes.
type Hook interface {
	// Name is the key used in the config to turn this hook off.
	Name() string
	// Description is one line for the settings UI.
	Description() string
	// Available reports whether this machine has what the hook needs. A hook
	// that is unavailable is not offered, rather than offered and broken.
	Available() bool
	Enter(ctx context.Context, env Env) (Undo, error)
}

// All returns every hook, in the order a session applies them.
func All() []Hook {
	return []Hook{
		&audioHook{},
		&idleHook{},
		&notificationsHook{},
		&barHook{},
		&nightLightHook{},
	}
}

// Available returns the hooks this machine can actually run.
func Available() []Hook {
	usable := make([]Hook, 0, 5)
	for _, h := range All() {
		if h.Available() {
			usable = append(usable, h)
		}
	}
	return usable
}

// Session runs a set of hooks and undoes them together.
type Session struct {
	undos []namedUndo
}

type namedUndo struct {
	name string
	undo Undo
}

// Enter runs each enabled hook. A hook that fails is logged and skipped rather
// than aborting the session: losing the HDMI audio switch is not a reason to
// refuse to play, and whatever did apply is still undone on the way out.
func Enter(ctx context.Context, env Env, enabled func(name string) bool) *Session {
	session := &Session{}
	for _, hook := range All() {
		if enabled != nil && !enabled(hook.Name()) {
			continue
		}
		if !hook.Available() {
			continue
		}
		undo, err := hook.Enter(ctx, env)
		if err != nil {
			env.logf("couch: hook %s did not apply: %v", hook.Name(), err)
			continue
		}
		if undo != nil {
			session.undos = append(session.undos, namedUndo{name: hook.Name(), undo: undo})
		}
	}
	return session
}

// Leave undoes everything that applied, most recent first, and keeps going
// past a failure so one stuck hook cannot strand the rest.
func (s *Session) Leave(ctx context.Context, env Env) error {
	if s == nil {
		return nil
	}
	var failures []error
	for i := len(s.undos) - 1; i >= 0; i-- {
		entry := s.undos[i]
		if err := entry.undo(ctx); err != nil {
			env.logf("couch: hook %s did not restore: %v", entry.name, err)
			failures = append(failures, err)
		}
	}
	s.undos = nil
	return errors.Join(failures...)
}

// Applied names the hooks that took effect, for the log and the status.
func (s *Session) Applied() []string {
	if s == nil {
		return nil
	}
	names := make([]string, 0, len(s.undos))
	for _, entry := range s.undos {
		names = append(names, entry.name)
	}
	return names
}

// run executes a helper with a short deadline. Session hooks must never be able
// to hang the session on a command that does not return.
func run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

func have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
