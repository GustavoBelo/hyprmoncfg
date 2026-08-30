package couch

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crmne/hyprmoncfg/internal/hypr"
)

func TestIsBigPictureWindow(t *testing.T) {
	cases := []struct {
		class string
		title string
		want  bool
	}{
		{"steam", "Steam Big Picture Mode", true},
		{"Steam", "Big Picture", true},
		{"", "Big Picture", false},
		{"firefox", "Big Picture Movies - YouTube", false},
		{"steam", "Steam", false},
		{"", "", false},
	}
	for _, tc := range cases {
		if got := IsBigPictureWindow(tc.class, tc.title); got != tc.want {
			t.Fatalf("IsBigPictureWindow(%q, %q) = %v, want %v", tc.class, tc.title, got, tc.want)
		}
	}
}

type fakeSource struct {
	windows  []hypr.Window
	monitors []hypr.Monitor
}

func (f *fakeSource) Clients(context.Context) ([]hypr.Window, error) {
	return f.windows, nil
}

func (f *fakeSource) Monitors(context.Context) ([]hypr.Monitor, error) {
	return f.monitors, nil
}

func TestDetectorMatchesClassicTitleEvenWhenKnown(t *testing.T) {
	source := &fakeSource{windows: []hypr.Window{
		{Address: "0x1", Class: "steam", Title: "Steam Big Picture Mode"},
	}}
	detector := NewBigPictureDetector(context.Background(), source)
	if got := detector.Count(context.Background()); got != 1 {
		t.Fatalf("classic title must always match, got count %d", got)
	}
}

// On some setups Big Picture shows up titled just "Steam"; a steam window that
// did not exist before launch is therefore treated as Big Picture.
func TestDetectorMatchesNewSteamWindow(t *testing.T) {
	source := &fakeSource{windows: []hypr.Window{
		{Address: "0x1", Class: "steam", Title: "Steam Library"},
		{Address: "0x2", Class: "steam", Title: "Steam"},
	}}
	detector := NewBigPictureDetector(context.Background(), source)
	source.windows = []hypr.Window{
		{Address: "0x1", Class: "steam", Title: "Steam Library"},
		{Address: "0x2", Class: "steam", Title: "Steam"},
		{Address: "0x3", Class: "steam", Title: "Steam"},
	}
	got := detector.Windows(context.Background())
	if len(got) != 1 || got[0].Address != "0x3" {
		t.Fatalf("expected only the new window 0x3, got %+v", got)
	}

	source.windows = source.windows[:2]
	if got := detector.Count(context.Background()); got != 0 {
		t.Fatalf("pre-existing desktop windows must not match, got count %d", got)
	}
}

// When BPM reuses the pre-existing window, geometry is the remaining tell.
func TestDetectorMatchesFullscreenCoverage(t *testing.T) {
	source := &fakeSource{
		windows: []hypr.Window{
			{Address: "0x1", Class: "steam", Title: "Steam", Monitor: "HDMI-A-1", Size: [2]int{2560, 1440}},
			{Address: "0x2", Class: "steam", Title: "Steam", Monitor: "DP-1", Size: [2]int{1280, 720}},
		},
		monitors: []hypr.Monitor{
			{Name: "HDMI-A-1", Width: 2560, Height: 1440},
			{Name: "DP-1", Width: 1920, Height: 1080},
		},
	}
	detector := NewBigPictureDetector(context.Background(), source)
	got := detector.Windows(context.Background())
	if len(got) != 1 || got[0].Address != "0x1" {
		t.Fatalf("only the output-covering window should match, got %+v", got)
	}
}

func TestResolveLauncherPicksValidCommand(t *testing.T) {
	launcher, existing, err := ResolveLauncher()
	if err != nil {
		t.Skip("no steam in PATH")
	}
	switch filepath.Base(launcher.Command) {
	case "bazzite-steam-bpm":
		if existing {
			t.Fatalf("bazzite launcher ignores running instances")
		}
	case "steam":
		switch {
		case existing && len(launcher.Args) == 1 && launcher.Args[0] == "steam://open/bigpicture":
		case !existing && len(launcher.Args) == 1 && launcher.Args[0] == "-gamepadui":
		default:
			t.Fatalf("unexpected steam args (existing=%v): %v", existing, launcher.Args)
		}
	default:
		t.Fatalf("unexpected launcher: %s", launcher.Command)
	}
}

// The exact shape that failed on the host, four sessions in a row.
//
// Omarchy forces Steam floating and pins `class=steam, title=Steam` to
// 1100x700, so Big Picture opens as a known, floating, non-fullscreen window
// that covers nothing. Every tell the old detector had went silent. Only the
// title change rescues it.
func TestDetectorSurvivesOmarchyFloatingSteamRule(t *testing.T) {
	monitors := []hypr.Monitor{{ID: 2, Name: "HDMI-A-1", Width: 2560, Height: 1440}}
	floating := hypr.Window{
		Address: "0x1", Class: "steam", InitialClass: "steam",
		Title: "Steam", InitialTitle: "Steam",
		Monitor: "2", Size: [2]int{1100, 700}, Fullscreen: 0,
	}

	source := &fakeSource{windows: []hypr.Window{floating}, monitors: monitors}
	// Snapshot first: Steam was already running, so this window is "known" and
	// steam://open/bigpicture reuses it rather than opening a new one.
	detector := NewBigPictureDetector(context.Background(), source)

	if got := detector.CertainCount(context.Background()); got != 0 {
		t.Fatalf("the plain desktop Steam window must not read as Big Picture, got %d", got)
	}

	// Steam relabels the window shortly after it maps.
	source.windows[0].Title = "Steam Big Picture Mode"
	if got := detector.CertainCount(context.Background()); got != 1 {
		t.Fatalf("the title change is the only surviving tell and must be caught, got %d", got)
	}
}

// An automatic trigger must not fire on the ordinary library window, or opening
// Steam on the desktop would drag the user onto the TV.
func TestCertainConfidenceIgnoresPlainSteamWindows(t *testing.T) {
	source := &fakeSource{windows: []hypr.Window{
		{Address: "0x1", Class: "steam", Title: "Steam"},
		{Address: "0x2", Class: "steam", Title: "Friends List"},
	}}
	detector := NewBigPictureDetector(context.Background(), source)

	// Both are new to a detector that snapshotted an empty desktop, so the weak
	// tell fires...
	source.windows = append(source.windows, hypr.Window{Address: "0x3", Class: "steam", Title: "Steam"})
	if got := detector.Count(context.Background()); got == 0 {
		t.Fatal("the weak tell should still see a newly appeared Steam window")
	}
	// ...but nothing here names itself as the Gamepad UI.
	if got := detector.CertainCount(context.Background()); got != 0 {
		t.Fatalf("no window here is Big Picture, got %d certain", got)
	}
}

// A fullscreen Steam window counts even without the title, which is what
// happens once the session neutralises Omarchy's floating rule.
func TestDetectorAcceptsFullscreenSteamWindow(t *testing.T) {
	source := &fakeSource{windows: []hypr.Window{
		{Address: "0x1", Class: "steam", Title: "Steam", Fullscreen: 2},
	}}
	detector := NewBigPictureDetector(context.Background(), source)
	if got := detector.CertainCount(context.Background()); got != 1 {
		t.Fatalf("fullscreen is a certain tell, got %d", got)
	}
}

// initialTitle carries the marker on builds where the live title has already
// moved on to the running game.
func TestDetectorReadsInitialTitle(t *testing.T) {
	source := &fakeSource{windows: []hypr.Window{
		{Address: "0x1", Class: "steam", Title: "Hollow Knight", InitialTitle: "Steam Big Picture Mode"},
	}}
	detector := NewBigPictureDetector(context.Background(), source)
	if got := detector.CertainCount(context.Background()); got != 1 {
		t.Fatalf("initialTitle must be consulted, got %d", got)
	}
}

// A browser reading about Big Picture is not Big Picture.
func TestDetectorIgnoresUnrelatedWindowsNamingBigPicture(t *testing.T) {
	source := &fakeSource{windows: []hypr.Window{
		{Address: "0x1", Class: "firefox", Title: "Steam Big Picture Mode - Google"},
		{Address: "0x2", Class: "chromium", InitialClass: "chromium", Title: "gamepadui docs"},
	}}
	detector := NewBigPictureDetector(context.Background(), source)
	if got := detector.Count(context.Background()); got != 0 {
		t.Fatalf("unrelated windows matched, got %d", got)
	}
}

// The 90s dead wait: with Steam already up, no new PID ever appears, so asking
// for one first meant detection did not start until the timeout expired. The
// session log shows exactly that gap -- "launched" 20:13:26, "no new Steam
// process" 20:14:57 -- with Big Picture already on screen throughout.
func TestResolveSteamPIDDoesNotWaitForAnExistingInstance(t *testing.T) {
	state := t.TempDir()
	restore := stubSteamPIDs(t, []int{4242})

	start := time.Now()
	pid := ResolveSteamPID(true, map[int]struct{}{4242: {}}, state)
	elapsed := time.Since(start)
	restore()

	if pid != 4242 {
		t.Fatalf("expected the running Steam (4242), got %d", pid)
	}
	if elapsed > time.Second {
		t.Fatalf("a running Steam must resolve immediately, took %s", elapsed)
	}
}

// A cold start has no Steam yet, so waiting for a new PID is the right thing;
// the launcher will bring one up.
func TestResolveSteamPIDWaitsForAColdStart(t *testing.T) {
	state := t.TempDir()
	restore := stubSteamPIDs(t, nil)
	defer restore()

	// Steam appears a moment after the launcher runs.
	go func() {
		time.Sleep(200 * time.Millisecond)
		setSteamPIDs([]int{7777})
	}()

	start := time.Now()
	pid := ResolveSteamPID(false, map[int]struct{}{}, state)
	if pid != 7777 {
		t.Fatalf("expected the newly started Steam (7777), got %d", pid)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("a new Steam should be picked up as soon as it appears, took %s", elapsed)
	}
}

var steamPIDMu sync.Mutex

func stubSteamPIDs(t *testing.T, pids []int) func() {
	t.Helper()
	original := livePIDs
	setSteamPIDs(pids)
	livePIDs = func() []int {
		steamPIDMu.Lock()
		defer steamPIDMu.Unlock()
		return append([]int(nil), stubbedPIDs...)
	}
	return func() { livePIDs = original }
}

var stubbedPIDs []int

func setSteamPIDs(pids []int) {
	steamPIDMu.Lock()
	defer steamPIDMu.Unlock()
	stubbedPIDs = pids
}

// Omarchy pins Big Picture to a 1100x700 floating window, and Steam falls back
// into it when a game exits. The session has to tile it before going
// fullscreen: a floating window snaps back to its floating geometry otherwise.
func TestKeepBigPictureFullscreenTilesBeforeGoingFullscreen(t *testing.T) {
	client := &fakeCloser{windows: []hypr.Window{
		{Address: "0xaa", Class: "steam", Title: "Steam Big Picture Mode", Floating: true, Fullscreen: 0, Size: [2]int{1100, 700}},
	}}
	detector := &BigPictureDetector{Source: client, Known: map[string]bool{}}

	if fixed := KeepBigPictureFullscreen(context.Background(), client, detector); fixed != 1 {
		t.Fatalf("expected one window fixed, got %d", fixed)
	}
	dispatched := client.dispatched()
	if len(dispatched) < 2 {
		t.Fatalf("expected a tile then a fullscreen, got %v", dispatched)
	}
	if !strings.HasPrefix(dispatched[0], "settiled") {
		t.Fatalf("the window must be tiled first, got %v", dispatched)
	}
}

// A window that is already fullscreen is left alone, so the re-assert on every
// title change is not a stream of pointless dispatches.
func TestKeepBigPictureFullscreenLeavesAFullscreenWindowAlone(t *testing.T) {
	client := &fakeCloser{windows: []hypr.Window{
		{Address: "0xaa", Class: "steam", Title: "Steam Big Picture Mode", Fullscreen: 2},
	}}
	detector := &BigPictureDetector{Source: client, Known: map[string]bool{}}

	if fixed := KeepBigPictureFullscreen(context.Background(), client, detector); fixed != 0 {
		t.Fatalf("nothing needed fixing, got %d", fixed)
	}
	if got := client.dispatched(); len(got) != 0 {
		t.Fatalf("expected no dispatches, got %v", got)
	}
}

func TestWindowEventAddressReadsBothPayloadShapes(t *testing.T) {
	cases := []struct {
		value string
		want  string
	}{
		{"55d99b4b2f60,1,steam,Steam", "55d99b4b2f60"},          // openwindow
		{"55d99b4b2f60,Steam Big Picture Mode", "55d99b4b2f60"}, // windowtitlev2
		{"55d99b4b2f60", "55d99b4b2f60"},                        // closewindow
		{"", ""},
	}
	for _, tc := range cases {
		if got := WindowEventAddress(hypr.Event{Value: tc.value}); got != tc.want {
			t.Fatalf("WindowEventAddress(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

// The payload filter rejects obviously unrelated windows cheaply, but a bare
// windowtitle event carries only an address, so it must fall through rather
// than deciding on no evidence.
func TestEventLooksLikeBigPictureFiltersWithoutOverreaching(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"0xaa,1,steam,Steam", true},
		{"0xaa,Steam Big Picture Mode", true},
		{"0xaa,1,code,PLAN.md - Visual Studio Code", false},
		{"0xaa", true}, // address only: not enough to judge
		{"", true},
	}
	for _, tc := range cases {
		if got := EventLooksLikeBigPicture(hypr.Event{Value: tc.value}); got != tc.want {
			t.Fatalf("EventLooksLikeBigPicture(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
