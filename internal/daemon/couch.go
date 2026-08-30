package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/crmne/hyprmoncfg/internal/config"
	"github.com/crmne/hyprmoncfg/internal/couch"
	"github.com/crmne/hyprmoncfg/internal/couch/hooks"
	"github.com/crmne/hyprmoncfg/internal/hypr"
	"github.com/crmne/hyprmoncfg/internal/profile"
)

// couchController owns the console session.
//
// Ownership used to sit in a detached `hyprmoncfg couch play` process while the
// daemon was merely told to stand aside. The two then fought over the same
// displays -- one session log shows a manual restore landing 47 seconds into a
// play -- and a SIGKILL left the TV layout applied with nothing to put the
// desktop back. Here the daemon holds the write lock, the event feed and the
// session state at once, so neither can happen.
type couchController struct {
	svc *Service

	mu      sync.Mutex
	phase   couch.Phase
	started time.Time
	desk    *profile.Profile
	cancel  context.CancelFunc
	// trackedWindow is the Big Picture window address the session is watching,
	// so its closewindow event is recognised without re-querying.
	trackedWindow string
	// reason records why the last session ended, for the status document.
	reason string
	// hooks holds the undos for everything the session changed outside the
	// display layout: audio, idle, notifications, the bar.
	hooks *hooks.Session
	// detector is kept for the duration so a window-title event can re-assert
	// fullscreen without rebuilding its snapshot.
	detector *couch.BigPictureDetector
}

func newCouchController(svc *Service) *couchController {
	return &couchController{svc: svc, phase: couch.PhaseIdle}
}

func (c *couchController) Phase() couch.Phase {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phase == "" {
		return couch.PhaseIdle
	}
	return c.phase
}

// Active reports whether a session holds the displays. applyBest consults this
// instead of the old CouchSessionActive callback, which had to be wired in from
// the command just to tell the daemon about a process it did not control.
func (c *couchController) Active() bool {
	return c.Phase() != couch.PhaseIdle
}

func (c *couchController) setPhase(phase couch.Phase) {
	c.mu.Lock()
	c.phase = phase
	c.mu.Unlock()
	c.svc.signalChange()
}

// Status describes the session for the status document and the CLI.
type CouchStatus struct {
	Phase       couch.Phase `json:"phase"`
	Active      bool        `json:"active"`
	StartedAt   time.Time   `json:"started_at,omitempty"`
	Duration    string      `json:"duration,omitempty"`
	Reason      string      `json:"reason,omitempty"`
	Controllers int         `json:"controllers"`
}

func (c *couchController) Status() CouchStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := CouchStatus{
		Phase:       c.phase,
		Active:      c.phase != couch.PhaseIdle && c.phase != "",
		StartedAt:   c.started,
		Reason:      c.reason,
		Controllers: couch.ConnectedControllers(),
	}
	if status.Phase == "" {
		status.Phase = couch.PhaseIdle
	}
	if status.Active && !c.started.IsZero() {
		d := time.Since(c.started)
		status.Duration = fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return status
}

var errCouchBusy = errors.New("a couch session is already running")

// Start enters console mode. It returns as soon as the layout is applied and
// Big Picture has been asked for; the session itself continues in the
// background until Big Picture closes, Steam exits, or Stop is called.
func (c *couchController) Start(ctx context.Context, trigger string) error {
	c.mu.Lock()
	if c.phase != couch.PhaseIdle && c.phase != "" {
		c.mu.Unlock()
		return errCouchBusy
	}
	c.phase = couch.PhaseEntering
	c.reason = ""
	c.mu.Unlock()

	if err := c.enter(ctx, trigger); err != nil {
		c.mu.Lock()
		c.phase = couch.PhaseIdle
		c.mu.Unlock()
		c.svc.signalChange()
		return err
	}
	return nil
}

func (c *couchController) enter(ctx context.Context, trigger string) error {
	base := c.svc.cfg.ConfigDir
	state, err := couch.StateDir()
	if err != nil {
		return err
	}

	cfg, err := couch.LoadConfig(base)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return errors.New("Couch Mode is disabled; enable it with `hyprmoncfg couch enable`")
	}
	if !config.IsManaged(base) {
		return couch.ErrUnmanaged
	}

	monitors, err := c.svc.client.Monitors(ctx)
	if err != nil {
		return err
	}

	consoleProfile, changed, err := couch.EnsureConsoleProfile(c.svc.store, monitors, &cfg)
	if err != nil {
		return fmt.Errorf("prepare the console layout: %w", err)
	}
	if changed {
		if err := couch.SaveConfig(base, cfg); err != nil {
			return err
		}
	}

	rules, err := c.svc.client.WorkspaceRules(ctx)
	if err != nil {
		rules = nil
	}
	desk := profile.FromState(couch.DeskSnapshotName, monitors, rules)
	if len(desk.Outputs) == 0 {
		return errors.New("Hyprland reported no displays")
	}

	// Persist the snapshot before touching anything. If the daemon dies between
	// here and the restore, whoever starts next finds a desktop layout to put
	// back rather than a note saying the session went stale.
	session := couch.Session{
		PID:       os.Getpid(),
		Phase:     couch.PhaseEntering,
		StartedAt: time.Now(),
		Desk:      &desk,
	}
	if err := couch.WriteSession(state, session); err != nil {
		return err
	}

	couch.AppendLog(state, "couch: entering via %s (tv=%s mode=%s hdr=%t vrr=%t others=%s)",
		trigger, cfg.Layout.TVName, cfg.Layout.Mode, cfg.Layout.HDR, cfg.Layout.VRR, cfg.Layout.Desk)

	if err := c.svc.applyProfileLocked(ctx, consoleProfile); err != nil {
		couch.ClearSession(state)
		couch.AppendLog(state, "couch: applying the console layout failed: %v", err)
		return fmt.Errorf("apply the console layout: %w", err)
	}

	env := c.hookEnv(cfg, monitors)
	applied := hooks.Enter(ctx, env, cfg.HookEnabled)
	if names := applied.Applied(); len(names) > 0 {
		couch.AppendLog(state, "couch: session hooks applied: %s", strings.Join(names, ", "))
	}

	c.mu.Lock()
	c.started = session.StartedAt
	c.desk = &desk
	c.hooks = applied
	c.phase = couch.PhasePlaying
	c.mu.Unlock()

	session.Phase = couch.PhasePlaying
	_ = couch.WriteSession(state, session)

	sessionCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()

	go c.run(sessionCtx, cfg, state)
	c.svc.signalChange()
	return nil
}

// run drives the session after the layout is up: focus the TV, ask for Big
// Picture, then wait for it to end.
func (c *couchController) run(ctx context.Context, cfg couch.Config, state string) {
	defer c.leave(context.WithoutCancel(ctx), state)

	// Big Picture opens on the focused monitor, so focus the TV first. This is
	// a session action rather than a workspace rule in the generated profile,
	// which would fight whatever workspace layout the desktop uses.
	if name := cfg.Layout.TVName; name != "" {
		if err := c.svc.client.Dispatch(ctx, "focusmonitor", name); err != nil {
			couch.AppendLog(state, "couch: could not focus %s: %v", name, err)
		}
	}

	detector := couch.NewBigPictureDetector(ctx, c.svc.client)

	launcher, existingInstance, err := couch.ResolveLauncher()
	if err != nil {
		couch.AppendLog(state, "couch: no Big Picture launcher found: %v", err)
		c.setReason("no launcher")
		return
	}
	if cfg.Gamescope.Enabled {
		// gamescope always starts its own Steam inside the nested compositor,
		// so the steam:// handoff to a running instance does not apply.
		nested, gsErr := couch.GamescopeCommand(cfg.Layout,
			cfg.Gamescope, couch.BigPictureLauncher{Command: launcher.Command, Args: []string{"-gamepadui"}})
		if gsErr != nil {
			couch.AppendLog(state, "couch: gamescope unavailable, falling back to plain Big Picture: %v", gsErr)
		} else {
			launcher, existingInstance = nested, false
			couch.AppendLog(state, "couch: using gamescope (%s)",
				couch.GamescopeSummary(cfg.Layout, cfg.Gamescope))
		}
	}
	known := couch.KnownSteamPIDs()
	if _, err := couch.LaunchBigPicture(launcher); err != nil {
		couch.AppendLog(state, "couch: launching %s failed: %v", launcher.Command, err)
		c.setReason("launch failed")
		return
	}
	couch.AppendLog(state, "couch: launched %s", launcher.Command)

	steamPID := couch.ResolveSteamPID(existingInstance, known, state)
	bpmSeen := couch.WaitForBigPicture(ctx, detector, couch.BigPictureWaitWindow)
	if bpmSeen {
		couch.AppendLog(state, "couch: Big Picture is up")
		c.mu.Lock()
		c.detector = detector
		c.mu.Unlock()
		if fixed := couch.KeepBigPictureFullscreen(ctx, c.svc.client, detector); fixed > 0 {
			couch.AppendLog(state, "couch: put %d Big Picture window(s) fullscreen", fixed)
		}
	} else {
		couch.AppendLog(state, "couch: Big Picture not seen; watching Steam instead")
	}

	if cfg.CloseAppsEnabled && len(cfg.AppsToClose) > 0 {
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(cfg.CloseAppsWaitSeconds) * time.Second):
			}
			killed := couch.CloseTrackedApps(context.WithoutCancel(ctx), c.svc.client, cfg.AppsToClose)
			if len(killed) > 0 {
				couch.AppendLog(state, "apps: closed tracked processes %v", killed)
			} else {
				couch.AppendLog(state, "apps: no running process matched %v", cfg.AppsToClose)
			}
		}()
	}

	reason := c.watch(ctx, cfg, steamPID, bpmSeen, detector)
	c.setReason(reason)
	couch.AppendLog(state, "couch: session ending (%s)", reason)

	if reason == "controllers off" {
		closed := couch.CloseBigPicture(ctx, c.svc.client, detector)
		couch.AppendLog(state, "controllers: closed %d Big Picture window(s)", closed)
	}
}

func (c *couchController) watch(ctx context.Context, cfg couch.Config, steamPID int, bpmSeen bool, detector *couch.BigPictureDetector) string {
	tracker := &couch.ControllerTracker{}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	seen := bpmSeen
	for {
		count := detector.Count(ctx)
		if count > 0 {
			seen = true
		}
		if seen && count == 0 {
			return "Big Picture closed"
		}
		if steamPID > 0 && !couch.ProcessAlive(steamPID) {
			return "Steam exited"
		}
		if cfg.ExitOnControllersOff && tracker.Poll(time.Now(), couch.ConnectedControllers()) {
			return "controllers off"
		}

		select {
		case <-ctx.Done():
			return "stopped"
		case <-ticker.C:
		}
	}
}

// leave puts the desktop back and clears the session, whatever happened.
func (c *couchController) leave(ctx context.Context, state string) {
	c.mu.Lock()
	desk := c.desk
	applied := c.hooks
	c.phase = couch.PhaseLeaving
	c.mu.Unlock()
	c.svc.signalChange()

	// Undo the hooks before the layout: putting sound back on a desk output
	// that is still disabled would fail.
	if applied != nil {
		_ = applied.Leave(ctx, hooks.Env{Logf: c.svc.cfg.Logf})
	}

	if desk != nil {
		if err := c.svc.applyProfileLocked(ctx, *desk); err != nil {
			couch.AppendLog(state, "couch: restoring the desktop layout failed: %v", err)
		} else {
			couch.AppendLog(state, "couch: desktop layout restored")
		}
	}

	couch.ClearSession(state)
	c.mu.Lock()
	c.phase = couch.PhaseIdle
	c.desk = nil
	c.hooks = nil
	c.detector = nil
	c.cancel = nil
	c.started = time.Time{}
	c.trackedWindow = ""
	c.mu.Unlock()
	c.svc.signalChange()
}

// Stop ends the session and restores the desktop.
func (c *couchController) Stop() error {
	c.mu.Lock()
	cancel := c.cancel
	active := c.phase != couch.PhaseIdle && c.phase != ""
	c.mu.Unlock()

	if !active {
		return errors.New("no active couch session")
	}
	if cancel != nil {
		cancel()
		return nil
	}
	// Entering, with no session goroutine yet: put the desktop back directly.
	state, err := couch.StateDir()
	if err != nil {
		return err
	}
	c.leave(context.Background(), state)
	return nil
}

// hookEnv tells the hooks which display the session plays on, so the audio hook
// can prefer the sink that names it.
func (c *couchController) hookEnv(cfg couch.Config, monitors []hypr.Monitor) hooks.Env {
	env := hooks.Env{TVName: cfg.Layout.TVName, Logf: c.svc.cfg.Logf}
	for _, m := range monitors {
		if m.Name == cfg.Layout.TVName {
			env.TVDescription = m.Description
			break
		}
	}
	return env
}

func (c *couchController) setReason(reason string) {
	c.mu.Lock()
	c.reason = reason
	c.mu.Unlock()
}

// Reconcile undoes a session whose process died before it could restore.
//
// Called at daemon startup. Before, a SIGKILL mid-session left the TV layout
// applied and the desk dark, with nothing but a "stale" note in the status for
// the user to act on.
func (c *couchController) Reconcile(ctx context.Context) {
	state, err := couch.StateDir()
	if err != nil {
		return
	}
	orphan, found := couch.OrphanedSession(state)
	if !found {
		couch.ClearSession(state)
		return
	}
	couch.AppendLog(state, "couch: found an abandoned session from PID %d in phase %s; restoring the desktop",
		orphan.PID, orphan.Phase)
	if err := c.svc.applyProfileLocked(ctx, *orphan.Desk); err != nil {
		couch.AppendLog(state, "couch: could not restore the desktop layout: %v", err)
		c.svc.cfg.Logf("could not restore the desktop after an abandoned couch session: %v", err)
		return
	}
	couch.ClearSession(state)
	c.svc.cfg.Logf("restored the desktop layout left behind by an abandoned couch session")
}

// observeWindowEvent lets Big Picture opened outside couch mode pull the
// machine onto the TV.
//
// Only a CertainBigPicture tell counts: a weaker one would mean opening the
// Steam library on the desktop drags the user to the living room.
func (c *couchController) observeWindowEvent(ctx context.Context, ev hypr.Event) {
	if c.Active() {
		// Steam drops out of fullscreen when a game exits and comes back as
		// the 1100x700 floating window Omarchy's rule pins it to, so this has
		// to be re-asserted on every title change, not just once.
		c.mu.Lock()
		detector := c.detector
		c.mu.Unlock()
		if detector != nil && ev.Type == hypr.EventWindowTitle {
			couch.KeepBigPictureFullscreen(ctx, c.svc.client, detector)
		}
		return
	}
	if !couch.EventLooksLikeBigPicture(ev) {
		return
	}
	base := c.svc.cfg.ConfigDir
	cfg, err := couch.LoadConfig(base)
	if err != nil || !cfg.Enabled || !cfg.WatchBigPicture {
		return
	}

	detector := &couch.BigPictureDetector{Source: c.svc.client, Known: map[string]bool{}}
	if detector.CertainCount(ctx) == 0 {
		return
	}
	if err := c.Start(ctx, "Big Picture opened outside couch mode"); err != nil && !errors.Is(err, errCouchBusy) {
		c.svc.cfg.Logf("could not enter couch mode for an external Big Picture: %v", err)
	}
}

// observeControllers enters couch mode when a gamepad shows up, which is the
// most console-like trigger there is: turn the pad on, the TV comes up.
func (c *couchController) observeControllers(ctx context.Context) {
	if c.Active() {
		return
	}
	cfg, err := couch.LoadConfig(c.svc.cfg.ConfigDir)
	if err != nil || !cfg.Enabled || !cfg.EnterOnControllerConnect {
		return
	}
	if couch.ConnectedControllers() == 0 {
		return
	}
	if err := c.Start(ctx, "a controller was connected"); err != nil && !errors.Is(err, errCouchBusy) {
		c.svc.cfg.Logf("could not enter couch mode for a connected controller: %v", err)
	}
}
