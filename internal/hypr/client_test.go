package hypr

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseEventAcceptsMonitorV2Events(t *testing.T) {
	tests := []struct {
		line string
		want EventType
	}{
		{
			line: "monitoraddedv2>>3,DP-1,Dell U2720Q",
			want: EventMonitorAdded,
		},
		{
			line: "monitorremovedv2>>3,DP-1,Dell U2720Q",
			want: EventMonitorRemoved,
		},
	}

	for _, tt := range tests {
		event, ok := parseEvent(tt.line)
		if !ok {
			t.Fatalf("expected %q to be parsed", tt.line)
		}
		if event.Type != tt.want {
			t.Fatalf("expected %q to map to %q, got %q", tt.line, tt.want, event.Type)
		}
		if event.Raw != tt.line {
			t.Fatalf("expected raw line to be preserved, got %q", event.Raw)
		}
	}
}

func TestSelectInstancePrefersWaylandDisplay(t *testing.T) {
	instances := []instanceInfo{
		{Instance: "sig-a", WLSocket: "wayland-0"},
		{Instance: "sig-b", WLSocket: "wayland-1"},
	}

	got, err := selectInstance(instances, "wayland-1")
	if err != nil {
		t.Fatalf("selectInstance returned error: %v", err)
	}
	if got != "sig-b" {
		t.Fatalf("expected wayland match to win, got %q", got)
	}
}

func TestSelectInstanceFallsBackToOnlyInstance(t *testing.T) {
	got, err := selectInstance([]instanceInfo{{Instance: "sig-a", WLSocket: "wayland-0"}}, "")
	if err != nil {
		t.Fatalf("selectInstance returned error: %v", err)
	}
	if got != "sig-a" {
		t.Fatalf("expected single instance to be selected, got %q", got)
	}
}

func TestSelectInstanceErrorsWhenAmbiguous(t *testing.T) {
	_, err := selectInstance([]instanceInfo{
		{Instance: "sig-a", WLSocket: "wayland-0"},
		{Instance: "sig-b", WLSocket: "wayland-1"},
	}, "")
	if err == nil {
		t.Fatal("expected ambiguous instance selection to fail")
	}
}

func TestMonitorsDiscoversInstanceWhenEnvMissing(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "calls.log")
	hyprctlPath := filepath.Join(tmp, "hyprctl")
	script := `#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "` + logPath + `"
if [ "$#" -eq 2 ] && [ "$1" = "-j" ] && [ "$2" = "instances" ]; then
	cat <<'EOF'
[{"instance":"sig-test","wl_socket":"wayland-9"}]
EOF
	exit 0
fi
if [ "$#" -eq 5 ] && [ "$1" = "--instance" ] && [ "$2" = "sig-test" ] && [ "$3" = "-j" ] && [ "$4" = "monitors" ] && [ "$5" = "all" ]; then
	echo '[]'
	exit 0
fi
exit 1
`
	if err := os.WriteFile(hyprctlPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake hyprctl: %v", err)
	}

	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")
	t.Setenv("WAYLAND_DISPLAY", "")

	client := &Client{hyprctl: hyprctlPath}
	monitors, err := client.Monitors(context.Background())
	if err != nil {
		t.Fatalf("Monitors returned error: %v", err)
	}
	if len(monitors) != 0 {
		t.Fatalf("expected no monitors, got %d", len(monitors))
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	got := string(data)
	want := "-j instances\n--instance sig-test -j monitors all\n"
	if got != want {
		t.Fatalf("unexpected hyprctl calls:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestEvalReturnsHyprlandResponse(t *testing.T) {
	tmp := t.TempDir()
	hyprctlPath := filepath.Join(tmp, "hyprctl")
	script := `#!/usr/bin/env bash
set -eu
if [ "${1-}" = "--instance" ]; then
  shift 2
fi
if [ "${1-}" != "eval" ]; then
  exit 2
fi
printf '%s' "$HYPRMONCFG_TEST_EVAL_REPLY"
exit "$HYPRMONCFG_TEST_EVAL_STATUS"
`
	if err := os.WriteFile(hyprctlPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake hyprctl: %v", err)
	}
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "sig-test")
	client := &Client{hyprctl: hyprctlPath}

	t.Run("success", func(t *testing.T) {
		t.Setenv("HYPRMONCFG_TEST_EVAL_REPLY", "ok")
		t.Setenv("HYPRMONCFG_TEST_EVAL_STATUS", "0")
		response, err := client.Eval(context.Background(), "assert(true)")
		if err != nil {
			t.Fatalf("eval: %v", err)
		}
		if response != "ok" {
			t.Fatalf("expected ok response, got %q", response)
		}
	})

	t.Run("failure preserves response", func(t *testing.T) {
		t.Setenv("HYPRMONCFG_TEST_EVAL_REPLY", "error: probe missing")
		t.Setenv("HYPRMONCFG_TEST_EVAL_STATUS", "1")
		response, err := client.Eval(context.Background(), "assert(false)")
		if err == nil {
			t.Fatal("expected eval failure")
		}
		if response != "error: probe missing" {
			t.Fatalf("expected Hyprland response to be preserved, got %q", response)
		}
	})
}

func TestMonitorOutputKeyUsesStableMSTPathForDuplicateMonitors(t *testing.T) {
	monitors := []Monitor{
		{Name: "DP-10", Make: "Sceptre Tech Inc", Model: "Sceptre Z27", ConnectorPath: "mst:532-3"},
		{Name: "DP-6", Make: "Sceptre Tech Inc", Model: "Sceptre Z27", ConnectorPath: "mst:519-2"},
	}
	counts := MonitorMatchCounts(monitors)

	if got := MonitorOutputKey(monitors[0], counts); got != "sceptre tech inc|sceptre z27@mst-3" {
		t.Fatalf("expected stable MST key for left monitor, got %q", got)
	}
	if got := MonitorOutputKey(monitors[1], counts); got != "sceptre tech inc|sceptre z27@mst-2" {
		t.Fatalf("expected stable MST key for right monitor, got %q", got)
	}
}

func TestMonitorOutputKeyFallsBackToConnectorNameWithoutPath(t *testing.T) {
	monitors := []Monitor{
		{Name: "DP-5", Make: "Sceptre Tech Inc", Model: "Sceptre Z27"},
		{Name: "DP-8", Make: "Sceptre Tech Inc", Model: "Sceptre Z27"},
	}
	counts := MonitorMatchCounts(monitors)

	if got := MonitorOutputKey(monitors[0], counts); got != "sceptre tech inc|sceptre z27@dp-5" {
		t.Fatalf("expected connector fallback key, got %q", got)
	}
}

func TestMonitorSelectorUsesDescriptionContainingCommentMarker(t *testing.T) {
	monitor := Monitor{Name: "DP-2", Description: "Dell Inc. Dell S2716DG #ASMy+EjOdybd"}

	if got := monitor.MonitorSelector(); got != "desc:Dell Inc. Dell S2716DG #ASMy+EjOdybd" {
		t.Fatalf("expected desc selector to preserve monitor description, got %q", got)
	}
}

func TestDRMConnectorEntriesParseSysfsConnectors(t *testing.T) {
	tmp := t.TempDir()
	connectorDir := filepath.Join(tmp, "card1-DP-10")
	if err := os.MkdirAll(connectorDir, 0o755); err != nil {
		t.Fatalf("create connector fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(connectorDir, "connector_id"), []byte("95\n"), 0o644); err != nil {
		t.Fatalf("write connector id: %v", err)
	}

	entries := drmConnectorEntries(tmp, map[string]bool{"DP-10": true})
	if len(entries) != 1 {
		t.Fatalf("expected one connector entry, got %v", entries)
	}
	if entries[0].card != "card1" || entries[0].name != "DP-10" || entries[0].connectorID != 95 {
		t.Fatalf("unexpected connector entry: %+v", entries[0])
	}
}

func TestSocket2PathDiscoversInstanceWhenEnvMissing(t *testing.T) {
	tmp := t.TempDir()
	hyprctlPath := filepath.Join(tmp, "hyprctl")
	script := `#!/usr/bin/env bash
set -eu
if [ "$#" -eq 2 ] && [ "$1" = "-j" ] && [ "$2" = "instances" ]; then
	cat <<'EOF'
[{"instance":"sig-test","wl_socket":"wayland-9"}]
EOF
	exit 0
fi
exit 1
`
	if err := os.WriteFile(hyprctlPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake hyprctl: %v", err)
	}

	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	client := &Client{hyprctl: hyprctlPath}
	got, err := client.socket2Path(context.Background())
	if err != nil {
		t.Fatalf("socket2Path returned error: %v", err)
	}
	want := "/run/user/1000/hypr/sig-test/.socket2.sock"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNormalizeMirrorTargetsResolvesMonitorIDs(t *testing.T) {
	// hyprctl reports the mirror source as a monitor ID, and "none" when a
	// monitor mirrors nothing.
	monitors := []Monitor{
		{ID: 1, Name: "DP-1", MirrorOf: "none"},
		{ID: 0, Name: "HDMI-A-1", MirrorOf: "1"},
		{ID: 2, Name: "DP-2", MirrorOf: "DP-1"},
		{ID: 3, Name: "DP-3", MirrorOf: "7"},
	}

	normalizeMirrorTargets(monitors)

	for _, tc := range []struct {
		name string
		want string
	}{
		{"DP-1", ""},
		{"HDMI-A-1", "DP-1"},
		{"DP-2", "DP-1"},
		{"DP-3", "7"},
	} {
		for _, monitor := range monitors {
			if monitor.Name != tc.name {
				continue
			}
			if monitor.MirrorOf != tc.want {
				t.Fatalf("%s mirrors %q, want %q", tc.name, monitor.MirrorOf, tc.want)
			}
		}
	}
}

func TestDropSyntheticMonitorsRemovesHyprlandsFallbackHead(t *testing.T) {
	monitors := []Monitor{
		{Name: "eDP-1", Make: "BOE", Model: "0x0CFD", Disabled: true},
		{Name: "FALLBACK"},
		{Name: "DP-1", Make: "Dell", Model: "U2720Q"},
	}

	got := dropSyntheticMonitors(monitors)
	if len(got) != 2 {
		t.Fatalf("got %d monitors, want 2: %+v", len(got), got)
	}
	for _, monitor := range got {
		if monitor.Name == "FALLBACK" {
			t.Fatal("the synthetic head survived filtering")
		}
	}
}

func TestDropSyntheticMonitorsKeepsRealConnectors(t *testing.T) {
	monitors := []Monitor{
		{Name: "eDP-1"}, {Name: "DP-1"}, {Name: "HDMI-A-1"}, {Name: "DP-2"},
	}

	if got := dropSyntheticMonitors(monitors); len(got) != len(monitors) {
		t.Fatalf("filtering dropped a real connector: %+v", got)
	}
}

// The daemon acts on every event its monitor feed delivers, so a window event
// arriving there makes it re-derive the layout on each focus change. Prefix
// matching used to let exactly that through.
func TestMonitorEventTypesExcludeWindowActivity(t *testing.T) {
	monitorFeed := make(map[EventType]struct{}, len(MonitorEventTypes))
	for _, t := range MonitorEventTypes {
		monitorFeed[t] = struct{}{}
	}

	for _, line := range []string{
		"activewindow>>steam,Steam",
		"activewindowv2>>55d99b4b2f60",
		"openwindow>>55d99b4b2f60,1,steam,Steam",
		"closewindow>>55d99b4b2f60",
		"windowtitlev2>>55d99b4b2f60,Steam Big Picture Mode",
	} {
		event, ok := parseEvent(line)
		if !ok {
			continue
		}
		if _, reaches := monitorFeed[event.Type]; reaches {
			t.Fatalf("%q reached the monitor feed as %q", line, event.Type)
		}
	}
}

// Window and config events still have to parse: couch mode drives Big Picture
// detection off them, and re-asserts a runtime layout after a config reload.
func TestParseEventRecognisesWindowAndConfigEvents(t *testing.T) {
	tests := []struct {
		line      string
		want      EventType
		wantValue string
	}{
		{"openwindow>>55d99b4b2f60,1,steam,Steam", EventWindowOpened, "55d99b4b2f60,1,steam,Steam"},
		{"closewindow>>55d99b4b2f60", EventWindowClosed, "55d99b4b2f60"},
		{"windowtitle>>55d99b4b2f60", EventWindowTitle, "55d99b4b2f60"},
		{"windowtitlev2>>55d99b4b2f60,Steam Big Picture Mode", EventWindowTitle, "55d99b4b2f60,Steam Big Picture Mode"},
		{"configreloaded>>", EventConfigReloaded, ""},
	}

	for _, tt := range tests {
		event, ok := parseEvent(tt.line)
		if !ok {
			t.Fatalf("expected %q to be parsed", tt.line)
		}
		if event.Type != tt.want {
			t.Fatalf("expected %q to map to %q, got %q", tt.line, tt.want, event.Type)
		}
		if event.Value != tt.wantValue {
			t.Fatalf("expected value %q from %q, got %q", tt.wantValue, tt.line, event.Value)
		}
	}
}

// activewindow is the highest-frequency event Hyprland emits. Nothing maps to
// it, so it can never wake a subscriber that did not ask for it.
func TestParseEventIgnoresActiveWindow(t *testing.T) {
	for _, line := range []string{"activewindow>>steam,Steam", "activewindowv2>>55d99b4b2f60"} {
		if _, ok := parseEvent(line); ok {
			t.Fatalf("expected %q to be ignored", line)
		}
	}
}
