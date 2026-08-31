package apps

import (
	"context"

	"github.com/crmne/hyprmoncfg/internal/hypr"
	"sort"
	"strings"
)

// Source is where the candidate list reads open windows from.
type Source interface {
	Clients(ctx context.Context) ([]hypr.Window, error)
}

// CloseCandidate is one thing the user can pick for the close list.
//
// Token is the whole point. Matching is exact -- a window class or a
// /proc comm, never a title substring -- which makes the right value hard to
// guess by hand: the WhatsApp web app on the development host is
// "chrome-web.whatsapp.com__-Default". Picking from a list is how the user gets
// that value without typing it.
type CloseCandidate struct {
	// Token is exactly what CloseTrackedApps compares against.
	Token string
	// Label is what the user recognises: a window title, or an app name.
	Label string
	// Detail says where the candidate came from, so the picker can show the
	// token itself rather than hiding what will be stored.
	Detail string
	// Running marks a candidate that has a window open right now.
	Running bool
}

// CloseCandidates lists what the user can pick, open windows first.
//
// Open windows come first and matter most: their class is what Hyprland will
// actually report when the session tries to close them, so a candidate taken
// from a live window is guaranteed to match. Installed applications fill in the
// rest for things that are not running at the moment.
func CloseCandidates(ctx context.Context, source Source) []CloseCandidate {
	seen := make(map[string]int)
	candidates := make([]CloseCandidate, 0, 32)

	add := func(c CloseCandidate) {
		c.Token = strings.TrimSpace(c.Token)
		if c.Token == "" || len(c.Token) > maxTargetNameLength {
			return
		}
		if _, protected := ProtectedProcesses[c.Token]; protected {
			return
		}
		if !validProcessName(c.Token) {
			return
		}
		key := strings.ToLower(c.Token)
		if idx, dup := seen[key]; dup {
			// A running window is the better version of the same token: it
			// proves the class and carries a title worth showing.
			if c.Running && !candidates[idx].Running {
				candidates[idx] = c
			}
			return
		}
		seen[key] = len(candidates)
		candidates = append(candidates, c)
	}

	if source != nil {
		if windows, err := source.Clients(ctx); err == nil {
			for _, w := range windows {
				class := strings.TrimSpace(w.MatchClass())
				if class == "" {
					continue
				}
				add(CloseCandidate{
					Token:   class,
					Label:   firstNonBlank(w.Title, class),
					Detail:  class,
					Running: true,
				})
			}
		}
	}

	for _, app := range SuggestCloseableApps() {
		add(CloseCandidate{
			Token:  app.Exec,
			Label:  firstNonBlank(app.Name, app.Exec),
			Detail: app.Exec,
		})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Running != candidates[j].Running {
			return candidates[i].Running
		}
		return strings.ToLower(candidates[i].Label) < strings.ToLower(candidates[j].Label)
	})
	return candidates
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// MarkChosen flags the candidates already on the close list, so a picker can
// open with them selected rather than making the user rebuild the list.
func MarkChosen(candidates []CloseCandidate, chosen []string) map[string]bool {
	selected := make(map[string]bool, len(chosen))
	for _, name := range chosen {
		selected[strings.ToLower(strings.TrimSpace(name))] = true
	}
	return selected
}

// MissingTokens returns close-list entries that no candidate offers, so they
// are not silently dropped when a picker writes its selection back. An app that
// is merely closed right now still belongs on the list.
func MissingTokens(candidates []CloseCandidate, chosen []string) []string {
	known := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		known[strings.ToLower(c.Token)] = true
	}
	missing := make([]string, 0)
	for _, name := range chosen {
		trimmed := strings.TrimSpace(name)
		if trimmed != "" && !known[strings.ToLower(trimmed)] {
			missing = append(missing, trimmed)
		}
	}
	return missing
}
