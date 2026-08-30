package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/crmne/hyprmoncfg/internal/couch"
	"github.com/crmne/hyprmoncfg/internal/couch/hooks"
	"github.com/crmne/hyprmoncfg/internal/hypr"
	"github.com/crmne/hyprmoncfg/internal/ipc"
)

// couchField identifies a selectable row of the Couch Mode tab.
//
// The set is deliberately small. Everything else about the console layout --
// position, transform, scale, bit depth, luminance -- is derived by
// couch.BuildConsoleProfile, because those are the fields that turn a working
// TV into a black screen you cannot fix with a controller in your hand.
type couchField int

const (
	couchFieldTV couchField = iota
	couchFieldMode
	couchFieldHDR
	couchFieldVRR
	couchFieldDesk
	couchFieldWatchBigPicture
	couchFieldEnterOnController
	couchFieldExitOnControllersOff
	couchFieldGamescope
	couchFieldGamescopeFPS
	couchFieldHook
	couchFieldCloseApps
	couchFieldCloseWait
	couchFieldApp
)

// couchRow is one visible row: a field, plus which app it refers to when the
// field is couchFieldApp.
type couchRow struct {
	field couchField
	index int
	// hook names the session hook a couchFieldHook row toggles.
	hook hooks.Hook
}

const couchLogTailLines = 8

// couchRows lists the rows on screen, in order. HDR only appears when the
// chosen TV actually advertises it, so the tab never shows a dead toggle.
func (m Model) couchRows(cfg couch.Config) []couchRow {
	rows := []couchRow{{field: couchFieldTV}, {field: couchFieldMode}}
	if m.couchTVSupportsHDR(cfg.Layout) {
		rows = append(rows, couchRow{field: couchFieldHDR})
	}
	rows = append(rows,
		couchRow{field: couchFieldVRR},
		couchRow{field: couchFieldDesk},
		couchRow{field: couchFieldWatchBigPicture},
		couchRow{field: couchFieldEnterOnController},
		couchRow{field: couchFieldExitOnControllersOff},
	)
	if couch.GamescopeAvailable() {
		rows = append(rows, couchRow{field: couchFieldGamescope})
		if cfg.Gamescope.Enabled {
			rows = append(rows, couchRow{field: couchFieldGamescopeFPS})
		}
	}
	// Only hooks this machine can actually run are offered; the rest would be
	// switches that do nothing.
	for _, hook := range hooks.Available() {
		rows = append(rows, couchRow{field: couchFieldHook, hook: hook})
	}
	rows = append(rows,
		couchRow{field: couchFieldCloseApps},
		couchRow{field: couchFieldCloseWait},
	)
	if cfg.CloseAppsEnabled {
		for i := range cfg.AppsToClose {
			rows = append(rows, couchRow{field: couchFieldApp, index: i})
		}
	}
	return rows
}

// couchRowIndex locates a field on screen. Row positions shift with the
// config -- HDR only appears for a capable TV, app rows only when the toggle is
// on -- so nothing may assume the enum order is the screen order.
func (m Model) couchRowIndex(cfg couch.Config, field couchField, appIndex int) int {
	for i, row := range m.couchRows(cfg) {
		if row.field == field && (field != couchFieldApp || row.index == appIndex) {
			return i
		}
	}
	return -1
}

// couchHookRowIndex finds the row for one named session hook.
func (m Model) couchHookRowIndex(cfg couch.Config, name string) int {
	for i, row := range m.couchRows(cfg) {
		if row.field == couchFieldHook && row.hook.Name() == name {
			return i
		}
	}
	return -1
}

func (m Model) couchRowAt(cfg couch.Config, index int) couchRow {
	rows := m.couchRows(cfg)
	if index < 0 || index >= len(rows) {
		return couchRow{field: couchFieldTV}
	}
	return rows[index]
}

// couchTVSupportsHDR reads the EDID rather than trusting the configured colour
// preset, which only says what was asked for.
func (m Model) couchTVSupportsHDR(layout couch.ConsoleLayout) bool {
	if strings.TrimSpace(layout.TVName) == "" {
		return false
	}
	if m.couchHDRCapable == nil {
		return false
	}
	return m.couchHDRCapable[layout.TVName]
}

// couchTVMonitor resolves the configured TV against the live displays.
func (m Model) couchTVMonitor(layout couch.ConsoleLayout) (hypr.Monitor, bool) {
	for _, mon := range m.monitors {
		if mon.HardwareKey() == layout.TVKey {
			return mon, true
		}
	}
	return hypr.Monitor{}, false
}

func (m Model) couchEnabled() bool {
	return m.couchConfig != nil && m.couchConfig.Enabled
}

// couchBaseDir is the hyprmoncfg base directory: the parent of the profiles dir.
func (m Model) couchBaseDir() string {
	if m.store == nil {
		return ""
	}
	return filepath.Dir(m.store.Dir())
}

func (m Model) couchStateDir() string {
	stateDir, _ := couch.StateDir()
	return stateDir
}

func (m Model) enableCouchFromLayout() (tea.Model, tea.Cmd) {
	if m.couchEnabled() {
		m.tab = tabCouch
		return m, nil
	}
	if !m.twoMonitorsLive() {
		m.setStatusErr("Couch Mode needs at least two displays connected")
		return m, nil
	}

	cfg := m.ensureCouchConfig()
	cfg.Enabled = true
	if err := m.persistCouch(cfg); err != nil {
		m.setStatusErr(fmt.Sprintf("Could not enable Couch Mode: %v", err))
		return m, nil
	}
	m.tab = tabCouch
	m.couchSelected = int(couchFieldTV)
	m.setStatusOK("Couch Mode enabled. Pick the TV and desk profiles below.")
	return m, nil
}

func (m Model) disableCouch(cfg couch.Config) (tea.Model, tea.Cmd) {
	cfg.Enabled = false
	if err := m.persistCouch(cfg); err != nil {
		m.setStatusErr(fmt.Sprintf("Could not disable Couch Mode: %v", err))
		return m, nil
	}
	if _, running := couch.RunningSession(m.couchStateDir()); running {
		m.setStatusOK("Couch Mode disabled. The running session still restores the desk profile.")
	} else {
		m.setStatusOK("Couch Mode disabled")
	}
	m.tab = tabLayout
	return m, nil
}

func (m Model) ensureCouchConfig() couch.Config {
	if m.couchConfig != nil {
		return *m.couchConfig
	}
	return couch.DefaultConfig()
}

func (m *Model) persistCouch(cfg couch.Config) error {
	base := m.couchBaseDir()
	if base == "" {
		return fmt.Errorf("no configuration directory")
	}
	if err := couch.SaveConfig(base, cfg); err != nil {
		return err
	}
	m.couchConfig = &cfg
	return nil
}

func (m Model) updateCouchKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cfg := m.ensureCouchConfig()
	rows := m.couchRows(cfg)

	switch msg.String() {
	case "up", "k":
		m.couchSelected = clampIndex(m.couchSelected-1, len(rows))
	case "down", "j":
		m.couchSelected = clampIndex(m.couchSelected+1, len(rows))
	case "left", "h", "-", "_":
		return m.adjustCouchField(&cfg, -1)
	case "right", "l", "+", "=":
		return m.adjustCouchField(&cfg, 1)
	case " ", "enter":
		return m.activateCouchField(&cfg)
	case "p":
		return m.startCouchPlay(&cfg)
	case "v":
		return m.startCouchRestore()
	case "x":
		return m.disableCouch(cfg)
	case "e":
		if !cfg.CloseAppsEnabled {
			m.setStatusErr("Enable \"Close apps during play\" first")
			return m, nil
		}
		return m, m.openCouchAppPicker(&cfg)
	default:
		return m, nil
	}
	return m, nil
}

func (m Model) adjustCouchField(cfg *couch.Config, dir int) (tea.Model, tea.Cmd) {
	row := m.couchRowAt(*cfg, m.couchSelected)

	switch row.field {
	case couchFieldTV:
		keys := couchOutputKeys(m.monitors)
		if len(keys) < 2 {
			if len(keys) == 0 {
				m.setStatusErr("No displays detected")
			}
			return m, nil
		}
		cfg.Layout.TVKey = cycleValue(keys, cfg.Layout.TVKey, dir)
		if tv, ok := m.couchTVMonitor(cfg.Layout); ok {
			cfg.Layout.TVName = tv.Name
			// The old mode almost certainly does not exist on the new display,
			// so take its best one rather than leaving an invalid layout.
			if modes := couch.AvailableModes(tv); len(modes) > 0 {
				cfg.Layout.Mode = modes[0]
			}
			cfg.Layout.HDR = cfg.Layout.HDR && m.couchTVSupportsHDR(cfg.Layout)
		}
		return m.saveCouchLayout(*cfg)

	case couchFieldMode:
		tv, ok := m.couchTVMonitor(cfg.Layout)
		if !ok {
			m.setStatusErr("The selected TV is not connected")
			return m, nil
		}
		modes := couch.AvailableModes(tv)
		if len(modes) == 0 {
			m.setStatusErr(fmt.Sprintf("%s reports no usable mode", tv.Name))
			return m, nil
		}
		cfg.Layout.Mode = cycleValue(modes, cfg.Layout.Mode, dir)
		return m.saveCouchLayout(*cfg)

	case couchFieldHDR:
		cfg.Layout.HDR = !cfg.Layout.HDR
		return m.saveCouchLayout(*cfg)

	case couchFieldVRR:
		cfg.Layout.VRR = !cfg.Layout.VRR
		return m.saveCouchLayout(*cfg)

	case couchFieldDesk:
		modes := []couch.DeskDuringCouch{couch.DeskDisabled, couch.DeskEnabled, couch.DeskMirror}
		names := make([]string, len(modes))
		for i, mode := range modes {
			names[i] = string(mode)
		}
		cfg.Layout.Desk = couch.DeskDuringCouch(cycleValue(names, string(cfg.Layout.Desk), dir))
		return m.saveCouchLayout(*cfg)

	case couchFieldWatchBigPicture:
		cfg.WatchBigPicture = !cfg.WatchBigPicture
		return m.saveCouchField(*cfg)

	case couchFieldEnterOnController:
		cfg.EnterOnControllerConnect = !cfg.EnterOnControllerConnect
		return m.saveCouchField(*cfg)

	case couchFieldExitOnControllersOff:
		cfg.ExitOnControllersOff = !cfg.ExitOnControllersOff
		return m.saveCouchField(*cfg)

	case couchFieldGamescope:
		cfg.Gamescope.Enabled = !cfg.Gamescope.Enabled
		return m.saveCouchField(*cfg)

	case couchFieldGamescopeFPS:
		// 0 means uncapped; step in tens through the rates a TV can show.
		next := cfg.Gamescope.FPSLimit + dir*10
		if next < 0 {
			next = 0
		}
		if next > 240 {
			return m, nil
		}
		cfg.Gamescope.FPSLimit = next
		return m.saveCouchField(*cfg)

	case couchFieldHook:
		cfg.SetHookEnabled(row.hook.Name(), !cfg.HookEnabled(row.hook.Name()))
		return m.saveCouchField(*cfg)

	case couchFieldCloseApps:
		cfg.CloseAppsEnabled = !cfg.CloseAppsEnabled
		return m.saveCouchField(*cfg)

	case couchFieldCloseWait:
		next := cfg.CloseAppsWaitSeconds + dir
		if next < 1 || next > 30 {
			return m, nil
		}
		cfg.CloseAppsWaitSeconds = next
		return m.saveCouchField(*cfg)
	}
	// App rows have no left/right action; removal lives on Enter.
	return m, nil
}

func (m Model) activateCouchField(cfg *couch.Config) (tea.Model, tea.Cmd) {
	row := m.couchRowAt(*cfg, m.couchSelected)
	if row.field != couchFieldApp {
		return m.adjustCouchField(cfg, 1)
	}
	if row.index >= len(cfg.AppsToClose) {
		return m, nil
	}
	removed := cfg.AppsToClose[row.index]
	cfg.AppsToClose = append(cfg.AppsToClose[:row.index], cfg.AppsToClose[row.index+1:]...)
	if err := m.persistCouch(*cfg); err != nil {
		m.setStatusErr(fmt.Sprintf("Could not update app list: %v", err))
		return m, nil
	}
	m.setStatusOK(fmt.Sprintf("Removed %q from the close list", removed))
	return m, nil
}

// saveCouchLayout validates the edited layout before writing it, so a choice
// that would break a session never reaches the config file. The apply engine's
// timed revert is the second net, for what validation cannot see.
func (m Model) saveCouchLayout(cfg couch.Config) (tea.Model, tea.Cmd) {
	if err := couch.ValidateConsoleLayout(cfg.Layout, m.monitors); err != nil {
		m.setStatusErr(err.Error())
		return m, nil
	}
	return m.saveCouchField(cfg)
}

// cycleValue steps through a list with wrap-around; an unknown current value
// starts from the first entry when moving forward.
func cycleValue(values []string, current string, dir int) string {
	if len(values) == 0 {
		return current
	}
	idx := indexOf(values, current)
	idx += dir
	idx %= len(values)
	if idx < 0 {
		idx += len(values)
	}
	return values[idx]
}

func couchOutputKeys(monitors []hypr.Monitor) []string {
	keys := make([]string, 0, len(monitors))
	for _, mon := range monitors {
		keys = append(keys, mon.HardwareKey())
	}
	return keys
}

func (m Model) saveCouchField(cfg couch.Config) (tea.Model, tea.Cmd) {
	if err := m.persistCouch(cfg); err != nil {
		m.setStatusErr(fmt.Sprintf("Could not save Couch settings: %v", err))
		return m, nil
	}
	return m, nil
}

// startCouchPlay launches a detached `hyprmoncfg couch play` child so closing
// the TUI never interrupts an active session.
func (m Model) startCouchPlay(cfg *couch.Config) (tea.Model, tea.Cmd) {
	if _, running := couch.RunningSession(m.couchStateDir()); running {
		m.setStatusErr("A Couch session is already running")
		return m, nil
	}
	cfg.Enabled = true
	if !cfg.Configured() {
		m.setStatusErr("Pick the TV display first")
		return m, nil
	}
	if err := couch.ValidateConsoleLayout(cfg.Layout, m.monitors); err != nil {
		m.setStatusErr(err.Error())
		return m, nil
	}
	if err := m.persistCouch(*cfg); err != nil {
		m.setStatusErr(fmt.Sprintf("Could not save Couch settings: %v", err))
		return m, nil
	}

	if handled, err := couchStartViaDaemon("the TUI"); handled {
		if err != nil {
			m.setStatusErr(fmt.Sprintf("Could not start console mode: %v", err))
			return m, nil
		}
		m.setStatusOK("Console mode starting — the daemon owns the session")
		return m, m.refreshCmd(false)
	}

	// No daemon: fall back to a detached child, which at least survives the
	// TUI closing. It cannot be reconciled after a SIGKILL, so the daemon is
	// the better path and this is only the fallback.
	cmd, err := m.couchChildCommand("play")
	if err != nil {
		m.setStatusErr(fmt.Sprintf("Could not start console mode: %v", err))
		return m, nil
	}
	if err := cmd.Start(); err != nil {
		m.setStatusErr(fmt.Sprintf("Could not start console mode: %v", err))
		return m, nil
	}
	m.setStatusOK("Console mode starting — no daemon, running detached")
	return m, m.refreshCmd(false)
}

func (m Model) startCouchRestore() (tea.Model, tea.Cmd) {
	if handled, err := couchStopViaDaemon(); handled {
		if err == nil {
			m.setStatusOK("Leaving console mode; restoring the desktop layout...")
			return m, m.refreshCmd(false)
		}
		// No session to stop is not a failure here: the user is asking for the
		// desktop back, and the fallback below does exactly that.
	}
	cmd, err := m.couchChildCommand("restore")
	if err != nil {
		m.setStatusErr(fmt.Sprintf("Could not restore desk profile: %v", err))
		return m, nil
	}
	if err := cmd.Start(); err != nil {
		m.setStatusErr(fmt.Sprintf("Could not restore desk profile: %v", err))
		return m, nil
	}
	m.setStatusOK("Restoring desk profile...")
	return m, m.refreshCmd(false)
}

// couchChildCommand spawns `hyprmoncfg couch <action>` detached from this
// process group, forwarding the monitor config paths the TUI was started with.
func (m Model) couchChildCommand(action string) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	base := m.couchBaseDir()
	if base == "" {
		return nil, fmt.Errorf("no configuration directory")
	}
	args := []string{"couch", action, "--config-dir", base}
	if path := strings.TrimSpace(m.engine.MonitorsConfPath); path != "" {
		args = append(args, "--monitors-conf", path)
	}
	if path := strings.TrimSpace(m.engine.HyprlandConfigPath); path != "" {
		args = append(args, "--hypr-config", path)
	}
	child := exec.Command(exe, args...)
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return child, nil
}

func (m Model) renderCouchView(height int) string {
	settings := m.couchSettingsLines()
	leftStyle := m.paneStyle(paneToneFocused)

	if m.terminalWidth() < 96 {
		width := m.terminalWidth() - m.styles.app.GetHorizontalFrameSize()
		settingsHeight := clampInt(len(settings)+2, 6, (height*2)/3)
		innerW := max(1, width-leftStyle.GetHorizontalFrameSize())
		leftBody := fitBlock(strings.Join(settings, "\n"), innerW, max(1, settingsHeight-leftStyle.GetVerticalFrameSize()))
		left := m.renderTitledPane(paneToneFocused, "Couch Mode Setup", leftBody, width)
		right := m.renderCouchStatusPanes(width, max(3, height-settingsHeight))
		return lipgloss.JoinVertical(lipgloss.Left, left, right)
	}

	leftWidth, rightWidth := m.sidePaneWidths(35)
	leftBody := fitBlock(strings.Join(settings, "\n"), max(1, leftWidth-leftStyle.GetHorizontalFrameSize()), max(1, height-leftStyle.GetVerticalFrameSize()))
	left := m.renderTitledPane(paneToneFocused, "Couch Mode Setup", leftBody, leftWidth)
	right := m.renderCouchStatusPanes(rightWidth, height)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", paneGapWidth), right)
}

func (m Model) couchSettingsLines() []string {
	cfg := m.ensureCouchConfig()
	rows := m.couchRows(cfg)
	lines := make([]string, 0, len(rows)+6)

	for i, row := range rows {
		selected := m.couchSelected == i
		switch row.field {
		case couchFieldTV:
			lines = append(lines, m.couchSettingLine(selected, "TV display", m.couchTVLabel(cfg.Layout)))
		case couchFieldMode:
			lines = append(lines, m.couchSettingLine(selected, "TV mode", blankFallback(cfg.Layout.Mode, "(none)")))
		case couchFieldHDR:
			lines = append(lines, m.couchToggleLine(selected, "HDR", cfg.Layout.HDR))
		case couchFieldVRR:
			lines = append(lines, m.couchToggleLine(selected, "Variable refresh rate", cfg.Layout.VRR))
		case couchFieldDesk:
			lines = append(lines, m.couchSettingLine(selected, "Other displays", couchDeskLabel(cfg.Layout.Desk)))
		case couchFieldWatchBigPicture:
			lines = append(lines, m.couchToggleLine(selected, "Enter on Big Picture", cfg.WatchBigPicture))
		case couchFieldEnterOnController:
			lines = append(lines, m.couchToggleLine(selected, "Enter on controller", cfg.EnterOnControllerConnect))
		case couchFieldExitOnControllersOff:
			lines = append(lines, m.couchToggleLine(selected, "End on controllers off", cfg.ExitOnControllersOff))
		case couchFieldGamescope:
			lines = append(lines, m.couchSettingLine(selected, "gamescope",
				couch.GamescopeSummary(cfg.Layout, cfg.Gamescope)))
		case couchFieldGamescopeFPS:
			lines = append(lines, m.couchSettingLine(selected, "  frame rate cap", fpsLabel(cfg.Gamescope.FPSLimit)))
		case couchFieldHook:
			lines = append(lines, m.couchToggleLine(selected, row.hook.Description(), cfg.HookEnabled(row.hook.Name())))
		case couchFieldCloseApps:
			lines = append(lines, m.couchToggleLine(selected, "Close apps during play", cfg.CloseAppsEnabled))
			// The app list belongs under its own toggle, and only when on.
			if cfg.CloseAppsEnabled {
				continue
			}
		case couchFieldCloseWait:
			lines = append(lines, m.couchSettingLine(selected, "Close wait",
				fmt.Sprintf("%ds after Big Picture opens", cfg.CloseAppsWaitSeconds)))
			if cfg.CloseAppsEnabled {
				lines = append(lines, "", m.styles.label.Render("Apps to close")+"  "+m.styles.subtle.Render("e chooses from open windows · Enter removes"))
				if len(cfg.AppsToClose) == 0 {
					lines = append(lines, m.styles.subtle.Render("  (none)"))
				}
			}
		case couchFieldApp:
			lines = append(lines, m.couchAppLine(selected, row.index, cfg.AppsToClose[row.index]))
		}
	}

	if !m.couchManaged {
		lines = append(lines, "",
			m.styles.warning.Render("hyprmoncfg is not managing monitor config."),
			m.styles.subtle.Render("Run `hyprmoncfg manage` before starting a session."))
	}

	lines = append(lines, "")
	lines = append(lines, m.styles.subtle.Render("p play · v back to desk · x disable"))
	return lines
}

// couchTVLabel names the TV by connector and model, since a bare hardware key
// means nothing on screen.
func (m Model) couchTVLabel(layout couch.ConsoleLayout) string {
	if tv, ok := m.couchTVMonitor(layout); ok {
		if desc := strings.TrimSpace(tv.Description); desc != "" {
			return tv.Name + "  " + desc
		}
		return tv.Name
	}
	if layout.TVName != "" {
		return layout.TVName + "  (not connected)"
	}
	return "(none)"
}

func fpsLabel(limit int) string {
	if limit <= 0 {
		return "uncapped"
	}
	return fmt.Sprintf("%d fps", limit)
}

func couchDeskLabel(desk couch.DeskDuringCouch) string {
	switch desk {
	case couch.DeskEnabled:
		return "stay on, beside the TV"
	case couch.DeskMirror:
		return "mirror the TV"
	default:
		return "turn off"
	}
}

func (m Model) couchAppLine(selected bool, index int, app string) string {
	prefix := "  "
	label := app
	if selected {
		prefix = m.styles.statusOK.Render("> ")
		label = m.styles.focused.Render(label)
	} else {
		label = m.styles.value.Render(label)
	}
	return fmt.Sprintf("%s%s %s", prefix, m.styles.subtle.Render(fmt.Sprintf("%d.", index+1)), label)
}

func (m Model) couchSettingLine(selected bool, label string, value string) string {
	prefix := "  "
	if selected {
		prefix = m.styles.statusOK.Render("> ")
		value = m.styles.focused.Render(value)
	} else {
		value = m.styles.value.Render(value)
	}
	return fmt.Sprintf("%s%s %s", prefix, m.styles.label.Render(fmt.Sprintf("%-24s", label)), value)
}

func (m Model) couchToggleLine(selected bool, label string, on bool) string {
	value := "off"
	if on {
		value = m.styles.statusOK.Render("on")
	} else {
		value = m.styles.subtle.Render("off")
	}
	return m.couchSettingLine(selected, label, value)
}

func (m Model) renderCouchStatusPanes(width int, height int) string {
	style := m.paneStyle(paneToneStatic)
	innerW := max(1, width-style.GetHorizontalFrameSize())
	vFrame := style.GetVerticalFrameSize()

	sessionLines := m.couchSessionLines()
	logLines := m.couchLogLines(innerW)

	infoHeight := clampInt(len(sessionLines)+vFrame, 4, max(4, height/2))
	logHeight := height - infoHeight
	if logHeight < 5 {
		info := fitBlock(strings.Join(sessionLines, "\n"), innerW, max(1, infoHeight-vFrame))
		return m.renderTitledPane(paneToneStatic, "Session Status", info, width)
	}
	info := fitBlock(strings.Join(sessionLines, "\n"), innerW, max(1, infoHeight-vFrame))
	logBody := fitBlock(strings.Join(logLines, "\n"), innerW, max(1, logHeight-vFrame))
	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderTitledPane(paneToneStatic, "Session Status", info, width),
		m.renderTitledPane(paneToneStatic, "Recent Log", logBody, width),
	)
}

func (m Model) couchSessionLines() []string {
	lines := make([]string, 0, 6)
	state := m.styles.subtle.Render("disabled")
	if m.couchEnabled() {
		state = m.styles.statusOK.Render("enabled")
	}
	lines = append(lines, m.styles.label.Render("Mode")+"    "+state)

	if m.couchSession != nil && couch.ProcessAlive(m.couchSession.PID) {
		started := ""
		if !m.couchSession.StartedAt.IsZero() {
			started = m.couchSession.StartedAt.Format("15:04:05")
		}
		duration := ""
		if !m.couchSession.StartedAt.IsZero() {
			d := time.Since(m.couchSession.StartedAt)
			duration = fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
		}
		lines = append(lines,
			m.styles.label.Render("Session")+" "+m.styles.statusOK.Render(fmt.Sprintf("running (PID %d, phase %s)", m.couchSession.PID, m.couchSession.Phase)),
			m.styles.label.Render("Started")+" "+m.styles.value.Render(blankFallback(started, "-")),
		)
		if duration != "" {
			lines = append(lines, m.styles.label.Render("Duration")+" "+m.styles.value.Render(duration))
		}
	} else if m.couchStale && m.couchSession != nil {
		lines = append(lines,
			m.styles.label.Render("Session")+" "+m.styles.statusOK.Render(fmt.Sprintf("stale (PID %d died)", m.couchSession.PID)),
			m.styles.label.Render("Action")+" "+m.styles.subtle.Render("run `hyprmoncfg couch restore` to recover"),
		)
	} else {
		lines = append(lines, m.styles.label.Render("Session")+" "+m.styles.subtle.Render("inactive"))
	}
	return lines
}

func (m Model) couchLogLines(maxWidth ...int) []string {
	tail := couch.LogTail(m.couchStateDir(), couchLogTailLines)
	if len(tail) == 0 {
		return []string{m.styles.subtle.Render("(no log yet)")}
	}
	limit := max(20, m.footerContentWidth()/2)
	if len(maxWidth) > 0 && maxWidth[0] > 0 {
		limit = maxWidth[0]
	}
	out := make([]string, 0, len(tail))
	for _, line := range tail {
		out = append(out, fitString(line, limit))
	}
	return out
}

// couchStartViaDaemon and couchStopViaDaemon drive the session where it lives.
// The daemon holds the write lock and the event feed, so a session there cannot
// race automatic switching the way a detached child did.
func couchStartViaDaemon(trigger string) (bool, error) {
	client, ok := dialCouchDaemon()
	if !ok {
		return false, nil
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	return true, client.CouchStart(ctx, trigger)
}

func couchStopViaDaemon() (bool, error) {
	client, ok := dialCouchDaemon()
	if !ok {
		return false, nil
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return true, client.CouchStop(ctx)
}

func dialCouchDaemon() (*ipc.Client, bool) {
	path, err := ipc.SocketPath()
	if err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, err := ipc.Dial(ctx, path)
	if err != nil {
		return nil, false
	}
	return client, true
}
