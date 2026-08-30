package daemon

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/crmne/hyprmoncfg/internal/couch"
	"github.com/crmne/hyprmoncfg/internal/hypr"
	"github.com/crmne/hyprmoncfg/internal/profile"
)

// A session killed mid-flight leaves the TV layout applied with nothing to put
// the desktop back. The daemon has to undo it on the way up, before automatic
// matching sees the TV layout and adopts it as the desktop.
func TestReconcileRestoresAnAbandonedSession(t *testing.T) {
	// The desktop as the abandoned session recorded it, which is also what the
	// fake compositor reports once the layout has been applied.
	restored := `[{"id":1,"name":"eDP-1","description":"Framework Panel","make":"Framework","model":"Panel","serial":"A1","width":2880,"height":1800,"refreshRate":120,"x":100,"y":200,"scale":1,"transform":0,"disabled":false,"dpmsStatus":true,"mirrorOf":""}]`
	// The TV layout the session left behind: the desk output disabled.
	onTV := `[{"id":1,"name":"eDP-1","description":"Framework Panel","make":"Framework","model":"Panel","serial":"A1","width":0,"height":0,"refreshRate":0,"x":0,"y":0,"scale":1,"transform":0,"disabled":true,"dpmsStatus":true,"mirrorOf":""}]`
	env := newApplyBestTestEnvWithMonitors(t, onTV, restored)
	mon := hypr.Monitor{Name: "eDP-1", Description: "Framework Panel", Make: "Framework", Model: "Panel", Serial: "A1"}

	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	couchState, err := couch.StateDir()
	if err != nil {
		t.Fatalf("state dir: %v", err)
	}

	desk := profile.New(couch.DeskSnapshotName, []profile.OutputConfig{{
		Key: mon.HardwareKey(), Name: mon.Name, Enabled: true,
		Width: 2880, Height: 1800, Refresh: 120, X: 100, Y: 200, Scale: 1,
	}})
	// A pid that is certainly gone.
	session := couch.Session{PID: 999999, Phase: couch.PhasePlaying, Desk: &desk}
	if err := couch.WriteSession(couchState, session); err != nil {
		t.Fatalf("write session: %v", err)
	}

	svc := New(env.client, env.store, Config{
		MonitorsConf: env.monitorsConfPath,
		HyprConfig:   env.hyprlandConfigPath,
	})
	svc.couch.Reconcile(context.Background())

	if rendered := readMonitorsConf(t, env); !strings.Contains(rendered, "position = 100x200") {
		t.Fatalf("the abandoned session's desktop layout was not restored:\n%s", rendered)
	}
	if _, err := os.Stat(couch.SessionPath(couchState)); !os.IsNotExist(err) {
		t.Fatal("the session file should be cleared once reconciled")
	}
}

// A session file with no recorded desktop is nothing to act on, and must not
// leave a stale file behind either.
func TestReconcileClearsASessionWithNothingToRestore(t *testing.T) {
	env := newApplyBestTestEnv(t, `[{"id":1,"name":"eDP-1","description":"P","make":"M","model":"X","serial":"A1","width":1920,"height":1080,"refreshRate":60,"x":0,"y":0,"scale":1,"disabled":false,"dpmsStatus":true,"mirrorOf":""}]`)
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	couchState, _ := couch.StateDir()

	if err := couch.WriteSession(couchState, couch.Session{PID: 999999, Phase: couch.PhasePlaying}); err != nil {
		t.Fatalf("write session: %v", err)
	}

	svc := New(env.client, env.store, Config{
		MonitorsConf: env.monitorsConfPath,
		HyprConfig:   env.hyprlandConfigPath,
	})
	svc.couch.Reconcile(context.Background())

	if _, err := os.Stat(couch.SessionPath(couchState)); !os.IsNotExist(err) {
		t.Fatal("a session with nothing to restore should still be cleared")
	}
}

func TestStopWithoutASessionIsAnError(t *testing.T) {
	env := newApplyBestTestEnv(t, `[{"id":1,"name":"eDP-1","description":"P","make":"M","model":"X","serial":"A1","width":1920,"height":1080,"refreshRate":60,"x":0,"y":0,"scale":1,"disabled":false,"dpmsStatus":true,"mirrorOf":""}]`)
	svc := New(env.client, env.store, Config{MonitorsConf: env.monitorsConfPath, HyprConfig: env.hyprlandConfigPath})
	if err := svc.couch.Stop(); err == nil {
		t.Fatal("stopping with no session should report that")
	}
	if svc.couch.Active() {
		t.Fatal("a fresh controller must not read as active")
	}
	if got := svc.couch.Status().Phase; got != couch.PhaseIdle {
		t.Fatalf("phase = %q, want %q", got, couch.PhaseIdle)
	}
}
