package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/crmne/hyprmoncfg/internal/console"
	"github.com/crmne/hyprmoncfg/internal/hypr"
	"github.com/crmne/hyprmoncfg/internal/ipc"
)

// consoleField identifies a selectable row of the Console tab.
//
// The list is short, and much shorter than the couch-mode tab it replaces,
// because most of what that tab edited is no longer ours to decide: gamescope
// takes the connector's preferred mode and Steam changes resolution, HDR and
// the frame limiter per game from its own settings. Recording them here would
// be a second source of truth that disagrees with the one actually in charge.
type consoleField int

const (
	consoleFieldTV consoleField = iota
	consoleFieldBoot
	consoleFieldDesktopSession
	consoleFieldTrigger
)

// consoleRow is one visible row.
type consoleRow struct {
	field consoleField
}

func (m Model) consoleRows(console.Config) []consoleRow {
	return []consoleRow{
		{field: consoleFieldTV},
		{field: consoleFieldBoot},
		{field: consoleFieldDesktopSession},
		{field: consoleFieldTrigger},
	}
}

func (m Model) consoleRowAt(cfg console.Config, index int) consoleRow {
	rows := m.consoleRows(cfg)
	if index < 0 || index >= len(rows) {
		return consoleRow{field: consoleFieldTV}
	}
	return rows[index]
}

// consoleAvailable reports whether the tab is worth showing at all.
//
// A machine with no gamescope session cannot enter console mode, and offering a
// tab whose every action fails is worse than not offering it.
func (m Model) consoleAvailable() bool {
	return m.consoleReady
}

func (m Model) consoleBaseDir() string {
	if m.store == nil {
		return ""
	}
	return filepath.Dir(m.store.Dir())
}

func (m Model) ensureConsoleConfig() console.Config {
	if m.consoleConfig != nil {
		return *m.consoleConfig
	}
	return console.Config{}
}

// consoleSavedMsg is the result of writing a console setting, which happens off
// the update loop.
type consoleSavedMsg struct {
	cfg console.Config
	ok  string
	err error
}

// persistConsoleCmd records a console setting, through the daemon when there is
// one.
//
// One-writer IPC is the project's rule: while the daemon runs, everything else
// sends changes through it rather than racing over config files. This wrote
// console.json directly, so a change made on a panel and a change made here
// overwrote each other -- no corruption, the write is atomic, but a lost update,
// which is exactly what the rule exists to prevent.
//
// It is a tea.Cmd because it talks to a socket and to the disk. Run from the key
// handler it would hold the update loop for as long as the daemon took to
// answer, and the whole UI -- not just the Console tab -- would stop redrawing.
//
// Only the fields that actually changed are sent. The daemon re-derives the TV's
// EDID key and description from the live display, which is better than shipping
// this model's copy of them, but it refuses a connector that is not plugged in
// right now -- so a boot-mode edit made while the TV is off must not carry a
// display name with it.
func persistConsoleCmd(client *ipc.Client, base string, saved *console.Config, cfg console.Config, ok string) tea.Cmd {
	params := consoleChanges(saved, cfg)
	return func() tea.Msg {
		if base == "" {
			return consoleSavedMsg{err: fmt.Errorf("no config directory")}
		}
		if client != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := client.ConsoleConfigure(ctx, params); err != nil {
				return consoleSavedMsg{err: err}
			}
			return consoleSavedMsg{cfg: cfg, ok: ok}
		}
		// No daemon, so there is no one to race with and nothing to hand it to.
		if err := console.SaveConfig(base, cfg); err != nil {
			return consoleSavedMsg{err: err}
		}
		return consoleSavedMsg{cfg: cfg, ok: ok}
	}
}

// consoleChanges is what to send: the fields that differ from what is already
// recorded. A nil previous means nothing is recorded yet, so everything set is
// a change.
func consoleChanges(previous *console.Config, next console.Config) ipc.ConsoleConfigureParams {
	old := console.Config{}
	if previous != nil {
		old = *previous
	}
	params := ipc.ConsoleConfigureParams{}
	if next.TVName != old.TVName && next.TVName != "" {
		name := next.TVName
		params.TVName = &name
	}
	if next.Boot != old.Boot && next.Boot != "" {
		boot := string(next.Boot)
		params.Boot = &boot
	}
	if next.DesktopSession != old.DesktopSession && next.DesktopSession != "" {
		session := next.DesktopSession
		params.DesktopSession = &session
	}
	if next.EnterOnControllerConnect != old.EnterOnControllerConnect {
		trigger := next.EnterOnControllerConnect
		params.Trigger = &trigger
	}
	return params
}

// enableConsoleFromLayout is the Layout tab's shortcut: pick the display under
// the cursor as the TV and move to the Console tab.
func (m Model) enableConsoleFromLayout() (tea.Model, tea.Cmd) {
	if m.selectedOutput < 0 || m.selectedOutput >= len(m.editOutputs) {
		m.setStatusErr("Select the display you play on first.")
		return m, nil
	}
	// The live monitor, not the edited row: the TV is identified by the EDID it
	// is actually presenting, which an unapplied edit does not have.
	chosen := m.editOutputs[m.selectedOutput]
	var monitor hypr.Monitor
	found := false
	for _, live := range m.monitors {
		if live.Name == chosen.Name {
			monitor, found = live, true
			break
		}
	}
	if !found {
		m.setStatusErr("That display is not connected.")
		return m, nil
	}
	cfg := m.ensureConsoleConfig()
	cfg.TVName = monitor.Name
	cfg.TVKey = monitor.HardwareKey()
	cfg.TVDescription = monitor.Description
	if cfg.DesktopSession == "" {
		// Whatever the user is logged into, which is the only sane default for
		// where to come back to. The lookup can reach a `systemctl --user
		// show-environment` with a fifteen second timeout, so it belongs off the
		// update loop with the write.
		cfg.DesktopSession = m.currentSessionGuess()
	}
	m.tab = tabConsole
	m.consoleSelected = 0
	return m, persistConsoleCmd(m.ipc, m.consoleBaseDir(), m.consoleSaved, cfg,
		fmt.Sprintf("The console will play on %s.", monitor.Name))
}

// currentSessionGuess names the session to come back to without asking systemd.
//
// The refresh already lists them, and the one the user logged into is in the
// environment when there is one. Asking the user manager here would block the
// update loop for as long as it took to answer.
func (m Model) currentSessionGuess() string {
	if name := console.CurrentDesktopSession(context.Background(), nil); name != "" {
		return name
	}
	if len(m.consoleSessions) == 1 {
		return m.consoleSessions[0]
	}
	return ""
}

func (m Model) updateConsoleKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cfg := m.ensureConsoleConfig()
	rows := m.consoleRows(cfg)

	switch msg.String() {
	case "up", "k":
		if m.consoleSelected > 0 {
			m.consoleSelected--
		}
		return m, nil
	case "down", "j":
		if m.consoleSelected < len(rows)-1 {
			m.consoleSelected++
		}
		return m, nil
	case "left", "h":
		return m.adjustConsoleField(&cfg, -1)
	case "right", "l":
		return m.adjustConsoleField(&cfg, 1)
	case "s":
		return m.saveConsole()
	case "r", "esc":
		return m.discardConsole()
	case "enter":
		if m.consoleDirty {
			m.setStatusErr("Save your changes first, or press r to discard them.")
			return m, nil
		}
		return m.startConsole()
	}
	return m, nil
}

func (m Model) adjustConsoleField(cfg *console.Config, dir int) (tea.Model, tea.Cmd) {
	row := m.consoleRowAt(*cfg, m.consoleSelected)
	switch row.field {
	case consoleFieldTV:
		names := connectedOutputNames(m.monitors)
		if len(names) == 0 {
			return m, nil
		}
		cfg.TVName = cycleValue(names, cfg.TVName, dir)
		for _, mon := range m.monitors {
			if mon.Name == cfg.TVName {
				cfg.TVKey = mon.HardwareKey()
				cfg.TVDescription = mon.Description
			}
		}
	case consoleFieldBoot:
		modes := []string{string(console.BootDesktop), string(console.BootConsole), string(console.BootLast)}
		cfg.Boot = console.BootMode(cycleValue(modes, string(cfg.Boot), dir))
	case consoleFieldDesktopSession:
		files := m.consoleSessions
		if len(files) == 0 {
			return m, nil
		}
		cfg.DesktopSession = cycleValue(files, cfg.DesktopSession, dir)
	case consoleFieldTrigger:
		cfg.EnterOnControllerConnect = !cfg.EnterOnControllerConnect
	default:
		return m, nil
	}
	m.consoleConfig = cfg
	m.consoleDirty = true
	return m, nil
}

// saveConsole writes the draft. Nothing else does, which is what makes the
// arrow keys safe to explore with.
func (m Model) saveConsole() (tea.Model, tea.Cmd) {
	if !m.consoleDirty {
		m.setStatusOK("Nothing to save.")
		return m, nil
	}
	cfg := m.ensureConsoleConfig()
	return m, persistConsoleCmd(m.ipc, m.consoleBaseDir(), m.consoleSaved, cfg, "Console settings saved.")
}

// discardConsole throws the draft away and goes back to what is on disk.
func (m Model) discardConsole() (tea.Model, tea.Cmd) {
	if !m.consoleDirty {
		return m, nil
	}
	m.consoleConfig = nil
	m.consoleDirty = false
	m.setStatusOK("Changes discarded.")
	return m, nil
}

// startConsole asks the daemon to enter, rather than doing it here.
//
// The daemon owns the countdown because the countdown has to outlive whatever
// asked for it: this TUI is about to be closed along with the rest of the
// desktop.
func (m Model) startConsole() (tea.Model, tea.Cmd) {
	if !m.consoleHosted {
		m.setStatusErr("This session cannot switch. Run `hyprmoncfg console setup` and log in again.")
		return m, nil
	}
	cfg := m.ensureConsoleConfig()
	if !cfg.Configured() {
		m.setStatusErr("Choose the display the console plays on first.")
		return m, nil
	}
	client := m.ipc
	if client == nil {
		m.setStatusErr("The daemon is not running, so there is nothing to hand the session to.")
		return m, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.ConsoleEnter(ctx, "the TUI", 0); err != nil {
		m.setStatusErr(err.Error())
		return m, nil
	}
	m.setStatusOK("Console mode starting. Click the notification to call it off.")
	return m, nil
}

func (m Model) renderConsoleView(height int) string {
	settings := m.consoleSettingsLines()
	leftStyle := m.paneStyle(paneToneFocused)

	if m.terminalWidth() < 96 {
		width := m.terminalWidth() - m.styles.app.GetHorizontalFrameSize()
		settingsHeight := clampInt(len(settings)+2, 6, (height*2)/3)
		innerW := max(1, width-leftStyle.GetHorizontalFrameSize())
		leftBody := fitBlock(strings.Join(settings, "\n"), innerW, max(1, settingsHeight-leftStyle.GetVerticalFrameSize()))
		left := m.renderTitledPane(paneToneFocused, "Console Mode", leftBody, width)
		right := m.renderConsoleStatusPane(width, max(3, height-settingsHeight))
		return lipgloss.JoinVertical(lipgloss.Left, left, right)
	}

	leftWidth, rightWidth := m.sidePaneWidths(35)
	leftBody := fitBlock(strings.Join(settings, "\n"), max(1, leftWidth-leftStyle.GetHorizontalFrameSize()), max(1, height-leftStyle.GetVerticalFrameSize()))
	left := m.renderTitledPane(paneToneFocused, "Console Mode", leftBody, leftWidth)
	right := m.renderConsoleStatusPane(rightWidth, height)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", paneGapWidth), right)
}

func (m Model) consoleSettingsLines() []string {
	cfg := m.ensureConsoleConfig()
	rows := m.consoleRows(cfg)
	lines := make([]string, 0, len(rows)+6)

	for i, row := range rows {
		selected := m.consoleSelected == i
		switch row.field {
		case consoleFieldTV:
			lines = append(lines, m.consoleSettingLine(selected, "Plays on", orNotSet(cfg.TVName)))
		case consoleFieldBoot:
			lines = append(lines, m.consoleSettingLine(selected, "Starts in", string(cfg.Boot)))
		case consoleFieldDesktopSession:
			// The .desktop suffix is noise here and pushes the value onto a
			// second line in a narrow pane.
			lines = append(lines, m.consoleSettingLine(selected, "Comes back to",
				orNotSet(strings.TrimSuffix(cfg.DesktopSession, ".desktop"))))
		case consoleFieldTrigger:
			lines = append(lines, m.consoleToggleLine(selected, "Start on controller", cfg.EnterOnControllerConnect))
		}
	}
	return lines
}

func (m Model) consoleSettingLine(selected bool, label string, value string) string {
	prefix := "  "
	if selected {
		prefix = m.styles.statusOK.Render("> ")
		value = m.styles.focused.Render(value)
	} else {
		value = m.styles.value.Render(value)
	}
	return fmt.Sprintf("%s%s %s", prefix, m.styles.label.Render(fmt.Sprintf("%-20s", label)), value)
}

func (m Model) consoleToggleLine(selected bool, label string, on bool) string {
	value := m.styles.subtle.Render("off")
	if on {
		value = m.styles.statusOK.Render("on")
	}
	return m.consoleSettingLine(selected, label, value)
}

func (m Model) renderConsoleStatusPane(width int, height int) string {
	style := m.paneStyle(paneToneStatic)
	innerW := max(1, width-style.GetHorizontalFrameSize())
	lines := []string{}

	if m.consoleDirty {
		lines = append(lines,
			m.styles.warning.Render("Unsaved changes."),
			m.styles.subtle.Render("s saves them, r puts them back."),
			"")
	}
	if m.consoleHosted {
		lines = append(lines, m.styles.statusOK.Render("This session can switch."))
	} else {
		lines = append(lines,
			m.styles.warning.Render("This session cannot switch."),
			"",
			"Console mode needs the login manager to start",
			"the hosting session. Run:",
			"",
			m.styles.value.Render("  hyprmoncfg console setup"),
			"",
			"then log out and back in.")
	}
	lines = append(lines, "", m.styles.subtle.Render(
		"Entering closes this desktop session. Come back"),
		m.styles.subtle.Render("from Big Picture: Steam -> Power -> Switch to"),
		m.styles.subtle.Render("Desktop."))
	lines = append(lines, "", m.styles.subtle.Render(
		"Resolution, HDR and the frame limiter are set"),
		m.styles.subtle.Render("per game inside Steam, not here."))

	body := fitBlock(strings.Join(lines, "\n"), innerW, max(1, height-style.GetVerticalFrameSize()))
	return m.renderTitledPane(paneToneStatic, "Status", body, width)
}

func connectedOutputNames(monitors []hypr.Monitor) []string {
	names := make([]string, 0, len(monitors))
	for _, m := range monitors {
		names = append(names, m.Name)
	}
	return names
}

func cycleValue(values []string, current string, dir int) string {
	if len(values) == 0 {
		return current
	}
	index := 0
	for i, v := range values {
		if v == current {
			index = i
			break
		}
	}
	index = (index + dir + len(values)) % len(values)
	return values[index]
}

func orNotSet(s string) string {
	if strings.TrimSpace(s) == "" {
		return "not set"
	}
	return s
}
