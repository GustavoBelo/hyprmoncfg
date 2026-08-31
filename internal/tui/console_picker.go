package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/crmne/hyprmoncfg/internal/apps"
	"github.com/crmne/hyprmoncfg/internal/console"
)

// consoleAppPickerState is the multi-select list of things a session may close.
//
// It replaces typing names into a text field. Matching is exact -- a window
// class or a /proc comm -- so the right value is often unguessable: the
// WhatsApp web app reports "chrome-web.whatsapp.com__-Default". Picking from
// what is actually open is how the user gets that value without knowing it.
type consoleAppPickerState struct {
	List     list.Model
	Chosen   map[string]bool
	Extra    []string
	Rendered []apps.CloseCandidate
}

type consoleAppItem struct {
	candidate apps.CloseCandidate
	chosen    bool
}

func (i consoleAppItem) FilterValue() string { return i.candidate.Label + " " + i.candidate.Token }

func (i consoleAppItem) Title() string {
	mark := "[ ]"
	if i.chosen {
		mark = "[x]"
	}
	label := i.candidate.Label
	if i.candidate.Running {
		label += "  ·  open now"
	}
	// The token is shown, never hidden: it is what actually gets stored, and
	// seeing it is how the user can tell two similar windows apart.
	if !strings.EqualFold(i.candidate.Token, i.candidate.Label) {
		label += "  (" + i.candidate.Token + ")"
	}
	return mark + " " + label
}

func (i consoleAppItem) Description() string { return "" }

func (m *Model) openConsoleAppPicker(cfg *console.Config) tea.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	candidates := apps.CloseCandidates(ctx, m.engine.Client)
	chosen := apps.MarkChosen(candidates, cfg.AppsToClose)
	// Entries the picker cannot show -- an app that is simply not running and
	// has no desktop entry -- must survive the round trip.
	extra := apps.MissingTokens(candidates, cfg.AppsToClose)

	state := &consoleAppPickerState{Chosen: chosen, Extra: extra, Rendered: candidates}
	state.List = m.newConsoleAppList(state)
	m.consolePicker = state
	m.mode = modeConsoleAppPicker
	return nil
}

func (m Model) newConsoleAppList(state *consoleAppPickerState) list.Model {
	items := make([]list.Item, 0, len(state.Rendered))
	for _, candidate := range state.Rendered {
		items = append(items, consoleAppItem{
			candidate: candidate,
			chosen:    state.Chosen[strings.ToLower(candidate.Token)],
		})
	}

	inner := list.NewDefaultDelegate()
	inner.ShowDescription = false
	inner.SetHeight(1)
	inner.SetSpacing(0)
	inner.Styles.NormalTitle = m.styles.value
	inner.Styles.SelectedTitle = m.styles.focused.Copy().UnsetPadding()
	inner.Styles.DimmedTitle = m.styles.subtle
	inner.Styles.FilterMatch = m.styles.badgeAccent

	height := clampInt(len(items)+2, 6, 16)
	picker := list.New(items, arrowDelegate{inner}, m.modePickerWidth()-2, height)
	picker.Title = "Apps to close during a session"
	picker.SetShowHelp(false)
	picker.SetShowPagination(false)
	picker.SetShowStatusBar(false)
	// Filtering earns its place here: the list is every open window plus every
	// installed application.
	picker.SetFilteringEnabled(true)
	picker.DisableQuitKeybindings()
	picker.Styles.Title = m.styles.modalTitle
	picker.Styles.TitleBar = lipgloss.NewStyle().PaddingBottom(1)
	picker.Styles.PaginationStyle = m.styles.subtle
	picker.Styles.HelpStyle = m.styles.help
	picker.Styles.NoItems = m.styles.subtle
	return picker
}

func (m Model) updateConsoleAppPickerKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.consolePicker == nil {
		m.mode = modeMain
		return m, nil
	}
	// While filtering, keys belong to the filter box.
	if m.consolePicker.List.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.consolePicker.List, cmd = m.consolePicker.List.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "esc":
		m.mode = modeMain
		m.consolePicker = nil
		return m, nil

	case " ", "x":
		item, ok := m.consolePicker.List.SelectedItem().(consoleAppItem)
		if !ok {
			return m, nil
		}
		key := strings.ToLower(item.candidate.Token)
		m.consolePicker.Chosen[key] = !m.consolePicker.Chosen[key]
		index := m.consolePicker.List.Index()
		m.consolePicker.List = m.newConsoleAppList(m.consolePicker)
		m.consolePicker.List.Select(index)
		return m, nil

	case "enter":
		cfg := m.ensureConsoleConfig()
		cfg.AppsToClose = apps.SanitizeApps(m.consolePickerSelection())
		if err := m.persistConsole(cfg); err != nil {
			m.setStatusErr(fmt.Sprintf("Could not save the close list: %v", err))
		} else {
			m.setStatusOK(fmt.Sprintf("Close list updated (%d apps)", len(cfg.AppsToClose)))
		}
		m.mode = modeMain
		m.consolePicker = nil
		return m, nil

	default:
		var cmd tea.Cmd
		m.consolePicker.List, cmd = m.consolePicker.List.Update(msg)
		return m, cmd
	}
}

// consolePickerSelection reads the ticks back out, in the order they are shown,
// and keeps the entries the picker could not offer.
func (m Model) consolePickerSelection() []string {
	if m.consolePicker == nil {
		return nil
	}
	chosen := make([]string, 0, len(m.consolePicker.Chosen))
	for _, candidate := range m.consolePicker.Rendered {
		if m.consolePicker.Chosen[strings.ToLower(candidate.Token)] {
			chosen = append(chosen, candidate.Token)
		}
	}
	return append(chosen, m.consolePicker.Extra...)
}

func (m Model) renderConsoleAppPicker() string {
	if m.consolePicker == nil {
		return ""
	}
	selected := len(m.consolePickerSelection())
	body := []string{
		m.styles.subtle.Render("Windows that are open right now come first; their name is what a session will match on."),
		"",
		m.consolePicker.List.View(),
		"",
		m.styles.help.Render(fmt.Sprintf("Space picks · / filters · Enter saves (%d selected) · Esc discards", selected)),
	}
	return m.renderModalFrame("Choose Apps to Close", body)
}
