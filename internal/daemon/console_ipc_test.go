package daemon

import (
	"strings"
	"testing"

	"github.com/crmne/hyprmoncfg/internal/console"
	"github.com/crmne/hyprmoncfg/internal/ipc"
)

// consoleService is a daemon with nothing but a config directory, which is all
// the methods below reach. internal/ipc tests the wire against a fake handler;
// these test what the daemon itself does when the wire delivers.
func consoleService(t *testing.T) *Service {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	rec := &logRecorder{}
	return &Service{cfg: Config{ConfigDir: t.TempDir(), Logf: rec.logf}}
}

// Arming without asking is how a panel button ended a desktop session on a
// machine whose own status document said it was not ready.
func TestConsoleEnterRefusesWhenTheMachineIsNotReady(t *testing.T) {
	svc := consoleService(t)
	svc.console = &consoleController{svc: svc}

	err := svc.ConsoleEnter(ipc.ConsoleEnterParams{Trigger: "a panel"})
	if err == nil {
		t.Fatal("the daemon armed an entry on a machine that is not ready")
	}
	if !strings.Contains(err.Error(), "console mode is not ready") || !strings.Contains(err.Error(), "the desktop stays") {
		t.Errorf("error = %q, want it to say the desktop stays", err)
	}
	if !strings.Contains(err.Error(), "no display has been chosen") {
		t.Errorf("error = %q, want it to name the problem", err)
	}
	if svc.console.Arming() {
		t.Error("a refused entry was armed anyway")
	}
}

// A daemon built without console support answers the question rather than
// panicking on a nil controller.
func TestConsoleEnterSaysWhenConsoleModeIsUnavailable(t *testing.T) {
	svc := consoleService(t)

	err := svc.ConsoleEnter(ipc.ConsoleEnterParams{})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("error = %v, want console mode reported unavailable", err)
	}
}

// A client that knows about three settings must not clear a fourth it has never
// heard of, which is what makes this safe to call from an old panel.
func TestConsoleConfigureChangesOnlyWhatItSends(t *testing.T) {
	svc := consoleService(t)
	before := console.Config{
		TVName:                   "HDMI-A-1",
		TVDescription:            "Some TV",
		DesktopSession:           "hyprland.desktop",
		Boot:                     console.BootDesktop,
		EnterOnControllerConnect: true,
	}
	if err := console.SaveConfig(svc.cfg.ConfigDir, before); err != nil {
		t.Fatalf("save config: %v", err)
	}

	boot := string(console.BootConsole)
	if err := svc.ConsoleConfigure(ipc.ConsoleConfigureParams{Boot: &boot}); err != nil {
		t.Fatalf("ConsoleConfigure: %v", err)
	}

	after, err := console.LoadConfig(svc.cfg.ConfigDir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if after.Boot != console.BootConsole {
		t.Errorf("Boot = %q, want it changed to %q", after.Boot, console.BootConsole)
	}
	if after.TVName != before.TVName || after.TVDescription != before.TVDescription {
		t.Errorf("the TV was cleared: %q (%q)", after.TVName, after.TVDescription)
	}
	if after.DesktopSession != before.DesktopSession {
		t.Errorf("DesktopSession = %q, want it left alone", after.DesktopSession)
	}
	if !after.EnterOnControllerConnect {
		t.Error("the controller trigger was cleared by a request that never mentioned it")
	}
}

func TestConsoleConfigureRejectsAnUnknownBootMode(t *testing.T) {
	svc := consoleService(t)
	if err := console.SaveConfig(svc.cfg.ConfigDir, console.Config{Boot: console.BootDesktop}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	mode := "maybe"
	err := svc.ConsoleConfigure(ipc.ConsoleConfigureParams{Boot: &mode})
	if err == nil || !strings.Contains(err.Error(), "unknown boot mode") {
		t.Fatalf("error = %v, want an unknown boot mode refused", err)
	}

	after, loadErr := console.LoadConfig(svc.cfg.ConfigDir)
	if loadErr != nil {
		t.Fatalf("load config: %v", loadErr)
	}
	if after.Boot != console.BootDesktop {
		t.Errorf("Boot = %q, want the rejected request to have changed nothing", after.Boot)
	}
}

// The memoised requirements quote the settings that just changed -- "the display
// to hand over is X" -- so keeping them would have a panel read back the
// previous choice for the rest of the TTL, right after saving a new one.
func TestConsoleConfigureForgetsTheMemoisedRequirements(t *testing.T) {
	svc := consoleService(t)
	seedRequirements(svc, []console.Requirement{{OK: true, Have: "the display to hand over is DP-1"}})

	session := "hyprland.desktop"
	if err := svc.ConsoleConfigure(ipc.ConsoleConfigureParams{DesktopSession: &session}); err != nil {
		t.Fatalf("ConsoleConfigure: %v", err)
	}

	svc.consoleReqMu.Lock()
	remembered := svc.consoleReqs
	svc.consoleReqMu.Unlock()
	if remembered != nil {
		t.Errorf("the daemon still remembers %v, which quotes the setting that just changed", remembered)
	}
}

// Saying "the desktop will stay" when nothing was counting down reads as having
// stopped something that was never happening.
func TestConsoleCancelSaysWhenThereWasNothingToCancel(t *testing.T) {
	svc := consoleService(t)
	svc.console = &consoleController{svc: svc}

	err := svc.ConsoleCancel()
	if err == nil || !strings.Contains(err.Error(), "nothing was counting down") {
		t.Errorf("error = %v, want it to say nothing was pending", err)
	}
}

// Cancelling over IPC has to reach the same countdown the notification's own
// button does, or `console cancel` works only when it is not needed.
func TestConsoleCancelStopsAnArmedEntry(t *testing.T) {
	svc := consoleService(t)
	stopped := false
	svc.console = &consoleController{svc: svc, armed: true, cancel: func() { stopped = true }}

	if err := svc.ConsoleCancel(); err != nil {
		t.Fatalf("ConsoleCancel: %v", err)
	}
	if !stopped {
		t.Fatal("the countdown was not stopped")
	}
	if got := svc.console.calledOffReason(); got != "cancelled" {
		t.Errorf("calledOffReason = %q, want %q", got, "cancelled")
	}
}
