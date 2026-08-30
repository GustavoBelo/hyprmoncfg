package couch

import (
	"strings"
	"testing"

	"github.com/crmne/hyprmoncfg/internal/hypr"
	"github.com/crmne/hyprmoncfg/internal/profile"
)

// The displays on the development host: a 300Hz desk monitor on DP-1 and a
// disabled 4K HDR TV on HDMI-A-1 that still reports its full mode list.
func hostMonitors() []hypr.Monitor {
	return []hypr.Monitor{
		{
			ID: 1, Name: "HDMI-A-1",
			Description: "Samsung Electric Company SAMSUNG 0x01000E00",
			Make:        "Samsung Electric Company", Model: "SAMSUNG", Serial: "0x01000E00",
			Disabled: true, Width: 0, Height: 0, RefreshRate: 60, Scale: 1,
			AvailableModes: []string{
				"4096x2160@120.00Hz", "3840x2160@120.00Hz", "2560x1440@120.00Hz", "1920x1080@120.00Hz",
			},
		},
		{
			ID: 2, Name: "DP-1",
			Description: "Technical Concepts Ltd 25G64",
			Make:        "Technical Concepts Ltd", Model: "25G64",
			Width: 1920, Height: 1080, RefreshRate: 300, Scale: 1,
			AvailableModes: []string{"1920x1080@300.00Hz", "1920x1080@120.00Hz", "1680x1050@60.00Hz"},
		},
	}
}

func tvKey(t *testing.T) string {
	t.Helper()
	for _, m := range hostMonitors() {
		if m.Name == "HDMI-A-1" {
			return m.HardwareKey()
		}
	}
	t.Fatal("no TV in the fixture")
	return ""
}

func TestSuggestPicksTheHDMIDisplayAndItsBestMode(t *testing.T) {
	layout, err := SuggestConsoleLayout(hostMonitors(), DisplayFacts{
		HDRCapable:          map[string]bool{"HDMI-A-1": true},
		PreferredResolution: map[string]string{"HDMI-A-1": "3840x2160"},
	})
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if layout.TVName != "HDMI-A-1" {
		t.Fatalf("the TV should be the HDMI output, got %q", layout.TVName)
	}
	// Native 16:9 at high refresh, not the larger 17:9 cinema mode above it.
	if layout.Mode != "3840x2160@120.00Hz" {
		t.Fatalf("expected native 4K at 120Hz, got %q", layout.Mode)
	}
	if !layout.HDR {
		t.Fatal("an HDR-capable TV should start with HDR on")
	}
	if layout.Desk != DeskDisabled {
		t.Fatalf("the desk should start disabled, got %q", layout.Desk)
	}
}

// A disabled display reports 0x0 as its current mode. Offering that would write
// a layout with no picture on it.
func TestAvailableModesNeverOfferTheDisabledPlaceholder(t *testing.T) {
	for _, mode := range AvailableModes(hostMonitors()[0]) {
		if strings.HasPrefix(mode, "0x0@") {
			t.Fatalf("0x0 offered as a mode in %v", AvailableModes(hostMonitors()[0]))
		}
	}
}

func TestValidateRejectsWhatWouldBreakASession(t *testing.T) {
	monitors := hostMonitors()
	base := ConsoleLayout{TVKey: tvKey(t), TVName: "HDMI-A-1", Mode: "2560x1440@120.00Hz", Desk: DeskDisabled}

	if err := ValidateConsoleLayout(base, monitors); err != nil {
		t.Fatalf("the baseline layout must be valid: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(ConsoleLayout) ConsoleLayout
	}{
		{"no TV chosen", func(l ConsoleLayout) ConsoleLayout { l.TVKey = ""; return l }},
		{"TV not connected", func(l ConsoleLayout) ConsoleLayout { l.TVKey = "ghost|display"; return l }},
		{"invented mode", func(l ConsoleLayout) ConsoleLayout { l.Mode = "7680x4320@240.00Hz"; return l }},
		{"mode from the other display", func(l ConsoleLayout) ConsoleLayout { l.Mode = "1920x1080@300.00Hz"; return l }},
		{"empty mode", func(l ConsoleLayout) ConsoleLayout { l.Mode = ""; return l }},
		{"unknown desk behaviour", func(l ConsoleLayout) ConsoleLayout { l.Desk = "vanish"; return l }},
	}
	for _, tc := range cases {
		if err := ValidateConsoleLayout(tc.mutate(base), monitors); err == nil {
			t.Fatalf("%s should have been rejected", tc.name)
		}
	}
}

// Everything a user cannot edit is pinned by construction, so no sequence of
// edits can produce a console profile that blanks the TV.
func TestConsoleProfileInvariants(t *testing.T) {
	monitors := hostMonitors()
	key := tvKey(t)

	for _, desk := range []DeskDuringCouch{DeskDisabled, DeskEnabled, DeskMirror} {
		layout := ConsoleLayout{TVKey: key, TVName: "HDMI-A-1", Mode: "2560x1440@120.00Hz", HDR: true, VRR: true, Desk: desk}
		p, err := BuildConsoleProfile(layout, monitors)
		if err != nil {
			t.Fatalf("desk=%s: %v", desk, err)
		}

		var tv *profile.OutputConfig
		enabled := 0
		for i := range p.Outputs {
			if p.Outputs[i].Enabled {
				enabled++
			}
			if p.Outputs[i].Key == key {
				tv = &p.Outputs[i]
			}
		}
		if tv == nil {
			t.Fatalf("desk=%s: the TV is missing from the profile", desk)
		}
		if !tv.Enabled {
			t.Fatalf("desk=%s: the TV must always be enabled", desk)
		}
		if enabled == 0 {
			t.Fatalf("desk=%s: every output disabled leaves a black screen", desk)
		}
		if tv.Transform != 0 {
			t.Fatalf("desk=%s: the TV must not be rotated, got transform %d", desk, tv.Transform)
		}
		if tv.Scale != 1 {
			t.Fatalf("desk=%s: the TV must run at scale 1, got %v", desk, tv.Scale)
		}
		if tv.X != 0 || tv.Y != 0 {
			t.Fatalf("desk=%s: the TV must sit at the origin, got %d,%d", desk, tv.X, tv.Y)
		}
		if tv.MirrorOf != "" {
			t.Fatalf("desk=%s: the TV must be the mirror source, not a mirror", desk)
		}
		if tv.CM != "hdr" {
			t.Fatalf("desk=%s: HDR was requested, got cm=%q", desk, tv.CM)
		}
		if tv.Mode != "2560x1440@120.00Hz" {
			t.Fatalf("desk=%s: mode not applied, got %q", desk, tv.Mode)
		}
		if p.Workspaces.Enabled {
			t.Fatalf("desk=%s: the console profile must not impose workspace rules", desk)
		}
	}
}

// "Beside" must never overlap the TV, which would put Big Picture half on each
// display.
func TestDeskBesideTheTVNeverOverlapsIt(t *testing.T) {
	layout := ConsoleLayout{TVKey: tvKey(t), Mode: "2560x1440@120.00Hz", Desk: DeskEnabled}
	p, err := BuildConsoleProfile(layout, hostMonitors())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, out := range p.Outputs {
		if out.Key == layout.TVKey || !out.Enabled {
			continue
		}
		if out.X < 2560 {
			t.Fatalf("the desk at x=%d overlaps a 2560-wide TV", out.X)
		}
	}
}

func TestDeskMirrorsTheTVNotTheOtherWayAround(t *testing.T) {
	layout := ConsoleLayout{TVKey: tvKey(t), Mode: "1920x1080@120.00Hz", Desk: DeskMirror}
	p, err := BuildConsoleProfile(layout, hostMonitors())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, out := range p.Outputs {
		if out.Key == layout.TVKey {
			continue
		}
		if out.MirrorOf != layout.TVKey {
			t.Fatalf("the desk should mirror the TV, got mirror_of=%q", out.MirrorOf)
		}
		// DP-1 has 1920x1080@120, so the mirror can match the TV exactly.
		if out.Mode != "1920x1080@120.00Hz" {
			t.Fatalf("a mirror should take a mode both displays have, got %q", out.Mode)
		}
	}
}

func TestBuildRefusesAnInvalidLayout(t *testing.T) {
	layout := ConsoleLayout{TVKey: tvKey(t), Mode: "9999x9999@1.00Hz", Desk: DeskDisabled}
	if _, err := BuildConsoleProfile(layout, hostMonitors()); err == nil {
		t.Fatal("an unbuildable layout must not produce a profile")
	}
}

// gamescope takes its resolution from the console layout rather than asking
// again: two places to say what the TV runs at is one place too many.
func TestGamescopeCommandFollowsTheConsoleLayout(t *testing.T) {
	if !GamescopeAvailable() {
		t.Skip("gamescope is not installed on this machine")
	}
	layout := ConsoleLayout{Mode: "2560x1440@120.00Hz", HDR: true}
	settings := GamescopeSettings{Enabled: true, FPSLimit: 60, MangoApp: true}
	inner := BigPictureLauncher{Command: "/usr/bin/steam", Args: []string{"-gamepadui"}}

	got, err := GamescopeCommand(layout, settings, inner)
	if err != nil {
		t.Fatalf("GamescopeCommand: %v", err)
	}
	line := strings.Join(got.Args, " ")
	for _, want := range []string{"-W 2560", "-H 1440", "-r 120", "--hdr-enabled", "--framerate-limit 60", "--mangoapp", "-- /usr/bin/steam -gamepadui"} {
		if !strings.Contains(line, want) {
			t.Fatalf("expected %q in the launch line, got %q", want, line)
		}
	}
}

func TestGamescopeCommandRejectsAnUnreadableMode(t *testing.T) {
	if _, err := GamescopeCommand(ConsoleLayout{Mode: "preferred"}, GamescopeSettings{Enabled: true}, BigPictureLauncher{}); err == nil {
		t.Fatal("a mode that cannot be parsed must not produce a launch line")
	}
}

func TestGamescopeSummaryReadsAsSettings(t *testing.T) {
	if got := GamescopeSummary(ConsoleLayout{Mode: "x"}, GamescopeSettings{}); got != "off" {
		t.Fatalf("disabled summary = %q", got)
	}
	got := GamescopeSummary(ConsoleLayout{Mode: "2560x1440@120.00Hz", HDR: true}, GamescopeSettings{Enabled: true, FPSLimit: 60})
	for _, want := range []string{"2560x1440", "HDR", "60 fps cap"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}

// The mode heuristic has three traps, all of them live on the development
// host's Samsung TV.
func TestSuggestModeBalancesResolutionAndRefresh(t *testing.T) {
	samsung := hypr.Monitor{
		Name: "HDMI-A-1",
		AvailableModes: []string{
			// 17:9 cinema modes, larger than native and high refresh.
			"4096x2160@120.00Hz", "4096x2160@60.00Hz",
			// Native 16:9, which does reach 120 here.
			"3840x2160@120.00Hz", "3840x2160@60.00Hz",
			"2560x1440@120.00Hz",
			// The highest refresh on the display, at the smallest picture.
			"1920x1080@143.98Hz", "1920x1080@60.00Hz",
		},
	}

	got, ok := suggestMode(samsung, "3840x2160")
	if !ok {
		t.Fatal("a display with modes must yield one")
	}
	if got != "3840x2160@120.00Hz" {
		t.Fatalf("expected native 4K at 120Hz, got %q", got)
	}
}

// Trap one: the largest mode is a 17:9 cinema variant that letterboxes a 16:9
// picture, so the native aspect has to constrain the choice.
func TestSuggestModeIgnoresCinemaAspectModes(t *testing.T) {
	m := hypr.Monitor{
		Name:           "HDMI-A-1",
		AvailableModes: []string{"4096x2160@120.00Hz", "2560x1440@120.00Hz"},
	}
	if got, _ := suggestMode(m, "3840x2160"); got != "2560x1440@120.00Hz" {
		t.Fatalf("a 17:9 mode must not win over a 16:9 one, got %q", got)
	}
}

// Trap two: chasing refresh alone drops the picture to 1080p to gain 24Hz.
func TestSuggestModeDoesNotTradePixelsForAFewHertz(t *testing.T) {
	m := hypr.Monitor{
		Name:           "HDMI-A-1",
		AvailableModes: []string{"2560x1440@120.00Hz", "1920x1080@143.98Hz"},
	}
	if got, _ := suggestMode(m, "3840x2160"); got != "2560x1440@120.00Hz" {
		t.Fatalf("expected the bigger picture at high refresh, got %q", got)
	}
}

// Trap three: a plain 60Hz TV clears no refresh bar, and must still get its
// full resolution rather than nothing.
func TestSuggestModeFallsBackOnASixtyHertzDisplay(t *testing.T) {
	m := hypr.Monitor{
		Name:           "HDMI-A-1",
		AvailableModes: []string{"3840x2160@60.00Hz", "1920x1080@60.00Hz"},
	}
	if got, _ := suggestMode(m, "3840x2160"); got != "3840x2160@60.00Hz" {
		t.Fatalf("expected the full resolution at 60Hz, got %q", got)
	}
}

// With no EDID-preferred mode to read, the ranking must still produce something
// usable rather than refusing.
func TestSuggestModeWithoutAPreferredResolution(t *testing.T) {
	m := hypr.Monitor{Name: "HDMI-A-1", AvailableModes: []string{"2560x1440@120.00Hz", "1920x1080@60.00Hz"}}
	if got, ok := suggestMode(m, ""); !ok || got != "2560x1440@120.00Hz" {
		t.Fatalf("expected a sensible pick without EDID guidance, got %q ok=%v", got, ok)
	}
}

func TestSameAspectTellsSixteenNineFromSeventeenNine(t *testing.T) {
	if !sameAspect(3840.0/2160.0, 2560.0/1440.0) {
		t.Fatal("3840x2160 and 2560x1440 are both 16:9")
	}
	if sameAspect(4096.0/2160.0, 3840.0/2160.0) {
		t.Fatal("17:9 must be told apart from 16:9")
	}
	// 1366x768 is nominally 16:9 but rounds to 1.779.
	if !sameAspect(1366.0/768.0, 16.0/9.0) {
		t.Fatal("the tolerance must survive real-world rounding")
	}
}

// hostMonitors' fixture has a 4096-wide cinema mode, so a suggestion made
// without EDID guidance must not silently become host-dependent.
func TestSuggestWithoutDisplayFactsStillProducesAValidLayout(t *testing.T) {
	layout, err := SuggestConsoleLayout(hostMonitors(), DisplayFacts{})
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if layout.HDR {
		t.Fatal("HDR must not be assumed without evidence from the EDID")
	}
	if err := ValidateConsoleLayout(layout, hostMonitors()); err != nil {
		t.Fatalf("a suggestion must always be valid: %v", err)
	}
}

// Whatever the display, a suggestion has to survive its own validation --
// otherwise enabling couch mode hands the user a layout it then refuses.
func TestEverySuggestionValidates(t *testing.T) {
	cases := map[string][]string{
		"60Hz 4K TV":        {"3840x2160@60.00Hz", "1920x1080@60.00Hz"},
		"cinema modes only": {"4096x2160@120.00Hz", "4096x2160@60.00Hz"},
		"single mode":       {"1920x1080@60.00Hz"},
		"high refresh 1080": {"1920x1080@240.00Hz", "1920x1080@60.00Hz"},
	}
	for name, modes := range cases {
		monitors := []hypr.Monitor{{
			Name: "HDMI-A-1", Description: "Test TV", Make: "T", Model: "V", Serial: "1",
			AvailableModes: modes,
		}}
		layout, err := SuggestConsoleLayout(monitors, DisplayFacts{
			PreferredResolution: map[string]string{"HDMI-A-1": "3840x2160"},
		})
		if err != nil {
			t.Fatalf("%s: suggest: %v", name, err)
		}
		if err := ValidateConsoleLayout(layout, monitors); err != nil {
			t.Fatalf("%s: suggestion %q failed validation: %v", name, layout.Mode, err)
		}
		if _, err := BuildConsoleProfile(layout, monitors); err != nil {
			t.Fatalf("%s: suggestion %q did not build: %v", name, layout.Mode, err)
		}
	}
}

// A TV's mode list is not a list of good choices. This Samsung offers
// 4096x2160, which is 17:9 cinema on a 16:9 panel and sorts above the native
// 3840x2160, so picking the biggest number gets a letterboxed picture.
func TestModeMatchesPanelShape(t *testing.T) {
	tv := hypr.Monitor{Name: "HDMI-A-1"}
	facts := DisplayFacts{PreferredResolution: map[string]string{"HDMI-A-1": "3840x2160"}}

	if _, ok := ModeMatchesPanelShape("3840x2160@120.00Hz", tv, facts); !ok {
		t.Error("the native mode must not be flagged")
	}
	if _, ok := ModeMatchesPanelShape("1920x1080@120.00Hz", tv, facts); !ok {
		t.Error("a mode with the panel's aspect must not be flagged whatever its size")
	}
	native, ok := ModeMatchesPanelShape("4096x2160@120.00Hz", tv, facts)
	if ok {
		t.Error("17:9 on a 16:9 panel must be flagged")
	}
	if native != "3840x2160" {
		t.Errorf("native = %q, want 3840x2160", native)
	}

	// Nothing known about the panel is not the same as something wrong.
	if _, ok := ModeMatchesPanelShape("4096x2160@120.00Hz", tv, DisplayFacts{}); !ok {
		t.Error("an unknown native resolution must not produce a warning")
	}
}
