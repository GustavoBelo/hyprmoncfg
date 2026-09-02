package tui

import (
	"testing"

	"github.com/crmne/hyprmoncfg/internal/console"
)

// Only what changed goes to the daemon. The TV name is the one that matters:
// ConsoleConfigure resolves it against the connected displays and refuses a
// connector that is not plugged in, so a boot-mode edit made while the TV is off
// must not carry the display name along and fail for a reason the user did not
// touch.
func TestConsoleChangesSendsOnlyWhatChanged(t *testing.T) {
	saved := &console.Config{
		TVName:         "HDMI-A-1",
		DesktopSession: "hyprland.desktop",
		Boot:           console.BootDesktop,
	}
	next := *saved
	next.Boot = console.BootConsole

	params := consoleChanges(saved, next)

	if params.TVName != nil {
		t.Errorf("TVName = %q, want it left out: it did not change", *params.TVName)
	}
	if params.DesktopSession != nil {
		t.Errorf("DesktopSession = %q, want it left out", *params.DesktopSession)
	}
	if params.Boot == nil || *params.Boot != string(console.BootConsole) {
		t.Fatalf("Boot = %v, want the change that was made", params.Boot)
	}
}

// Nothing recorded yet means everything set is a change, or the first save from
// a fresh machine would send an empty request and record nothing.
func TestConsoleChangesFromNothingSendsEverything(t *testing.T) {
	next := console.Config{
		TVName:                   "DP-1",
		DesktopSession:           "omarchy.desktop",
		Boot:                     console.BootLast,
		EnterOnControllerConnect: true,
	}

	params := consoleChanges(nil, next)

	if params.TVName == nil || *params.TVName != "DP-1" {
		t.Errorf("TVName = %v", params.TVName)
	}
	if params.DesktopSession == nil || *params.DesktopSession != "omarchy.desktop" {
		t.Errorf("DesktopSession = %v", params.DesktopSession)
	}
	if params.Boot == nil || *params.Boot != string(console.BootLast) {
		t.Errorf("Boot = %v", params.Boot)
	}
	if params.Trigger == nil || !*params.Trigger {
		t.Errorf("Trigger = %v", params.Trigger)
	}
}

// Turning the trigger off is a change to false, which a "send it if it is set"
// rule would drop -- and the setting that closes the desktop when a pad wakes up
// is the last one that should silently fail to turn off.
func TestConsoleChangesSendsTheTriggerBeingTurnedOff(t *testing.T) {
	saved := &console.Config{TVName: "DP-1", EnterOnControllerConnect: true}
	next := *saved
	next.EnterOnControllerConnect = false

	params := consoleChanges(saved, next)

	if params.Trigger == nil {
		t.Fatal("turning the trigger off sent nothing")
	}
	if *params.Trigger {
		t.Error("the trigger was sent as still on")
	}
}

// Saving with nothing edited must send an empty request rather than re-asserting
// a TV name that may no longer be plugged in.
func TestConsoleChangesOnNoEditIsEmpty(t *testing.T) {
	saved := &console.Config{TVName: "HDMI-A-1", Boot: console.BootDesktop}

	params := consoleChanges(saved, *saved)

	if params.TVName != nil || params.Boot != nil || params.DesktopSession != nil || params.Trigger != nil {
		t.Errorf("params = %+v, want nothing to send", params)
	}
}

// An empty value is absence, not an instruction to clear the setting: the
// protocol has no way to say "unset", and sending "" would be read as a display
// called "" and refused.
func TestConsoleChangesNeverSendsAnEmptyValue(t *testing.T) {
	saved := &console.Config{TVName: "HDMI-A-1", DesktopSession: "hyprland.desktop", Boot: console.BootDesktop}
	next := console.Config{}

	params := consoleChanges(saved, next)

	if params.TVName != nil {
		t.Errorf("TVName = %q, want an empty name treated as absence", *params.TVName)
	}
	if params.DesktopSession != nil {
		t.Errorf("DesktopSession = %q, want an empty session treated as absence", *params.DesktopSession)
	}
	if params.Boot != nil {
		t.Errorf("Boot = %q, want an empty boot mode treated as absence", *params.Boot)
	}
}
