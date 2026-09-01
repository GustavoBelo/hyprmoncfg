package daemon

import (
	"testing"
	"time"

	"github.com/crmne/hyprmoncfg/internal/console"
)

// Only the edge counts. A level test re-enters one poll after every exit, which
// is exactly what happened when this was first written: leaving the session put
// the user straight back into it, over and over, because the pad was still on.
func TestIsConsoleConnectEdge(t *testing.T) {
	for _, tc := range []struct {
		name     string
		previous int
		now      int
		want     bool
	}{
		{"a pad switched on", 0, 1, true},
		{"a second pad", 1, 2, false},
		{"still on after leaving", 2, 2, false},
		{"switched off", 1, 0, false},
		{"none at all", 0, 0, false},
	} {
		if got := isConsoleConnectEdge(tc.previous, tc.now); got != tc.want {
			t.Errorf("%s: isConsoleConnectEdge(%d, %d) = %v, want %v", tc.name, tc.previous, tc.now, got, tc.want)
		}
	}
}

func TestIsConsoleDisconnectEdge(t *testing.T) {
	for _, tc := range []struct {
		name     string
		previous int
		now      int
		want     bool
	}{
		{"the only pad switched off", 1, 0, true},
		{"one of two switched off", 2, 1, false},
		{"never any pad at all", 0, 0, false},
		{"a pad switched on", 0, 1, false},
	} {
		if got := isConsoleDisconnectEdge(tc.previous, tc.now); got != tc.want {
			t.Errorf("%s: isConsoleDisconnectEdge(%d, %d) = %v, want %v", tc.name, tc.previous, tc.now, got, tc.want)
		}
	}
}

// A machine with no controller sits at zero controllers for ever. A level test
// there called off every countdown within one poll -- from the launcher, the
// TUI, the command line -- and blamed a controller that was never plugged in.
func TestOnlyAControllerEntryIsCalledOffByAController(t *testing.T) {
	asked := &consoleController{armed: true, byController: false}
	if asked.byController && isConsoleDisconnectEdge(0, 0) {
		t.Error("an entry the user asked for must survive a machine with no controller")
	}

	started := &consoleController{armed: true, byController: true}
	if !(started.byController && isConsoleDisconnectEdge(1, 0)) {
		t.Error("switching the pad off must still call off the entry it started")
	}
}

// A caller with no opinion gets the daemon's, and the daemon's is shared with
// the command line so the two cannot drift apart.
func TestConsoleGrace(t *testing.T) {
	if got := consoleGrace(0); got != console.DefaultGrace {
		t.Errorf("consoleGrace(0) = %s, want %s", got, console.DefaultGrace)
	}
	if got := consoleGrace(-time.Second); got != console.DefaultGrace {
		t.Errorf("consoleGrace(-1s) = %s, want %s", got, console.DefaultGrace)
	}
	if got := consoleGrace(3 * time.Second); got != 3*time.Second {
		t.Errorf("consoleGrace(3s) = %s, want it honoured", got)
	}
}

// An entry nobody asked for waits longer than one that was asked for: switching
// a controller on is as often an accident as an intention.
func TestAnUnaskedEntryWaitsLonger(t *testing.T) {
	if console.TriggerGrace <= console.DefaultGrace {
		t.Errorf("TriggerGrace = %s, DefaultGrace = %s: the accident must get more time",
			console.TriggerGrace, console.DefaultGrace)
	}
}

// cancelArmed records who stopped it before cancelling, so the countdown can
// name them on its way out. Nothing to cancel is not an error state, it is a
// false return.
func TestCancelArmedRecordsWhoStoppedIt(t *testing.T) {
	c := &consoleController{}
	if c.cancelArmed("cancelled") {
		t.Error("cancelling nothing must report that there was nothing to cancel")
	}

	stopped := false
	c.armed, c.cancel = true, func() { stopped = true }
	if !c.cancelArmed("the controller was disconnected") {
		t.Fatal("an armed entry must be cancellable")
	}
	if !stopped {
		t.Error("cancelling did not stop the countdown")
	}
	if got := c.calledOffReason(); got != "the controller was disconnected" {
		t.Errorf("calledOffReason = %q, want the controller", got)
	}
}
