package hooks

import (
	"context"
	"errors"
	"testing"
)

type fakeHook struct {
	name      string
	available bool
	enterErr  error
	entered   *int
	undone    *int
	undoErr   error
}

func (f *fakeHook) Name() string        { return f.name }
func (f *fakeHook) Description() string { return f.name }
func (f *fakeHook) Available() bool     { return f.available }
func (f *fakeHook) Enter(context.Context, Env) (Undo, error) {
	if f.enterErr != nil {
		return nil, f.enterErr
	}
	*f.entered++
	return func(context.Context) error {
		*f.undone++
		return f.undoErr
	}, nil
}

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

// Undo runs in reverse and keeps going past a failure, so one stuck hook
// cannot strand the rest of the desktop in console mode.
func TestLeaveUndoesInReverseAndSurvivesFailures(t *testing.T) {
	var order []string
	session := &Session{undos: []namedUndo{
		{name: "first", undo: func(context.Context) error { order = append(order, "first"); return nil }},
		{name: "broken", undo: func(context.Context) error { order = append(order, "broken"); return errors.New("nope") }},
		{name: "last", undo: func(context.Context) error { order = append(order, "last"); return nil }},
	}}

	err := session.Leave(context.Background(), Env{})
	if err == nil {
		t.Fatal("a failing undo should be reported")
	}
	want := []string{"last", "broken", "first"}
	if len(order) != len(want) {
		t.Fatalf("undo order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("undo order = %v, want %v", order, want)
		}
	}
	if len(session.Applied()) != 0 {
		t.Fatal("a finished session should hold no undos")
	}
}

func TestLeaveOnNilSessionIsSafe(t *testing.T) {
	var session *Session
	if err := session.Leave(context.Background(), Env{}); err != nil {
		t.Fatalf("leaving a session that never started: %v", err)
	}
	if session.Applied() != nil {
		t.Fatal("a nil session has applied nothing")
	}
}
