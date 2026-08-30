package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The TV's audio output is found through the EDID, because nothing else links
// a display connector to a sound device. Every HDMI sink on this card is
// described as the graphics card itself.
func TestMatchTVPinFindsTheDisplayByItsEDIDName(t *testing.T) {
	pins := []eldPin{
		{Card: 1, Pin: 0, MonitorName: "25G64"},   // the desk monitor
		{Card: 1, Pin: 1, MonitorName: "SAMSUNG"}, // the TV
	}

	got, ok := matchTVPin(pins, "Samsung Electric Company SAMSUNG 0x01000E00")
	if !ok || got.Pin != 1 {
		t.Fatalf("expected the TV on pin 1, got %+v ok=%v", got, ok)
	}

	got, ok = matchTVPin(pins, "Technical Concepts Ltd 25G64")
	if !ok || got.Pin != 0 {
		t.Fatalf("expected the desk monitor on pin 0, got %+v ok=%v", got, ok)
	}
}

// Refusing beats guessing: sending a console session's sound to the desk
// monitor is worse than leaving it where the user had it.
func TestMatchTVPinRefusesWhenNothingMatches(t *testing.T) {
	pins := []eldPin{{Card: 1, Pin: 0, MonitorName: "25G64"}}
	if _, ok := matchTVPin(pins, "Samsung Electric Company SAMSUNG"); ok {
		t.Fatal("a pin that is not the TV must not be offered as one")
	}
	if _, ok := matchTVPin(nil, "Samsung Electric Company SAMSUNG"); ok {
		t.Fatal("no pins means no answer")
	}
}

// A two- or three-letter monitor name is inside half the descriptions on a
// machine, so it cannot be enough on its own.
func TestMatchTVPinIgnoresShortNames(t *testing.T) {
	pins := []eldPin{{Card: 1, Pin: 0, MonitorName: "LG"}}
	if _, ok := matchTVPin(pins, "LG Electronics OLED"); ok {
		t.Fatal("a two-letter name must not decide which pin is the TV")
	}
}

func TestMatchTVPinPrefersTheLongerName(t *testing.T) {
	pins := []eldPin{
		{Card: 1, Pin: 0, MonitorName: "SAMS"},
		{Card: 1, Pin: 1, MonitorName: "SAMSUNG"},
	}
	got, ok := matchTVPin(pins, "Samsung Electric Company SAMSUNG")
	if !ok || got.Pin != 1 {
		t.Fatalf("expected the more specific match, got %+v", got)
	}
}

func TestReadELDPinsSkipsPinsWithNoDisplay(t *testing.T) {
	root := t.TempDir()
	card := filepath.Join(root, "card1")
	if err := os.MkdirAll(card, 0o755); err != nil {
		t.Fatal(err)
	}
	writeELD(t, filepath.Join(card, "eld#0.0"), "1", "25G64")
	writeELD(t, filepath.Join(card, "eld#0.1"), "1", "SAMSUNG")
	// A connector with nothing plugged in reports present 0 and no name.
	writeELD(t, filepath.Join(card, "eld#0.2"), "0", "")

	pins := readELDPins(root)
	if len(pins) != 2 {
		t.Fatalf("got %d pins, want 2: %+v", len(pins), pins)
	}
	if pins[0].Pin != 0 || pins[0].MonitorName != "25G64" {
		t.Errorf("first pin = %+v", pins[0])
	}
	if pins[1].Card != 1 || pins[1].Pin != 1 || pins[1].MonitorName != "SAMSUNG" {
		t.Errorf("second pin = %+v", pins[1])
	}
}

// The pin index is what names the port, and the port is what says which
// display a sink actually reaches.
func TestPortForProfile(t *testing.T) {
	cases := map[string]string{
		"output:hdmi-stereo":            "hdmi-output-0",
		"output:hdmi-stereo-extra1":     "hdmi-output-1",
		"output:hdmi-surround-extra2":   "hdmi-output-2",
		"output:hdmi-surround71-extra3": "hdmi-output-3",
	}
	for profile, want := range cases {
		got, ok := portForProfile(profile)
		if !ok || got != want {
			t.Errorf("portForProfile(%q) = %q ok=%v, want %q", profile, got, ok, want)
		}
	}
	for _, profile := range []string{"off", "pro-audio", "output:analog-stereo"} {
		if _, ok := portForProfile(profile); ok {
			t.Errorf("portForProfile(%q) claimed an HDMI port", profile)
		}
	}
}

// Switching a card that is already right would drop the sound for a moment for
// nothing.
func TestProfileForPortKeepsTheActiveProfile(t *testing.T) {
	c := card{
		ActiveProfile: "output:hdmi-surround-extra1",
		Profiles: map[string][]string{
			"hdmi-output-1": {"output:hdmi-stereo-extra1", "output:hdmi-surround-extra1"},
		},
	}
	if got, _ := c.profileForPort("hdmi-output-1"); got != "output:hdmi-surround-extra1" {
		t.Errorf("profile = %q, want the active one kept", got)
	}
}

// Otherwise stereo, which is the shortest name among a port's variants.
func TestProfileForPortPrefersStereo(t *testing.T) {
	c := card{
		ActiveProfile: "output:hdmi-stereo",
		Profiles: map[string][]string{
			"hdmi-output-1": {"output:hdmi-stereo-extra1", "output:hdmi-surround71-extra1", "output:hdmi-surround-extra1"},
		},
	}
	got, ok := c.profileForPort("hdmi-output-1")
	if !ok || got != "output:hdmi-stereo-extra1" {
		t.Errorf("profile = %q, want output:hdmi-stereo-extra1", got)
	}
	if _, ok := c.profileForPort("hdmi-output-2"); ok {
		t.Error("a port no available profile carries must not be offered")
	}
}

func writeELD(t *testing.T, path, present, name string) {
	t.Helper()
	body := "monitor_present\t\t" + present + "\neld_valid\t\t1\n"
	if name != "" {
		body += "monitor_name\t\t" + name + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A hook records what it found as data, not as a closure, so the record
// survives the process that made it. A daemon killed mid-session used to leave
// the bar hidden and sound on the TV with nothing able to undo either.
func TestHookStateIsSerialisableAndSurvivesTheProcess(t *testing.T) {
	applied := map[string]State{
		"audio": {"previous_sink": "alsa_output.usb-KTMicro.analog-stereo"},
		"bar":   {"hidden": "false"},
	}

	encoded, err := json.Marshal(applied)
	if err != nil {
		t.Fatalf("a hook record must be serialisable: %v", err)
	}
	var decoded map[string]State
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["audio"]["previous_sink"] != "alsa_output.usb-KTMicro.analog-stereo" {
		t.Fatalf("the previous output was lost: %+v", decoded)
	}
	if got := Names(decoded); len(got) != 2 {
		t.Fatalf("Names = %v, want both hooks", got)
	}
}

// Names follows the order hooks are applied in, not map order, so the log line
// is stable between runs.
func TestNamesIsStable(t *testing.T) {
	applied := map[string]State{"bar": {}, "audio": {}, "idle": {}}
	first := Names(applied)
	for i := 0; i < 20; i++ {
		if got := Names(applied); !equalStrings(got, first) {
			t.Fatalf("Names is unstable: %v then %v", first, got)
		}
	}
	if first[0] != "audio" {
		t.Fatalf("expected apply order, got %v", first)
	}
}

// Undoing a record that names a hook this build no longer has must not stop
// the rest from being put back.
func TestLeaveSkipsUnknownHooks(t *testing.T) {
	err := Leave(context.Background(), Env{}, map[string]State{"a-hook-from-the-future": {}})
	if err != nil {
		t.Fatalf("an unknown hook is not a failure to report: %v", err)
	}
}

func TestLeaveOnAnEmptyRecordIsSafe(t *testing.T) {
	if err := Leave(context.Background(), Env{}, nil); err != nil {
		t.Fatalf("nothing to undo: %v", err)
	}
	if got := Names(nil); len(got) != 0 {
		t.Fatalf("Names(nil) = %v", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
