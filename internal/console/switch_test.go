package console

import (
	"context"
	"os"
	"testing"
)

// A request has to be cleared when it is acted on. One that survived would
// switch again on the next exit, and the user would be unable to log out at all.
func TestTakeRequestClearsWhatItReturns(t *testing.T) {
	dir := t.TempDir()
	if err := Request(dir, ModeConsole); err != nil {
		t.Fatal(err)
	}

	mode, ok := TakeRequest(dir)
	if !ok || mode != ModeConsole {
		t.Fatalf("TakeRequest = %q ok=%v", mode, ok)
	}
	if _, ok := TakeRequest(dir); ok {
		t.Fatal("the request survived being acted on")
	}
	if _, err := os.Stat(RequestPath(dir)); !os.IsNotExist(err) {
		t.Errorf("the request file is still there: %v", err)
	}
}

// No request means a real log out, which is what keeps an ordinary logout
// working while the wrapper is hosting the session.
func TestTakeRequestOnNothingMeansLogOut(t *testing.T) {
	if _, ok := TakeRequest(t.TempDir()); ok {
		t.Fatal("no request must not be read as a switch")
	}
}

// A truncated or hand-edited file must not be read as a switch: acting on
// garbage would start a compositor nobody asked for.
func TestTakeRequestRejectsAnythingElse(t *testing.T) {
	for _, body := range []string{"", "\n", "gamescope", "console desktop", "CONSOLE"} {
		dir := t.TempDir()
		if err := os.WriteFile(RequestPath(dir), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if mode, ok := TakeRequest(dir); ok {
			t.Errorf("TakeRequest(%q) = %q, want a refusal", body, mode)
		}
	}
}

func TestRequestRoundTripsBothModes(t *testing.T) {
	for _, want := range []Mode{ModeConsole, ModeDesktop} {
		dir := t.TempDir()
		if err := Request(dir, want); err != nil {
			t.Fatal(err)
		}
		if got, ok := TakeRequest(dir); !ok || got != want {
			t.Errorf("round trip of %q gave %q ok=%v", want, got, ok)
		}
	}
}

func TestClearRequestLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	if err := Request(dir, ModeConsole); err != nil {
		t.Fatal(err)
	}
	ClearRequest(dir)
	if _, ok := TakeRequest(dir); ok {
		t.Fatal("a cleared request was still acted on")
	}
	// Clearing nothing is not an error either.
	ClearRequest(dir)
}

// The compositor is detected by its socket rather than by a process name,
// because the wrapper hosts whichever compositor the user configured.
func TestCompositorRunningFollowsTheSocket(t *testing.T) {
	dir := t.TempDir()
	if compositorRunning(dir) {
		t.Fatal("an empty runtime directory has no compositor in it")
	}
	instance := dir + "/hypr/abc123"
	if err := os.MkdirAll(instance, 0o755); err != nil {
		t.Fatal(err)
	}
	if compositorRunning(dir) {
		t.Fatal("an instance directory without a socket is not a running compositor")
	}
	if err := os.WriteFile(instance+"/.socket.sock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !compositorRunning(dir) {
		t.Fatal("a live socket was not noticed")
	}
	if compositorGone(context.Background(), dir, 0) {
		t.Fatal("compositorGone reported a compositor that is still there")
	}
}

// The daemon that arms an automatic entry and the command that calls it off are
// different processes; the runtime directory is what they share.
func TestCancelIsSeenOnceAndOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	if TakeCancel(dir) {
		t.Fatal("nothing was cancelled, but a stand-down was reported")
	}
	if err := RequestCancel(dir); err != nil {
		t.Fatal(err)
	}
	if !TakeCancel(dir) {
		t.Fatal("a stand-down was asked for and not seen")
	}
	if TakeCancel(dir) {
		t.Fatal("the stand-down fired twice; the next entry would be cancelled too")
	}
}
