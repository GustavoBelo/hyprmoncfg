package couch

import (
	"context"
	"strings"
	"testing"

	"github.com/crmne/hyprmoncfg/internal/hypr"
)

// Open windows come first: their class is what Hyprland will report when the
// session tries to close them, so a candidate taken from a live window is
// guaranteed to match. Everything else is a guess by comparison.
func TestCloseCandidatesPutRunningWindowsFirst(t *testing.T) {
	source := &fakeSource{windows: []hypr.Window{
		{Address: "0x1", Class: "chrome-web.whatsapp.com__-Default", Title: "web.whatsapp.com"},
		{Address: "0x2", Class: "code", Title: "PLAN.md - Visual Studio Code"},
	}}

	candidates := CloseCandidates(context.Background(), source)
	if len(candidates) < 2 {
		t.Fatalf("expected the open windows, got %d candidates", len(candidates))
	}
	running := 0
	for _, c := range candidates {
		if !c.Running {
			break
		}
		running++
	}
	if running < 2 {
		t.Fatalf("running windows must sort first, got %d before the rest", running)
	}
}

// The token is the exact value the matcher compares, and the whole reason a
// picker exists: nobody would type this one correctly.
func TestCloseCandidatesCarryTheExactToken(t *testing.T) {
	source := &fakeSource{windows: []hypr.Window{
		{Address: "0x1", Class: "chrome-web.whatsapp.com__-Default", Title: "web.whatsapp.com"},
	}}

	var found *CloseCandidate
	for _, c := range CloseCandidates(context.Background(), source) {
		if c.Token == "chrome-web.whatsapp.com__-Default" {
			candidate := c
			found = &candidate
		}
	}
	if found == nil {
		t.Fatal("the window class must be offered verbatim")
	}
	// The label is what the user recognises; the detail shows what gets stored.
	if found.Label != "web.whatsapp.com" {
		t.Fatalf("label = %q, want the window title", found.Label)
	}
	if found.Detail != found.Token {
		t.Fatalf("the detail should show the token, got %q", found.Detail)
	}

	// And it has to be a target the matcher actually accepts.
	targets := map[string]struct{}{strings.ToLower(found.Token): {}}
	if !windowMatchesTarget(source.windows[0], targets) {
		t.Fatal("a picked candidate must match the window it came from")
	}
}

// Offering a protected process would let the user pick something the session
// then refuses to close.
func TestCloseCandidatesSkipProtectedProcesses(t *testing.T) {
	source := &fakeSource{windows: []hypr.Window{
		{Address: "0x1", Class: "steam", Title: "Steam"},
		{Address: "0x2", Class: "Hyprland", Title: "compositor"},
		{Address: "0x3", Class: "kitty", Title: "shell"},
	}}
	for _, c := range CloseCandidates(context.Background(), source) {
		if _, protected := ProtectedProcesses[c.Token]; protected {
			t.Fatalf("protected process %q was offered", c.Token)
		}
	}
}

// Several windows of one app are one candidate, and the running version wins
// over the installed-app version of the same token.
func TestCloseCandidatesDeduplicate(t *testing.T) {
	source := &fakeSource{windows: []hypr.Window{
		{Address: "0x1", Class: "chromium", Title: "one tab"},
		{Address: "0x2", Class: "chromium", Title: "another tab"},
		{Address: "0x3", Class: "Chromium", Title: "different case"},
	}}
	count := 0
	for _, c := range CloseCandidates(context.Background(), source) {
		if strings.EqualFold(c.Token, "chromium") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one chromium candidate, got %d", count)
	}
}

// A close-list entry whose app is not running must survive a trip through the
// picker; being closed right now is not a reason to drop it.
func TestMissingTokensKeepsEntriesThePickerCannotShow(t *testing.T) {
	candidates := []CloseCandidate{{Token: "chromium"}, {Token: "code"}}

	missing := MissingTokens(candidates, []string{"chromium", "retroarch", "  ", "CODE"})
	if len(missing) != 1 || missing[0] != "retroarch" {
		t.Fatalf("expected only retroarch to be missing, got %v", missing)
	}
}

func TestMarkChosenIsCaseInsensitive(t *testing.T) {
	selected := MarkChosen(nil, []string{"Chromium", " RetroArch "})
	if !selected["chromium"] || !selected["retroarch"] {
		t.Fatalf("chosen set = %v", selected)
	}
}

// A picker with no compositor to ask still offers the installed applications.
func TestCloseCandidatesWithoutAWindowSource(t *testing.T) {
	if candidates := CloseCandidates(context.Background(), nil); candidates == nil {
		t.Fatal("a nil source must not panic or yield nil")
	}
}
