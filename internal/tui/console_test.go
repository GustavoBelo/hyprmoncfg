package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/crmne/hyprmoncfg/internal/console"
	"github.com/crmne/hyprmoncfg/internal/profile"
)

// s has to reach the Console tab, which is what its own status pane promises:
// "s saves them, r puts them back". The global handler claimed both keys first,
// so s opened the profile-name dialog instead and the tab could not be saved at
// all -- a display chosen here never reached the file.
func TestConsoleTabSaveIsNotShadowedByTheProfileDialog(t *testing.T) {
	base := t.TempDir()
	cfg := console.Config{TVName: "DP-1", DesktopSession: "hyprland.desktop", Boot: console.BootDesktop}
	m := Model{
		styles:        newStyles(),
		tab:           tabConsole,
		store:         profile.NewStore(base),
		consoleConfig: &cfg,
		consoleDirty:  true,
	}

	_, cmd := m.updateMainKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd == nil {
		t.Fatal("s on the Console tab did nothing")
	}
	saved, ok := cmd().(consoleSavedMsg)
	if !ok {
		t.Fatalf("s on the Console tab produced %T, want the console settings to be saved", cmd())
	}
	if saved.err != nil {
		t.Fatal(saved.err)
	}

	onDisk, err := console.LoadConfig(base)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.TVName != "DP-1" {
		t.Errorf("tv_name = %q, want the display chosen on the tab", onDisk.TVName)
	}
}

// And r has to discard the Console draft. The global reset leaves consoleDirty
// alone, so a tab that could not be saved could not be cleared either: Enter
// refused with "save your changes first", and there was no way to do either.
func TestConsoleTabDiscardIsNotShadowedByTheGlobalReset(t *testing.T) {
	cfg := console.Config{TVName: "DP-1"}
	m := Model{
		styles:        newStyles(),
		tab:           tabConsole,
		consoleConfig: &cfg,
		consoleDirty:  true,
	}

	updated, _ := m.updateMainKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	got := updated.(Model)

	if got.consoleDirty {
		t.Error("r left the draft dirty, so the tab still cannot be saved or started")
	}
	if got.consoleConfig != nil {
		t.Error("r kept the draft it was meant to throw away")
	}
	if got.resetRequested {
		t.Error("r on the Console tab reset the layout draft as well")
	}
}

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
