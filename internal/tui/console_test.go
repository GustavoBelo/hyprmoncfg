package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/crmne/hyprmoncfg/internal/console"
	"github.com/crmne/hyprmoncfg/internal/hypr"
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

// consoleModel is a Console tab wide enough to render both panes without
// wrapping, with two displays to choose between and two sessions to come back
// to. Tests narrow it or take things away from it as they need.
func consoleModel(t *testing.T, cfg *console.Config) Model {
	t.Helper()
	return Model{
		styles: newStyles(),
		tab:    tabConsole,
		width:  160,
		height: 40,
		monitors: []hypr.Monitor{
			{Name: "DP-1", Description: "Dell U2720Q"},
			{Name: "HDMI-A-1", Description: "Samsung Q80"},
		},
		consoleSessions: []string{"hyprland.desktop", "omarchy.desktop"},
		consoleConfig:   cfg,
		consoleReady:    true,
		consoleHosted:   true,
	}
}

func consoleKey(t *testing.T, m Model, key string) Model {
	t.Helper()
	var msg tea.KeyMsg
	switch key {
	case "up", "down", "left", "right", "enter", "esc":
		msg = tea.KeyMsg{Type: map[string]tea.KeyType{
			"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft,
			"right": tea.KeyRight, "enter": tea.KeyEnter, "esc": tea.KeyEsc,
		}[key]}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	updated, _ := m.updateConsoleKeys(msg)
	return updated.(Model)
}

func consoleView(t *testing.T, m Model) string {
	t.Helper()
	return ansi.Strip(m.renderConsoleView(24))
}

// Cycling has to wrap in both directions, or the last option in a list is
// reachable only by pressing right until it comes round.
func TestCycleValueWrapsInBothDirections(t *testing.T) {
	values := []string{"a", "b", "c"}
	for _, tc := range []struct {
		name    string
		current string
		dir     int
		want    string
	}{
		{"forward", "a", 1, "b"},
		{"forward past the end", "c", 1, "a"},
		{"back", "b", -1, "a"},
		{"back past the start", "a", -1, "c"},
		{"a value not in the list starts over", "zzz", 1, "b"},
	} {
		if got := cycleValue(values, tc.current, tc.dir); got != tc.want {
			t.Errorf("%s: cycleValue(%q, %d) = %q, want %q", tc.name, tc.current, tc.dir, got, tc.want)
		}
	}
}

// With nothing to choose from there is nothing to cycle to, and returning the
// empty string would clear a setting the user was only looking at.
func TestCycleValueKeepsWhatItHasWithNothingToChooseFrom(t *testing.T) {
	if got := cycleValue(nil, "DP-1", 1); got != "DP-1" {
		t.Errorf("cycleValue(nil) = %q, want the current value kept", got)
	}
}

func TestOrNotSetNamesTheAbsence(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "not set"},
		{"   ", "not set"},
		{"DP-1", "DP-1"},
	} {
		if got := orNotSet(tc.in); got != tc.want {
			t.Errorf("orNotSet(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestConnectedOutputNamesListsEveryDisplay(t *testing.T) {
	got := connectedOutputNames([]hypr.Monitor{{Name: "DP-1"}, {Name: "HDMI-A-1"}})
	if strings.Join(got, ",") != "DP-1,HDMI-A-1" {
		t.Errorf("connectedOutputNames = %q, want both displays in order", got)
	}
}

// The cursor has to stop at both ends. Wrapping from the last row to the first
// would put the controller trigger one keypress from the display chooser.
func TestConsoleKeysStopAtBothEndsOfTheList(t *testing.T) {
	m := consoleModel(t, nil)

	for i := 0; i < 10; i++ {
		m = consoleKey(t, m, "down")
	}
	if got, want := m.consoleSelected, len(m.consoleRows(console.Config{}))-1; got != want {
		t.Errorf("consoleSelected = %d after pressing down ten times, want %d", got, want)
	}

	for i := 0; i < 10; i++ {
		m = consoleKey(t, m, "up")
	}
	if m.consoleSelected != 0 {
		t.Errorf("consoleSelected = %d after pressing up ten times, want 0", m.consoleSelected)
	}
}

func TestConsoleKeysMoveWithVimKeysToo(t *testing.T) {
	m := consoleModel(t, nil)

	if m = consoleKey(t, m, "j"); m.consoleSelected != 1 {
		t.Errorf("j left the cursor at %d, want 1", m.consoleSelected)
	}
	if m = consoleKey(t, m, "k"); m.consoleSelected != 0 {
		t.Errorf("k left the cursor at %d, want 0", m.consoleSelected)
	}
}

// An index off the end has to land somewhere real rather than reading past the
// slice, because the row list is rebuilt on every keypress.
func TestConsoleRowAtClampsToTheFirstRow(t *testing.T) {
	m := consoleModel(t, nil)
	for _, index := range []int{-1, 99} {
		if got := m.consoleRowAt(console.Config{}, index); got.field != consoleFieldTV {
			t.Errorf("consoleRowAt(%d) = %v, want the first row", index, got.field)
		}
	}
}

// Choosing the TV records the description as well as the connector. The
// description is what finds the display's HDMI audio pin, so a connector
// recorded without one plays the console on the TV with the sound still coming
// out of the desk speakers.
func TestArrowKeysRecordTheDisplaysDescriptionAsWellAsItsName(t *testing.T) {
	m := consoleModel(t, nil)

	m = consoleKey(t, m, "right")

	if m.consoleConfig == nil {
		t.Fatal("choosing a display recorded nothing")
	}
	if m.consoleConfig.TVName != "HDMI-A-1" {
		t.Errorf("TVName = %q, want the next display", m.consoleConfig.TVName)
	}
	if m.consoleConfig.TVDescription != "Samsung Q80" {
		t.Errorf("TVDescription = %q, want the description of the display chosen", m.consoleConfig.TVDescription)
	}
	if !m.consoleDirty {
		t.Error("choosing a display did not mark the draft dirty")
	}
}

func TestArrowKeysCycleTheBootMode(t *testing.T) {
	m := consoleModel(t, &console.Config{Boot: console.BootDesktop})
	m.consoleSelected = 1

	m = consoleKey(t, m, "right")
	if m.consoleConfig.Boot != console.BootConsole {
		t.Errorf("Boot = %q, want %q", m.consoleConfig.Boot, console.BootConsole)
	}
	m = consoleKey(t, m, "right")
	if m.consoleConfig.Boot != console.BootLast {
		t.Errorf("Boot = %q, want %q", m.consoleConfig.Boot, console.BootLast)
	}
	m = consoleKey(t, m, "left")
	if m.consoleConfig.Boot != console.BootConsole {
		t.Errorf("Boot = %q, want left to go back", m.consoleConfig.Boot)
	}
}

func TestArrowKeysCycleTheSessionToComeBackTo(t *testing.T) {
	m := consoleModel(t, &console.Config{DesktopSession: "hyprland.desktop"})
	m.consoleSelected = 2

	m = consoleKey(t, m, "right")

	if m.consoleConfig.DesktopSession != "omarchy.desktop" {
		t.Errorf("DesktopSession = %q, want the next session", m.consoleConfig.DesktopSession)
	}
}

func TestArrowKeysToggleTheControllerTrigger(t *testing.T) {
	m := consoleModel(t, &console.Config{})
	m.consoleSelected = 3

	m = consoleKey(t, m, "right")
	if !m.consoleConfig.EnterOnControllerConnect {
		t.Error("right did not turn the trigger on")
	}
	m = consoleKey(t, m, "left")
	if m.consoleConfig.EnterOnControllerConnect {
		t.Error("left did not turn the trigger back off")
	}
}

// A keypress that changes nothing must not mark the draft dirty: a dirty draft
// is what Enter refuses to start the console with, so a spurious one leaves the
// tab demanding that the user save an edit they never made.
func TestArrowKeysOnAnEmptyListChangeNothing(t *testing.T) {
	m := consoleModel(t, nil)
	m.monitors = nil
	m.consoleSessions = nil

	for _, row := range []int{0, 2} {
		m.consoleSelected = row
		after := consoleKey(t, m, "right")
		if after.consoleDirty {
			t.Errorf("row %d marked the draft dirty with nothing to choose from", row)
		}
	}
}

// Starting the console with an unsaved draft would enter with the settings on
// disk, not the ones on screen -- on the display the user just changed away
// from, which is the one they are not looking at.
func TestEnterRefusesWhileThereAreUnsavedChanges(t *testing.T) {
	m := consoleModel(t, &console.Config{TVName: "DP-1"})
	m.consoleDirty = true

	updated, cmd := m.updateConsoleKeys(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)

	if cmd != nil {
		t.Fatal("Enter started the console with unsaved changes")
	}
	if !strings.Contains(got.status, "Save your changes first") {
		t.Errorf("status = %q, want it to say what to do", got.status)
	}
}

// Without the hosting session there is nothing to come back to, and the switch
// is one way. Naming the command is the whole of the fix.
func TestStartConsoleRefusesAnUnhostedSession(t *testing.T) {
	m := consoleModel(t, &console.Config{TVName: "DP-1"})
	m.consoleHosted = false

	updated, _ := m.startConsole()
	got := updated.(Model)

	if !strings.Contains(got.status, "console setup") {
		t.Errorf("status = %q, want it to name the command that fixes this", got.status)
	}
}

func TestStartConsoleRefusesWithoutADisplay(t *testing.T) {
	m := consoleModel(t, &console.Config{})

	updated, _ := m.startConsole()
	got := updated.(Model)

	if !strings.Contains(got.status, "display the console plays on") {
		t.Errorf("status = %q, want it to say a display is missing", got.status)
	}
}

// The countdown lives in the daemon because it has to outlive this process:
// the TUI is closed along with the rest of the desktop. With no daemon there is
// nobody to hand it to, and saying so beats appearing to do nothing.
func TestStartConsoleNeedsADaemonToHandTheSessionTo(t *testing.T) {
	m := consoleModel(t, &console.Config{TVName: "DP-1"})

	updated, _ := m.startConsole()
	got := updated.(Model)

	if !strings.Contains(got.status, "daemon is not running") {
		t.Errorf("status = %q, want it to say there is nothing to hand the session to", got.status)
	}
}

func TestConsoleViewShowsEverySetting(t *testing.T) {
	m := consoleModel(t, &console.Config{
		TVName:         "HDMI-A-1",
		Boot:           console.BootConsole,
		DesktopSession: "hyprland.desktop",
	})

	got := consoleView(t, m)

	for _, want := range []string{"Plays on", "Starts in", "Comes back to", "Start on controller", "HDMI-A-1", "console"} {
		if !strings.Contains(got, want) {
			t.Errorf("the Console tab did not show %q:\n%s", want, got)
		}
	}
}

// The marker is the only thing saying which row the arrow keys will change.
func TestConsoleViewMarksTheSelectedRow(t *testing.T) {
	m := consoleModel(t, &console.Config{TVName: "DP-1"})

	first := consoleView(t, m)
	if !strings.Contains(first, "> Plays on") {
		t.Errorf("the first row was not marked as selected:\n%s", first)
	}

	m.consoleSelected = 3
	moved := consoleView(t, m)
	if strings.Contains(moved, "> Plays on") {
		t.Errorf("the marker stayed on a row the cursor had left:\n%s", moved)
	}
	if !strings.Contains(moved, "> Start on controller") {
		t.Errorf("the marker did not follow the cursor:\n%s", moved)
	}
}

// The suffix is noise in a narrow pane and pushes the value onto a second line.
func TestConsoleViewTrimsTheDesktopSuffix(t *testing.T) {
	m := consoleModel(t, &console.Config{DesktopSession: "hyprland.desktop"})

	got := consoleView(t, m)

	if strings.Contains(got, "hyprland.desktop") {
		t.Errorf("the session was shown with its suffix:\n%s", got)
	}
	if !strings.Contains(got, "hyprland") {
		t.Errorf("the session was not shown at all:\n%s", got)
	}
}

func TestConsoleViewSaysWhetherTheSessionCanSwitch(t *testing.T) {
	m := consoleModel(t, &console.Config{TVName: "DP-1"})

	if got := consoleView(t, m); !strings.Contains(got, "This session can switch.") {
		t.Errorf("a hosted session was not reported as such:\n%s", got)
	}

	m.consoleHosted = false
	unhosted := consoleView(t, m)
	if !strings.Contains(unhosted, "This session cannot switch.") {
		t.Errorf("an unhosted session was not reported as such:\n%s", unhosted)
	}
	if !strings.Contains(unhosted, "hyprmoncfg console setup") {
		t.Errorf("the fix was not spelled out:\n%s", unhosted)
	}
}

// The status pane is where the tab says its own keys, and an unsaved draft is
// the one state where pressing Enter does not do what the footer promises.
func TestConsoleViewWarnsAboutUnsavedChanges(t *testing.T) {
	m := consoleModel(t, &console.Config{TVName: "DP-1"})
	m.consoleDirty = true

	got := consoleView(t, m)

	if !strings.Contains(got, "Unsaved changes.") {
		t.Errorf("the tab did not warn about the draft:\n%s", got)
	}
	if !strings.Contains(got, "s saves them, r puts them back.") {
		t.Errorf("the tab did not say how to resolve it:\n%s", got)
	}
}

// A terminal under 96 columns stacks the panes instead of putting them side by
// side, and nothing may spill past the edge.
func TestConsoleViewFitsANarrowTerminal(t *testing.T) {
	m := consoleModel(t, &console.Config{TVName: "DP-1"})
	m.width = 80

	got := consoleView(t, m)

	if !strings.Contains(got, "Plays on") {
		t.Errorf("the settings disappeared in a narrow terminal:\n%s", got)
	}
	if !strings.Contains(got, "This session can switch.") {
		t.Errorf("the status pane disappeared in a narrow terminal:\n%s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if width := lipgloss.Width(line); width > m.width {
			t.Fatalf("a line is %d columns wide in an %d column terminal: %q", width, m.width, line)
		}
	}
}

// The Layout tab's shortcut takes the display from the live monitor rather than
// from the edited row: the TV is identified by the EDID it is actually
// presenting, which an unapplied edit does not have.
func TestEnableConsoleFromLayoutTakesTheLiveDisplay(t *testing.T) {
	t.Setenv("DESKTOP_SESSION", "hyprland")
	m := consoleModel(t, nil)
	m.tab = tabLayout
	m.store = profile.NewStore(t.TempDir())
	m.editOutputs = []editableOutput{{Name: "DP-1"}, {Name: "HDMI-A-1"}}
	m.selectedOutput = 1

	updated, cmd := m.enableConsoleFromLayout()
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("choosing the display recorded nothing")
	}
	if got.tab != tabConsole {
		t.Error("the shortcut did not move to the Console tab")
	}
	if got.consoleSelected != 0 {
		t.Errorf("consoleSelected = %d, want the tab opened at the top", got.consoleSelected)
	}
	saved, ok := cmd().(consoleSavedMsg)
	if !ok {
		t.Fatalf("the shortcut produced %T, want the settings to be saved", cmd())
	}
	if saved.err != nil {
		t.Fatal(saved.err)
	}
	if saved.cfg.TVName != "HDMI-A-1" || saved.cfg.TVDescription != "Samsung Q80" {
		t.Errorf("recorded %q (%q), want the live display under the cursor", saved.cfg.TVName, saved.cfg.TVDescription)
	}
	// The session to come back to is filled in at the same time, because a TV
	// with nowhere to return to is not a console anyone can leave.
	if saved.cfg.DesktopSession != "hyprland.desktop" {
		t.Errorf("DesktopSession = %q, want the session the user is logged into", saved.cfg.DesktopSession)
	}
}

func TestEnableConsoleFromLayoutRefusesADisplayThatIsNotConnected(t *testing.T) {
	m := consoleModel(t, nil)
	m.editOutputs = []editableOutput{{Name: "DVI-D-1"}}
	m.selectedOutput = 0

	updated, cmd := m.enableConsoleFromLayout()
	got := updated.(Model)

	if cmd != nil {
		t.Fatal("a disconnected display was recorded as the TV")
	}
	if !strings.Contains(got.status, "not connected") {
		t.Errorf("status = %q, want it to say the display is not connected", got.status)
	}
}

func TestEnableConsoleFromLayoutRefusesWithNothingSelected(t *testing.T) {
	m := consoleModel(t, nil)
	m.selectedOutput = -1

	updated, cmd := m.enableConsoleFromLayout()
	got := updated.(Model)

	if cmd != nil {
		t.Fatal("the shortcut recorded a display that was never chosen")
	}
	if !strings.Contains(got.status, "Select the display") {
		t.Errorf("status = %q, want it to say what to do first", got.status)
	}
}

// The environment is the cheap answer and the refresh already has the list.
// Asking the user manager here would block the update loop for as long as
// `systemctl --user show-environment` takes, which is up to fifteen seconds.
func TestCurrentSessionGuessPrefersTheEnvironment(t *testing.T) {
	t.Setenv("DESKTOP_SESSION", "omarchy")
	m := consoleModel(t, nil)

	if got := m.currentSessionGuess(); got != "omarchy.desktop" {
		t.Errorf("currentSessionGuess = %q, want the session from the environment", got)
	}
}

// With one session installed there is nothing to disambiguate, so guessing it
// beats making the user choose from a list of one.
func TestCurrentSessionGuessFallsBackToTheOnlySessionInstalled(t *testing.T) {
	t.Setenv("DESKTOP_SESSION", "")
	m := consoleModel(t, nil)
	m.consoleSessions = []string{"hyprland.desktop"}

	if got := m.currentSessionGuess(); got != "hyprland.desktop" {
		t.Errorf("currentSessionGuess = %q, want the only session installed", got)
	}
}

// With several it would be a coin toss, and recording the wrong one sends the
// user back to a session they did not ask for.
func TestCurrentSessionGuessDeclinesToPickBetweenSeveral(t *testing.T) {
	t.Setenv("DESKTOP_SESSION", "")
	m := consoleModel(t, nil)

	if got := m.currentSessionGuess(); got != "" {
		t.Errorf("currentSessionGuess = %q, want no guess at all", got)
	}
}

// Saving an untouched tab must say so rather than send an empty request to the
// daemon and report success for a write that never happened.
func TestConsoleSaveSaysWhenThereIsNothingToSave(t *testing.T) {
	m := consoleModel(t, &console.Config{TVName: "DP-1"})

	updated, cmd := m.saveConsole()
	got := updated.(Model)

	if cmd != nil {
		t.Fatal("an untouched tab sent a write anyway")
	}
	if !strings.Contains(got.status, "Nothing to save.") {
		t.Errorf("status = %q, want it to say there was nothing to do", got.status)
	}
}

// Esc discards like r does. The status pane only advertises r, but Esc is what
// a user reaches for to back out of an edit, and leaving the draft dirty would
// then block Enter with no sign of why.
func TestConsoleEscDiscardsTheDraftLikeRDoes(t *testing.T) {
	for _, key := range []string{"r", "esc"} {
		m := consoleModel(t, &console.Config{TVName: "DP-1"})
		m.consoleDirty = true

		got := consoleKey(t, m, key)

		if got.consoleDirty {
			t.Errorf("%s left the draft dirty", key)
		}
		if got.consoleConfig != nil {
			t.Errorf("%s kept the draft it was meant to throw away", key)
		}
	}
}

// Discarding when there is nothing to discard must not claim it did something.
func TestConsoleDiscardOnACleanTabSaysNothing(t *testing.T) {
	m := consoleModel(t, &console.Config{TVName: "DP-1"})

	updated, _ := m.discardConsole()
	got := updated.(Model)

	if got.status != "" {
		t.Errorf("status = %q, want nothing claimed", got.status)
	}
	if got.consoleConfig == nil {
		t.Error("a clean tab had its settings thrown away")
	}
}

// The trigger is the one row whose value is a state rather than a name, and
// "on" is the state worth being sure reads differently: it is what closes the
// desktop when a pad wakes up.
func TestConsoleViewShowsTheTriggerBeingOn(t *testing.T) {
	m := consoleModel(t, &console.Config{TVName: "DP-1", EnterOnControllerConnect: true})

	got := consoleView(t, m)

	if !strings.Contains(got, "Start on controller") || !strings.Contains(got, "on") {
		t.Errorf("the tab did not show the trigger as on:\n%s", got)
	}

	m.consoleConfig = &console.Config{TVName: "DP-1"}
	if off := consoleView(t, m); !strings.Contains(off, "off") {
		t.Errorf("the tab did not show the trigger as off:\n%s", off)
	}
}

// s reaches saveConsole through the tab's own handler as well as through the
// global one the existing tests cover.
func TestConsoleSaveKeyReachesTheTabsOwnHandler(t *testing.T) {
	base := t.TempDir()
	m := consoleModel(t, &console.Config{TVName: "DP-1", DesktopSession: "hyprland.desktop"})
	m.store = profile.NewStore(base)
	m.consoleDirty = true

	_, cmd := m.updateConsoleKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd == nil {
		t.Fatal("s did nothing on the Console tab")
	}
	if saved, ok := cmd().(consoleSavedMsg); !ok || saved.err != nil {
		t.Fatalf("s produced %T (%v), want the settings saved", cmd(), saved.err)
	}

	onDisk, err := console.LoadConfig(base)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.TVName != "DP-1" {
		t.Errorf("tv_name = %q, want what was on the tab", onDisk.TVName)
	}
}
