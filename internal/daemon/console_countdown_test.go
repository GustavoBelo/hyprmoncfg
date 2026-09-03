package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crmne/hyprmoncfg/internal/console"
)

// recordingEntry stands in for the installed hyprmoncfg. It records the
// arguments it was run with, which is how these tests tell "the countdown
// finished and closed the desktop" from "it did not".
const recordingEntry = `#!/bin/bash
printf '%s\n' "$*" >> "$CONSOLE_CALLS"
`

// failingEntry is a console session that cannot start, with more output than a
// notification can hold.
const failingEntry = `#!/bin/bash
printf '%s\n' "$*" >> "$CONSOLE_CALLS"
echo "gamescope-session.target could not be started"
echo "and a second line no popup has room for"
exit 1
`

// armFixture makes the countdown deterministic and harmless.
//
// PATH is REPLACED rather than prefixed, which is the one thing here that must
// not be relaxed: `arm` looks up hyprmoncfg and runs `console enter --yes` with
// it, so leaving the real one reachable would have this test close the desktop
// of whoever ran it. Replacing PATH also puts notify-send out of reach, and the
// bus address points at nothing, so notify.Dial falls back to the notifier that
// quietly does nothing and Countdown becomes a plain wait on its deadline.
func armFixture(t *testing.T, entry string) (*consoleController, *logRecorder, string) {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hyprmoncfg"), []byte(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls.log")

	t.Setenv("PATH", dir)
	t.Setenv("CONSOLE_CALLS", calls)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	rec := &logRecorder{}
	svc := &Service{cfg: Config{ConfigDir: t.TempDir(), Logf: rec.logf}}
	return &consoleController{svc: svc}, rec, calls
}

func entryCalls(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// The countdown has to actually end in an entry. Everything else in this file
// is about stopping it, and none of that means anything if the thing being
// stopped never happens.
func TestArmRunsTheEntryOnceTheCountdownIsOver(t *testing.T) {
	c, _, calls := armFixture(t, recordingEntry)

	if err := c.Arm(context.Background(), "a test", 30*time.Millisecond); err != nil {
		t.Fatalf("Arm: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		return strings.Contains(entryCalls(t, calls), "console enter --yes")
	}, "the countdown to run the entry")
}

// The most important test here. The notification's Cancel button, the pad being
// switched off and `hyprmoncfg console cancel` all end up in cancelArmed, and if
// it does not stop the entry then every one of them is decorative and the user
// loses everything they had open.
func TestCancellingBeforeTheDeadlineNeverClosesTheDesktop(t *testing.T) {
	c, _, calls := armFixture(t, recordingEntry)

	if err := c.Arm(context.Background(), "a test", 2*time.Second); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	waitFor(t, time.Second, c.Arming, "the entry to be armed")

	if !c.cancelArmed("cancelled") {
		t.Fatal("an armed entry reported that there was nothing to cancel")
	}
	waitFor(t, 2*time.Second, func() bool { return !c.Arming() }, "the countdown to stand down")

	// Well past the grace it was armed with, so a countdown that ignored the
	// cancel would have fired by now.
	time.Sleep(200 * time.Millisecond)
	if got := entryCalls(t, calls); got != "" {
		t.Fatalf("a cancelled entry still closed the desktop: %q", got)
	}
}

// Arming twice would leave two countdowns racing to close the same desktop, and
// only one of them cancellable.
func TestArmRefusesASecondEntryWhileOneIsPending(t *testing.T) {
	c, rec, _ := armFixture(t, recordingEntry)

	if err := c.Arm(context.Background(), "the first", 2*time.Second); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	waitFor(t, time.Second, c.Arming, "the entry to be armed")
	if err := c.Arm(context.Background(), "the second", 2*time.Second); err != nil {
		t.Fatalf("Arm: %v", err)
	}

	if got := strings.Count(rec.all(), "entering console mode in"); got != 1 {
		t.Errorf("%d countdowns were started, want 1:\n%s", got, rec.all())
	}
	c.cancelArmed("cancelled")
}

// A controller left armed for ever cannot be armed again, so leaving the desktop
// and coming back would never enter a second time.
func TestArmClearsTheArmingFlagWhenItIsDone(t *testing.T) {
	c, _, calls := armFixture(t, recordingEntry)

	if err := c.Arm(context.Background(), "a test", 30*time.Millisecond); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return strings.Contains(entryCalls(t, calls), "console enter")
	}, "the entry to run")
	waitFor(t, 2*time.Second, func() bool { return !c.Arming() }, "arming to be cleared")
}

// The announcement promised the desktop was about to close. Silence after a
// failure reads as "it worked", and if the desktop did close first the user has
// lost everything on it and been given no reason.
func TestArmSaysWhyItCouldNotEnter(t *testing.T) {
	c, rec, _ := armFixture(t, failingEntry)

	if err := c.Arm(context.Background(), "a test", 30*time.Millisecond); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return rec.contains("entering failed")
	}, "the failure to be reported")

	if !rec.contains("gamescope-session.target could not be started") {
		t.Errorf("the log did not carry the reason:\n%s", rec.all())
	}
}

// Command output carries usage text and stack-shaped detail. A popup has room
// for the sentence that matters and nothing else.
func TestFirstLineKeepsTheSentenceThatMatters(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"nothing at all", "", ""},
		{"newlines only", "\n\n\n", ""},
		{"one line", "it did not start", "it did not start"},
		{"blank lines first", "\n  \nit did not start\nand more", "it did not start"},
		{"indented", "   it did not start   \nsecond", "it did not start"},
	} {
		if got := firstLine(tc.in); got != tc.want {
			t.Errorf("%s: firstLine(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// The panel, the command line and a controller all arm the same countdown, so
// the display it announces is whatever the file says at that moment. Announcing
// anything else would promise a screen the machine is not going to use.
func TestChosenDisplayIsReadWhenTheEntryIsAnnounced(t *testing.T) {
	c, _, _ := armFixture(t, recordingEntry)
	base := c.svc.cfg.ConfigDir

	if got := c.chosenDisplay(); got != "" {
		t.Errorf("chosenDisplay = %q, want nothing chosen yet", got)
	}

	if err := console.SaveConfig(base, console.Config{TVName: "HDMI-A-1"}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if got := c.chosenDisplay(); got != "HDMI-A-1" {
		t.Errorf("chosenDisplay = %q, want %q", got, "HDMI-A-1")
	}

	if err := console.SaveConfig(base, console.Config{TVName: "DP-2"}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if got := c.chosenDisplay(); got != "DP-2" {
		t.Errorf("chosenDisplay = %q, want the file as it is now, not as it was", got)
	}
}
