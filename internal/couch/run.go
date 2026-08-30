package couch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/crmne/hyprmoncfg/internal/apply"
	"github.com/crmne/hyprmoncfg/internal/config"
	"github.com/crmne/hyprmoncfg/internal/couch/hooks"
	"github.com/crmne/hyprmoncfg/internal/hypr"
	"github.com/crmne/hyprmoncfg/internal/ipc"
	"github.com/crmne/hyprmoncfg/internal/profile"
)

const (
	monitorTick        = 2 * time.Second
	applyTimeout       = 15 * time.Second
	daemonApplyTimeout = 20 * time.Second
)

type Runner struct {
	Client             *hypr.Client
	Store              *profile.Store
	BaseDir            string
	MonitorsConfPath   string
	HyprlandConfigPath string
	Logf               func(format string, args ...any)
}

func (r Runner) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

func (r Runner) stateDir() (string, error) {
	return StateDir()
}

// ErrUnmanaged is returned when monitor configuration has been handed back to
// Hyprland. A couch session writes the generated layout through the same apply
// path as everything else, so it cannot run while hyprmoncfg is standing aside
// without silently taking control back -- which is exactly what the old code
// did through Engine.Apply's unconditional include repair.
var ErrUnmanaged = errors.New("hyprmoncfg is not managing monitor configuration; run `hyprmoncfg manage` first")

func (r Runner) Play(ctx context.Context) error {
	state, err := r.stateDir()
	if err != nil {
		return err
	}
	if existing, running := RunningSession(state); running {
		return fmt.Errorf("couch mode session already running (PID %d)", existing.PID)
	}

	cfg, err := LoadConfig(r.BaseDir)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return errors.New("Couch Mode is disabled; enable it with `hyprmoncfg couch enable`")
	}
	if !config.IsManaged(r.BaseDir) {
		return ErrUnmanaged
	}

	monitors, err := r.Client.Monitors(ctx)
	if err != nil {
		return err
	}

	consoleProfile, changed, err := EnsureConsoleProfile(r.Store, monitors, &cfg)
	if err != nil {
		return fmt.Errorf("prepare the console layout: %w", err)
	}
	if changed {
		if err := SaveConfig(r.BaseDir, cfg); err != nil {
			return err
		}
	}

	// The desk is whatever was live a moment ago, captured rather than named.
	// Naming it is how a session ended up logged as "TV=escritório desk=game",
	// with the two roles the wrong way round.
	deskProfile, err := r.captureDesk(ctx, monitors)
	if err != nil {
		return fmt.Errorf("capture the current layout: %w", err)
	}

	session := Session{
		PID:       os.Getpid(),
		Phase:     PhaseEntering,
		StartedAt: time.Now(),
		Desk:      &deskProfile,
	}
	if err := WriteSession(state, session); err != nil {
		return err
	}
	defer ClearSession(state)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	go func() {
		select {
		case <-signals:
			cancel()
		case <-ctx.Done():
		}
	}()

	AppendLog(state, "play: starting (tv=%s mode=%s hdr=%t vrr=%t desk=%s controllers-off=%t close-apps=%t apps=%v)",
		cfg.Layout.TVName, cfg.Layout.Mode, cfg.Layout.HDR, cfg.Layout.VRR, cfg.Layout.Desk,
		cfg.ExitOnControllersOff, cfg.CloseAppsEnabled, cfg.AppsToClose)

	if err := r.applyProfile(ctx, consoleProfile, state); err != nil {
		AppendLog(state, "play: applying the console layout failed: %v", err)
		return fmt.Errorf("apply the console layout: %w", err)
	}

	session.Phase = PhasePlaying
	_ = WriteSession(state, session)

	// The same session hooks the daemon runs. Without them this fallback would
	// leave sound on the desk speakers and the lock screen armed.
	hookEnv := hooks.Env{TVName: cfg.Layout.TVName, Logf: r.Logf}
	for _, m := range monitors {
		if m.Name == cfg.Layout.TVName {
			hookEnv.TVDescription = m.Description
			break
		}
	}
	applied := hooks.Enter(ctx, hookEnv, cfg.HookEnabled)
	if names := applied.Applied(); len(names) > 0 {
		AppendLog(state, "play: session hooks applied: %s", strings.Join(names, ", "))
	}
	defer func() {
		// Undo the hooks before the layout: putting sound back on a desk
		// output that is still disabled would fail.
		_ = applied.Leave(context.WithoutCancel(ctx), hookEnv)
	}()

	// Big Picture opens on the focused monitor, so focus the TV before asking
	// for it. This is a session action rather than a workspace rule in the
	// profile, which would fight whatever workspace layout the desktop uses.
	r.focusTV(ctx, cfg.Layout)

	// Snapshot before launching: any steam window that appears afterwards is
	// treated as Big Picture even when its title gives nothing away.
	detector := NewBigPictureDetector(ctx, r.Client)

	launcher, existingInstance, err := ResolveLauncher()
	if err != nil {
		AppendLog(state, "play: no Big Picture launcher found: %v", err)
		r.restoreQuiet(ctx, deskProfile, state)
		return fmt.Errorf("find Big Picture launcher: %w", err)
	}
	if cfg.Gamescope.Enabled {
		nested, gsErr := GamescopeCommand(cfg.Layout, cfg.Gamescope,
			BigPictureLauncher{Command: launcher.Command, Args: []string{"-gamepadui"}})
		if gsErr != nil {
			AppendLog(state, "play: gamescope unavailable, falling back to plain Big Picture: %v", gsErr)
		} else {
			launcher, existingInstance = nested, false
			AppendLog(state, "play: using gamescope (%s)", GamescopeSummary(cfg.Layout, cfg.Gamescope))
		}
	}
	if existingInstance {
		AppendLog(state, "play: Steam already running; requesting Big Picture via steam:// protocol")
	}

	knownPIDs := KnownSteamPIDs()
	if _, err := LaunchBigPicture(launcher); err != nil {
		AppendLog(state, "play: launching %s failed: %v", launcher.Command, err)
		r.restoreQuiet(ctx, deskProfile, state)
		return fmt.Errorf("launch Big Picture: %w", err)
	}
	AppendLog(state, "play: launched %s %s", launcher.Command, strings.Join(launcher.Args, " "))

	steamPID := ResolveSteamPID(existingInstance, knownPIDs, state)

	AppendLog(state, "play: waiting up to %s for the Big Picture window...", BigPictureWaitWindow)
	bpmSeen := WaitForBigPicture(ctx, detector, BigPictureWaitWindow)
	if bpmSeen {
		AppendLog(state, "play: Big Picture window detected; monitoring until it closes or Steam exits")
		if fixed := KeepBigPictureFullscreen(ctx, r.Client, detector); fixed > 0 {
			AppendLog(state, "play: put %d Big Picture window(s) fullscreen", fixed)
		}
	} else {
		AppendLog(state, "play: Big Picture window not detected; will wait for Steam to exit")
	}

	// Close tracked apps once play has actually been attempted, matching the
	// reference engine: whether or not the Big Picture window was found.
	if cfg.CloseAppsEnabled && len(cfg.AppsToClose) > 0 {
		go func() {
			time.Sleep(time.Duration(cfg.CloseAppsWaitSeconds) * time.Second)
			killed := CloseTrackedApps(context.WithoutCancel(ctx), r.Client, cfg.AppsToClose)
			if len(killed) > 0 {
				AppendLog(state, "apps: closed tracked processes %v", killed)
			} else {
				AppendLog(state, "apps: no running process matched %v", cfg.AppsToClose)
			}
		}()
	}

	reason := r.monitorSession(ctx, cfg, steamPID, bpmSeen, detector, state)
	AppendLog(state, "play: session ended (%s)", reason)

	session.Phase = PhaseLeaving
	_ = WriteSession(state, session)

	if reason == "controllers off" {
		closed := CloseBigPicture(ctx, r.Client, detector)
		AppendLog(state, "controllers: closed %d Big Picture window(s)", closed)
	}

	if err := r.applyProfile(context.WithoutCancel(ctx), deskProfile, state); err != nil {
		AppendLog(state, "restore: putting the desktop layout back failed: %v", err)
		return fmt.Errorf("restore the desktop layout: %w", err)
	}
	AppendLog(state, "restore: desktop layout restored")
	return nil
}

// captureDesk records the live layout so the session can put it back exactly,
// rather than re-deriving a winner afterwards.
func (r Runner) captureDesk(ctx context.Context, monitors []hypr.Monitor) (profile.Profile, error) {
	rules, err := r.Client.WorkspaceRules(ctx)
	if err != nil {
		rules = nil
	}
	desk := profile.FromState(DeskSnapshotName, monitors, rules)
	if len(desk.Outputs) == 0 {
		return profile.Profile{}, errors.New("Hyprland reported no displays")
	}
	return desk, nil
}

func (r Runner) focusTV(ctx context.Context, layout ConsoleLayout) {
	name := strings.TrimSpace(layout.TVName)
	if name == "" {
		return
	}
	if err := r.Client.Dispatch(ctx, "focusmonitor", name); err != nil {
		r.logf("could not focus %s before launching Big Picture: %v", name, err)
	}
}

func (r Runner) monitorSession(ctx context.Context, cfg Config, steamPID int, bpmSeen bool, detector *BigPictureDetector, stateDir string) string {
	tracker := &ControllerTracker{}
	ticker := time.NewTicker(monitorTick)
	defer ticker.Stop()

	seenBPM := bpmSeen
	for {
		count := detector.Count(ctx)
		if count > 0 {
			seenBPM = true
		}
		if seenBPM && count == 0 {
			return "Big Picture closed"
		}
		if steamPID > 0 && !ProcessAlive(steamPID) {
			return "Steam exited"
		}

		now := time.Now()
		if cfg.ExitOnControllersOff && tracker.Poll(now, ConnectedControllers()) {
			return "controllers off"
		}

		select {
		case <-ctx.Done():
			return "interrupted"
		case <-ticker.C:
		}
	}
}

// Restore puts the desktop layout back without starting a session. It prefers
// the snapshot the last session captured, which is exact, and falls back to
// letting the daemon's own matching pick when there is none.
func (r Runner) Restore(ctx context.Context) error {
	state, err := r.stateDir()
	if err != nil {
		return err
	}
	if !config.IsManaged(r.BaseDir) {
		return ErrUnmanaged
	}

	desk, ok := SnapshotDesk(state)
	if !ok {
		return errors.New("no desktop layout was recorded; apply a profile from the Profiles tab instead")
	}
	if err := r.applyProfile(ctx, desk, state); err != nil {
		return err
	}
	ClearSession(state)
	AppendLog(state, "restore: desktop layout restored manually")
	return nil
}

// Stop kills any running couch session process and clears the session file.
// It does NOT restore the desktop layout -- use Restore for that.
func (r Runner) Stop() error {
	state, err := r.stateDir()
	if err != nil {
		return err
	}
	session, running := RunningSession(state)
	if !running {
		ClearSession(state)
		return errors.New("no active couch session")
	}
	_ = syscall.Kill(session.PID, syscall.SIGTERM)
	time.Sleep(500 * time.Millisecond)
	if ProcessAlive(session.PID) {
		_ = syscall.Kill(session.PID, syscall.SIGKILL)
	}
	ClearSession(state)
	AppendLog(state, "stop: session (PID %d) killed", session.PID)
	return nil
}

func (r Runner) restoreQuiet(ctx context.Context, desk profile.Profile, state string) {
	if err := r.applyProfile(ctx, desk, state); err != nil {
		AppendLog(state, "restore: applying desk profile after failure failed too: %v", err)
		return
	}
	AppendLog(state, "restore: desk profile reapplied after failure")
}

func (r Runner) applyProfile(ctx context.Context, p profile.Profile, state string) error {
	AppendLog(state, "apply: entering %q", p.Name)
	if handled, err := r.applyViaDaemon(ctx, p); handled {
		return err
	}
	return r.applyDirect(ctx, p)
}

func (r Runner) applyViaDaemon(ctx context.Context, p profile.Profile) (bool, error) {
	path, err := ipc.SocketPath()
	if err != nil {
		return false, nil
	}
	dialCtx, cancel := context.WithTimeout(ctx, daemonApplyTimeout)
	client, err := ipc.Dial(dialCtx, path)
	cancel()
	if err != nil {
		return false, nil
	}
	defer client.Close()

	tx, err := client.Preview(ctx, ipc.PreviewParams{Profile: &p, TimeoutSeconds: 30})
	if err != nil {
		return true, fmt.Errorf("daemon preview: %w", err)
	}
	confirmCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Confirm(confirmCtx, tx.ID); err != nil {
		return true, fmt.Errorf("daemon confirm: %w", err)
	}
	return true, nil
}

func (r Runner) applyDirect(ctx context.Context, p profile.Profile) error {
	applyCtx, cancel := context.WithTimeout(ctx, applyTimeout)
	defer cancel()
	monitors, err := r.Client.Monitors(applyCtx)
	if err != nil {
		return err
	}
	engine := apply.Engine{
		Client:             r.Client,
		MonitorsConfPath:   r.MonitorsConfPath,
		HyprlandConfigPath: r.HyprlandConfigPath,
	}
	_, err = engine.Apply(applyCtx, p, monitors, apply.ApplyModeNonInteractive)
	return err
}

type StatusReport struct {
	Enabled              bool          `json:"enabled"`
	Configured           bool          `json:"configured"`
	Managed              bool          `json:"managed"`
	Layout               ConsoleLayout `json:"layout"`
	ExitOnControllersOff bool          `json:"exit_on_controllers_off"`
	CloseAppsEnabled     bool          `json:"close_apps_enabled"`
	AppsToClose          []string      `json:"apps_to_close,omitempty"`
	Running              bool          `json:"running"`
	Stale                bool          `json:"stale,omitempty"`
	Phase                Phase         `json:"phase,omitempty"`
	PID                  int           `json:"pid,omitempty"`
	StartedAt            time.Time     `json:"started_at,omitempty"`
	Duration             string        `json:"duration,omitempty"`
	Controllers          int           `json:"controllers"`
	RecentLog            []string      `json:"recent_log,omitempty"`
}

func BuildStatus(baseDir string) (StatusReport, error) {
	cfg, err := LoadConfig(baseDir)
	if err != nil {
		return StatusReport{}, err
	}
	report := StatusReport{
		Enabled:              cfg.Enabled,
		Configured:           cfg.Configured(),
		Managed:              config.IsManaged(baseDir),
		Layout:               cfg.Layout,
		ExitOnControllersOff: cfg.ExitOnControllersOff,
		CloseAppsEnabled:     cfg.CloseAppsEnabled,
		AppsToClose:          cfg.AppsToClose,
		Controllers:          ConnectedControllers(),
	}
	state, err := StateDir()
	if err == nil {
		if session, running := RunningSession(state); running {
			report.Running = true
			report.PID = session.PID
			report.Phase = session.Phase
			report.StartedAt = session.StartedAt
			if !session.StartedAt.IsZero() {
				d := time.Since(session.StartedAt)
				report.Duration = fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
			}
		} else if session, stale := StaleSession(state); stale {
			report.Stale = true
			report.PID = session.PID
			report.Phase = session.Phase
			report.StartedAt = session.StartedAt
		}
		report.RecentLog = LogTail(state, logTailLines)
	}
	return report, nil
}
