package hooks

import (
	"context"
	"encoding/json"
	"testing"
)

// The sink for the TV is picked by naming, since the display connector and the
// audio device are separate subsystems with no reliable link.
func TestPickHDMISinkPrefersTheOneNamingTheTV(t *testing.T) {
	sinks := []sink{
		{NodeID: 34, Name: "alsa_output.usb-KTMicro.analog-stereo", Description: "KT USB Audio"},
		{NodeID: 45, Name: "alsa_output.pci-0000_0f_00.4.iec958-stereo", Description: "Starship HD Audio"},
		{NodeID: 57, Name: "alsa_output.pci-0000_0d_00.1.hdmi-stereo", Description: "Navi 48 HDMI/DP Audio Controller"},
	}

	got, ok := pickHDMISink(sinks, Env{})
	if !ok || got.NodeID != 57 {
		t.Fatalf("expected the HDMI sink, got %+v ok=%v", got, ok)
	}

	// With two HDMI devices, the one whose description names the display wins.
	sinks = append(sinks, sink{NodeID: 61, Name: "alsa_output.pci-0000_09_00.1.hdmi-stereo", Description: "Samsung Living Room"})
	got, ok = pickHDMISink(sinks, Env{TVDescription: "Samsung Electric Company SAMSUNG"})
	if !ok || got.NodeID != 61 {
		t.Fatalf("expected the sink naming the TV, got %+v", got)
	}
}

func TestPickHDMISinkReportsWhenThereIsNone(t *testing.T) {
	sinks := []sink{{NodeID: 34, Name: "alsa_output.usb-KTMicro.analog-stereo", Description: "KT USB Audio"}}
	if _, ok := pickHDMISink(sinks, Env{}); ok {
		t.Fatal("a machine with no HDMI output must not report one")
	}
}

// A short word in the display description must not match half the sinks on the
// machine.
func TestDescriptionMatchIgnoresShortWords(t *testing.T) {
	s := sink{Description: "HD Audio Controller"}
	if descriptionMatches(s, "LG TV") {
		t.Fatal("two- and three-letter words must not be enough to match")
	}
	if !descriptionMatches(s, "Controller Something") {
		t.Fatal("a real word should still match")
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
