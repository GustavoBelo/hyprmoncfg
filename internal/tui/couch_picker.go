package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/crmne/hyprmoncfg/internal/couch"
)

// couchAppPickerState is the multi-select list of things a session may close.
//
// It replaces typing names into a text field. Matching is exact -- a window
// class or a /proc comm -- so the right value is often unguessable: the
// WhatsApp web app reports "chrome-web.whatsapp.com__-Default". Picking from
// what is actually open is how the user gets that value without knowing it.
type couchAppPickerState struct {
	List     list.Model
	Chosen   map[string]bool
	Extra    []string
	Rendered []couch.CloseCandidate
}

type couchAppItem struct {
	candidate couch.CloseCandidate
	chosen    bool
}

func (i couchAppItem) FilterValue() string { return i.candidate.Label + " " + i.candidate.Token }

func (i couchAppItem) Title() string {
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

func (i couchAppItem) Description() string { return "" }

func (m *Model) openCouchAppPicker(cfg *couch.Config) tea.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	candidates := couch.CloseCandidates(ctx, m.engine.Client)
	chosen := couch.MarkChosen(candidates, cfg.AppsToClose)
	// Entries the picker cannot show -- an app that is simply not running and
	// has no desktop entry -- must survive the round trip.
	extra := couch.MissingTokens(candidates, cfg.AppsToClose)

	state := &couchAppPickerState{Chosen: chosen, Extra: extra, Rendered: candidates}
	state.List = m.newCouchAppList(state)
	m.couchPicker = state
	m.mode = modeCouchAppPicker
	return nil
}

func (m Model) newCouchAppList(state *couchAppPickerState) list.Model {
	items := make([]list.Item, 0, len(state.Rendered))
	for _, candidate := range state.Rendered {
		items = append(items, couchAppItem{
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

func (m Model) updateCouchAppPickerKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.couchPicker == nil {
		m.mode = modeMain
		return m, nil
	}
	// While filtering, keys belong to the filter box.
	if m.couchPicker.List.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.couchPicker.List, cmd = m.couchPicker.List.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "esc":
		m.mode = modeMain
		m.couchPicker = nil
		return m, nil

	case " ", "x":
		item, ok := m.couchPicker.List.SelectedItem().(couchAppItem)
		if !ok {
			return m, nil
		}
		key := strings.ToLower(item.candidate.Token)
		m.couchPicker.Chosen[key] = !m.couchPicker.Chosen[key]
		index := m.couchPicker.List.Index()
		m.couchPicker.List = m.newCouchAppList(m.couchPicker)
		m.couchPicker.List.Select(index)
		return m, nil

	case "enter":
		cfg := m.ensureCouchConfig()
		cfg.AppsToClose = couch.SanitizeApps(m.couchPickerSelection())
		if err := m.persistCouch(cfg); err != nil {
			m.setStatusErr(fmt.Sprintf("Could not save the close list: %v", err))
		} else {
			m.setStatusOK(fmt.Sprintf("Close list updated (%d apps)", len(cfg.AppsToClose)))
		}
		m.mode = modeMain
		m.couchPicker = nil
		return m, nil

	default:
		var cmd tea.Cmd
		m.couchPicker.List, cmd = m.couchPicker.List.Update(msg)
		return m, cmd
	}
}

// couchPickerSelection reads the ticks back out, in the order they are shown,
// and keeps the entries the picker could not offer.
func (m Model) couchPickerSelection() []string {
	if m.couchPicker == nil {
		return nil
	}
	chosen := make([]string, 0, len(m.couchPicker.Chosen))
	for _, candidate := range m.couchPicker.Rendered {
		if m.couchPicker.Chosen[strings.ToLower(candidate.Token)] {
			chosen = append(chosen, candidate.Token)
		}
	}
	return append(chosen, m.couchPicker.Extra...)
}

func (m Model) renderCouchAppPicker() string {
	if m.couchPicker == nil {
		return ""
	}
	selected := len(m.couchPickerSelection())
	body := []string{
		m.styles.subtle.Render("Windows that are open right now come first; their name is what a session will match on."),
		"",
		m.couchPicker.List.View(),
		"",
		m.styles.help.Render(fmt.Sprintf("Space picks · / filters · Enter saves (%d selected) · Esc discards", selected)),
	}
	return m.renderModalFrame("Choose Apps to Close", body)
}
