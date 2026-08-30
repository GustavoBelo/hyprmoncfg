package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/crmne/hyprmoncfg/internal/couch"
	"github.com/crmne/hyprmoncfg/internal/couch/hooks"
	"github.com/crmne/hyprmoncfg/internal/hypr"
	"github.com/crmne/hyprmoncfg/internal/profile"
)

func newCouchTestModel(t *testing.T) Model {
	t.Helper()
	return Model{
		store:  profile.NewStore(t.TempDir()),
		styles: newStyles(),
		mode:   modeMain,
		tab:    tabCouch,
		width:  120,
		height: 36,
	}
}

func TestTabsHideCouchModeWhenDisabled(t *testing.T) {
	m := Model{styles: newStyles(), tab: tabLayout}
	if tabs := m.renderTabs(); strings.Contains(tabs, "Couch Mode") {
		t.Fatalf("expected Couch Mode tab hidden while disabled, got:\n%s", tabs)
	}

	m.couchConfig = &couch.Config{Enabled: true}
	if tabs := m.renderTabs(); !strings.Contains(tabs, "Couch Mode") {
		t.Fatalf("expected Couch Mode tab visible once enabled, got:\n%s", tabs)
	}
}

func TestTabKeyFourRequiresEnabledCouchMode(t *testing.T) {
	m := newCouchTestModel(t)
	m.tab = tabLayout
	next, _ := m.updateMainKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	if next.(Model).tab != tabLayout {
		t.Fatalf("expected tab to stay on Layout while Couch Mode is disabled")
	}

	enabled := next.(Model)
	enabled.couchConfig = &couch.Config{Enabled: true}
	next, _ = enabled.updateMainKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	if next.(Model).tab != tabCouch {
		t.Fatalf("expected tab to switch to Couch Mode once enabled")
	}
}

func TestEnableFromLayoutRequiresTwoMonitors(t *testing.T) {
	m := newCouchTestModel(t)
	m.tab = tabLayout
	m.monitors = []hypr.Monitor{{}, {}}

	next, _ := m.updateLayoutKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	model := next.(Model)
	if !model.couchEnabled() {
		t.Fatalf("expected Couch Mode enabled with two live monitors")
	}
	if model.tab != tabCouch {
		t.Fatalf("expected tab switch to Couch Mode, got %v", model.tab)
	}
	cfg, err := couch.LoadConfig(model.couchBaseDir())
	if err != nil || !cfg.Enabled {
		t.Fatalf("expected persisted enabled config, got cfg=%+v err=%v", cfg, err)
	}
}

func TestEnableFromLayoutBlockedWithOneMonitor(t *testing.T) {
	m := newCouchTestModel(t)
	m.tab = tabLayout
	m.monitors = []hypr.Monitor{{}}

	next, _ := m.updateLayoutKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	model := next.(Model)
	if model.couchEnabled() || model.tab == tabCouch {
		t.Fatalf("expected enable to be blocked with a single display")
	}
}

func TestCouchTabTogglesAndPersists(t *testing.T) {
	m := newCouchTestModel(t)
	m.couchConfig = &couch.Config{Enabled: true, CloseAppsWaitSeconds: 5}

	m.couchSelected = m.couchRowIndex(*m.couchConfig, couchFieldExitOnControllersOff, 0)
	next, _ := m.updateCouchKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	model := next.(Model)
	if !model.couchConfig.ExitOnControllersOff {
		t.Fatalf("expected ExitOnControllersOff toggled on")
	}
	saved, err := couch.LoadConfig(model.couchBaseDir())
	if err != nil || !saved.ExitOnControllersOff {
		t.Fatalf("expected persisted toggle, got %+v err=%v", saved, err)
	}

	model.couchSelected = model.couchRowIndex(*model.couchConfig, couchFieldCloseWait, 0)
	cfg := model.ensureCouchConfig()
	next, _ = model.adjustCouchField(&cfg, 1)
	if next.(Model).couchConfig.CloseAppsWaitSeconds != 6 {
		t.Fatalf("expected close wait bumped to 6, got %d", next.(Model).couchConfig.CloseAppsWaitSeconds)
	}
}

func TestCouchAppRowRemoval(t *testing.T) {
	m := newCouchTestModel(t)
	m.couchConfig = &couch.Config{
		Enabled:          true,
		AppsToClose:      []string{"retroarch", "melonds"},
		CloseAppsEnabled: true,
	}
	m.couchSelected = m.couchRowIndex(*m.couchConfig, couchFieldApp, 0)
	cfg := m.ensureCouchConfig()
	next, _ := m.activateCouchField(&cfg)
	model := next.(Model)
	got := model.couchConfig.AppsToClose
	if len(got) != 1 || got[0] != "melonds" {
		t.Fatalf("expected retroarch removed, got %v", got)
	}
	saved, err := couch.LoadConfig(model.couchBaseDir())
	if err != nil || len(saved.AppsToClose) != 1 {
		t.Fatalf("expected persisted removal, got %+v err=%v", saved.AppsToClose, err)
	}
}

func TestRenderCouchViewShowsPanes(t *testing.T) {
	m := newCouchTestModel(t)
	m.couchManaged = true
	m.monitors = couchTestMonitors()
	m.couchConfig = &couch.Config{
		Enabled:              true,
		Layout:               couchTestLayout(),
		CloseAppsWaitSeconds: 5,
	}
	view := m.renderCouchView(24)
	for _, want := range []string{"Couch Mode Setup", "Session Status", "TV display", "TV mode", "Other displays"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in couch view, got:\n%s", want, view)
		}
	}
}

// The HDR row is offered only for a display whose EDID advertises it, so the
// tab never shows a toggle that cannot do anything.
func TestCouchHDRRowOnlyForACapableTV(t *testing.T) {
	m := newCouchTestModel(t)
	m.monitors = couchTestMonitors()
	cfg := couch.Config{Enabled: true, Layout: couchTestLayout(), CloseAppsWaitSeconds: 5}

	if m.couchRowIndex(cfg, couchFieldHDR, 0) != -1 {
		t.Fatal("HDR must not be offered for a display that does not advertise it")
	}

	m.couchHDRCapable = map[string]bool{"HDMI-A-1": true}
	if m.couchRowIndex(cfg, couchFieldHDR, 0) == -1 {
		t.Fatal("HDR should be offered once the TV advertises it")
	}
}

// Cycling the mode must stay inside what the chosen display reports.
func TestCouchModeCyclingStaysWithinTheDisplaysModes(t *testing.T) {
	m := newCouchTestModel(t)
	m.monitors = couchTestMonitors()
	m.couchManaged = true
	m.couchConfig = &couch.Config{Enabled: true, Layout: couchTestLayout(), CloseAppsWaitSeconds: 5}
	m.couchSelected = m.couchRowIndex(*m.couchConfig, couchFieldMode, 0)

	allowed := map[string]bool{}
	for _, mode := range couch.AvailableModes(couchTestMonitors()[0]) {
		allowed[mode] = true
	}

	model := m
	for i := 0; i < 8; i++ {
		cfg := model.ensureCouchConfig()
		next, _ := model.adjustCouchField(&cfg, 1)
		model = next.(Model)
		if got := model.couchConfig.Layout.Mode; !allowed[got] {
			t.Fatalf("cycling produced %q, which the TV does not report", got)
		}
	}
}

// Switching the TV must take the new display's own mode with it, or the layout
// would be left naming a mode the new panel cannot do.
func TestCouchSwitchingTVAdoptsThatDisplaysMode(t *testing.T) {
	m := newCouchTestModel(t)
	m.monitors = couchTestMonitors()
	m.couchManaged = true
	m.couchConfig = &couch.Config{Enabled: true, Layout: couchTestLayout(), CloseAppsWaitSeconds: 5}
	m.couchSelected = m.couchRowIndex(*m.couchConfig, couchFieldTV, 0)

	cfg := m.ensureCouchConfig()
	next, _ := m.adjustCouchField(&cfg, 1)
	model := next.(Model)

	if model.couchConfig.Layout.TVName != "DP-1" {
		t.Fatalf("expected the TV to move to DP-1, got %q", model.couchConfig.Layout.TVName)
	}
	if err := couch.ValidateConsoleLayout(model.couchConfig.Layout, model.monitors); err != nil {
		t.Fatalf("switching the TV left an invalid layout: %v", err)
	}
}

func couchTestMonitors() []hypr.Monitor {
	return []hypr.Monitor{
		{
			ID: 1, Name: "HDMI-A-1", Description: "Samsung SAMSUNG",
			Make: "Samsung", Model: "SAMSUNG", Serial: "0x01",
			Disabled: true, Scale: 1,
			AvailableModes: []string{"3840x2160@120.00Hz", "2560x1440@120.00Hz", "1920x1080@60.00Hz"},
		},
		{
			ID: 2, Name: "DP-1", Description: "Technical Concepts 25G64",
			Make: "Technical Concepts", Model: "25G64",
			Width: 1920, Height: 1080, RefreshRate: 300, Scale: 1,
			AvailableModes: []string{"1920x1080@300.00Hz", "1920x1080@120.00Hz"},
		},
	}
}

func couchTestLayout() couch.ConsoleLayout {
	tv := couchTestMonitors()[0]
	return couch.ConsoleLayout{
		TVKey:  tv.HardwareKey(),
		TVName: tv.Name,
		Mode:   "2560x1440@120.00Hz",
		Desk:   couch.DeskDisabled,
	}
}

// Only hooks this machine can run are offered; a switch that does nothing is
// worse than no switch.
func TestCouchOffersOnlyAvailableHooks(t *testing.T) {
	m := newCouchTestModel(t)
	m.monitors = couchTestMonitors()
	cfg := couch.Config{Enabled: true, Layout: couchTestLayout(), CloseAppsWaitSeconds: 5}

	offered := map[string]bool{}
	for _, row := range m.couchRows(cfg) {
		if row.field == couchFieldHook {
			offered[row.hook.Name()] = true
		}
	}
	for _, hook := range hooks.All() {
		if !hook.Available() && offered[hook.Name()] {
			t.Fatalf("hook %q is unavailable but was offered", hook.Name())
		}
	}
	for _, hook := range hooks.Available() {
		if !offered[hook.Name()] {
			t.Fatalf("available hook %q was not offered", hook.Name())
		}
	}
}

// A hook is on unless it has been turned off, so a machine that gains a
// capability starts using it without the user opting in again.
func TestCouchHookToggleRoundTrips(t *testing.T) {
	available := hooks.Available()
	if len(available) == 0 {
		t.Skip("no session hooks are available on this machine")
	}
	name := available[0].Name()

	m := newCouchTestModel(t)
	m.monitors = couchTestMonitors()
	m.couchManaged = true
	m.couchConfig = &couch.Config{Enabled: true, Layout: couchTestLayout(), CloseAppsWaitSeconds: 5}

	if !m.couchConfig.HookEnabled(name) {
		t.Fatalf("hook %q should default to on", name)
	}

	m.couchSelected = m.couchHookRowIndex(*m.couchConfig, name)
	cfg := m.ensureCouchConfig()
	next, _ := m.adjustCouchField(&cfg, 1)
	model := next.(Model)

	if model.couchConfig.HookEnabled(name) {
		t.Fatalf("hook %q should now be off", name)
	}
	saved, err := couch.LoadConfig(model.couchBaseDir())
	if err != nil || saved.HookEnabled(name) {
		t.Fatalf("the choice should persist, got %+v err=%v", saved.Hooks, err)
	}
}

// The gamescope frame-rate row only exists while gamescope is on, so it cannot
// be selected into nothing.
func TestCouchGamescopeFPSRowFollowsTheToggle(t *testing.T) {
	if !couch.GamescopeAvailable() {
		t.Skip("gamescope is not installed on this machine")
	}
	m := newCouchTestModel(t)
	m.monitors = couchTestMonitors()
	cfg := couch.Config{Enabled: true, Layout: couchTestLayout(), CloseAppsWaitSeconds: 5}

	if m.couchRowIndex(cfg, couchFieldGamescopeFPS, 0) != -1 {
		t.Fatal("the frame-rate row must not exist while gamescope is off")
	}
	cfg.Gamescope.Enabled = true
	if m.couchRowIndex(cfg, couchFieldGamescopeFPS, 0) == -1 {
		t.Fatal("the frame-rate row should appear once gamescope is on")
	}
}

// Every row has to render and be reachable, whatever the config: an
// unreachable row is a setting the user cannot change.
func TestEveryCouchRowRendersAndIsSelectable(t *testing.T) {
	m := newCouchTestModel(t)
	m.monitors = couchTestMonitors()
	m.couchManaged = true
	m.couchHDRCapable = map[string]bool{"HDMI-A-1": true}
	m.couchConfig = &couch.Config{
		Enabled:              true,
		Layout:               couchTestLayout(),
		CloseAppsEnabled:     true,
		AppsToClose:          []string{"retroarch"},
		CloseAppsWaitSeconds: 5,
		Gamescope:            couch.GamescopeSettings{Enabled: true},
	}

	rows := m.couchRows(*m.couchConfig)
	for i := range rows {
		model := m
		model.couchSelected = i
		if view := model.renderCouchView(30); view == "" {
			t.Fatalf("row %d rendered nothing", i)
		}
	}
}

// The picker exists because matching is exact: the WhatsApp web app's class is
// "chrome-web.whatsapp.com__-Default", which nobody types correctly. Picking it
// has to store that value verbatim.
func TestCouchAppPickerStoresTheExactToken(t *testing.T) {
	m := newCouchTestModel(t)
	m.couchConfig = &couch.Config{Enabled: true, CloseAppsEnabled: true, CloseAppsWaitSeconds: 5}
	m.couchPicker = &couchAppPickerState{
		Chosen: map[string]bool{},
		Rendered: []couch.CloseCandidate{
			{Token: "chrome-web.whatsapp.com__-Default", Label: "web.whatsapp.com", Running: true},
			{Token: "chromium", Label: "Chromium"},
		},
	}
	m.couchPicker.List = m.newCouchAppList(m.couchPicker)
	m.mode = modeCouchAppPicker

	// Tick the first entry, then save.
	next, _ := m.updateCouchAppPickerKeys(tea.KeyMsg{Type: tea.KeySpace})
	model := next.(Model)
	next, _ = model.updateCouchAppPickerKeys(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(Model)

	got := model.couchConfig.AppsToClose
	if len(got) != 1 || got[0] != "chrome-web.whatsapp.com__-Default" {
		t.Fatalf("expected the exact class to be stored, got %v", got)
	}
	if model.mode != modeMain {
		t.Fatalf("saving should close the picker, mode = %v", model.mode)
	}
	saved, err := couch.LoadConfig(model.couchBaseDir())
	if err != nil || len(saved.AppsToClose) != 1 {
		t.Fatalf("the selection should persist, got %v err=%v", saved.AppsToClose, err)
	}
}

// An entry the picker cannot show -- an app that simply is not running -- must
// survive the round trip rather than being dropped on save.
func TestCouchAppPickerKeepsEntriesItCannotShow(t *testing.T) {
	m := newCouchTestModel(t)
	m.couchConfig = &couch.Config{
		Enabled: true, CloseAppsEnabled: true, CloseAppsWaitSeconds: 5,
		AppsToClose: []string{"retroarch"},
	}
	m.couchPicker = &couchAppPickerState{
		Chosen:   map[string]bool{},
		Extra:    []string{"retroarch"},
		Rendered: []couch.CloseCandidate{{Token: "chromium", Label: "Chromium"}},
	}
	m.couchPicker.List = m.newCouchAppList(m.couchPicker)

	next, _ := m.updateCouchAppPickerKeys(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(Model).couchConfig.AppsToClose
	if len(got) != 1 || got[0] != "retroarch" {
		t.Fatalf("an app that is merely closed must stay on the list, got %v", got)
	}
}

// Esc leaves the close list exactly as it was.
func TestCouchAppPickerDiscardsOnEscape(t *testing.T) {
	m := newCouchTestModel(t)
	m.couchConfig = &couch.Config{
		Enabled: true, CloseAppsEnabled: true, CloseAppsWaitSeconds: 5,
		AppsToClose: []string{"chromium"},
	}
	m.couchPicker = &couchAppPickerState{
		Chosen:   map[string]bool{},
		Rendered: []couch.CloseCandidate{{Token: "code", Label: "Visual Studio Code"}},
	}
	m.couchPicker.List = m.newCouchAppList(m.couchPicker)
	m.mode = modeCouchAppPicker

	next, _ := m.updateCouchAppPickerKeys(tea.KeyMsg{Type: tea.KeySpace})
	next, _ = next.(Model).updateCouchAppPickerKeys(tea.KeyMsg{Type: tea.KeyEsc})
	model := next.(Model)

	if got := model.couchConfig.AppsToClose; len(got) != 1 || got[0] != "chromium" {
		t.Fatalf("escape must not change the list, got %v", got)
	}
	if model.couchPicker != nil || model.mode != modeMain {
		t.Fatal("escape should close the picker")
	}
}

// Opening the picker with a list already set must show those entries ticked,
// rather than making the user rebuild the selection every time.
func TestCouchAppPickerOpensWithCurrentChoicesTicked(t *testing.T) {
	m := newCouchTestModel(t)
	m.couchPicker = &couchAppPickerState{
		Chosen:   couch.MarkChosen(nil, []string{"Chromium"}),
		Rendered: []couch.CloseCandidate{{Token: "chromium", Label: "Chromium"}},
	}
	m.couchPicker.List = m.newCouchAppList(m.couchPicker)

	item, ok := m.couchPicker.List.Items()[0].(couchAppItem)
	if !ok || !item.chosen {
		t.Fatal("an app already on the list should open ticked")
	}
	if !strings.Contains(item.Title(), "[x]") {
		t.Fatalf("a ticked row should look ticked, got %q", item.Title())
	}
}

// The row shows what the user recognises and what will actually be stored.
func TestCouchAppPickerRowShowsBothNameAndToken(t *testing.T) {
	item := couchAppItem{candidate: couch.CloseCandidate{
		Token: "chrome-web.whatsapp.com__-Default", Label: "web.whatsapp.com", Running: true,
	}}
	title := item.Title()
	for _, want := range []string{"web.whatsapp.com", "chrome-web.whatsapp.com__-Default", "open now"} {
		if !strings.Contains(title, want) {
			t.Fatalf("expected %q in the row, got %q", want, title)
		}
	}
}
