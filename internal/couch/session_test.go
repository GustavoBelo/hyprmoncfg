package couch

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSessionRoundTripAndRunning(t *testing.T) {
	dir := t.TempDir()

	if _, err := ReadSession(dir); !errors.Is(err, ErrNoSession) {
		t.Fatalf("want ErrNoSession, got %v", err)
	}

	session := Session{PID: 1234567890, Phase: "playing", StartedAt: time.Now()}
	if err := WriteSession(dir, session); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	got, err := ReadSession(dir)
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if got.PID != session.PID || got.Phase != session.Phase {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if _, running := RunningSession(dir); running {
		t.Fatal("dead PID must not report as running")
	}

	ClearSession(dir)
	if _, err := ReadSession(dir); !errors.Is(err, ErrNoSession) {
		t.Fatalf("want ErrNoSession after clear, got %v", err)
	}
}

func TestProcessAliveSelf(t *testing.T) {
	if !ProcessAlive(os.Getpid()) {
		t.Fatal("own process must be alive")
	}
	if ProcessAlive(0) || ProcessAlive(-1) {
		t.Fatal("invalid PIDs must not be alive")
	}
}

func TestAppendLogAndTail(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < logTailLines+5; i++ {
		AppendLog(dir, "line %d", i)
	}
	lines := LogTail(dir, logTailLines)
	if len(lines) != logTailLines {
		t.Fatalf("tail = %d lines, want %d", len(lines), logTailLines)
	}
	last := lines[len(lines)-1]
	if want := "line " + strconv.Itoa(logTailLines+4); !strings.Contains(last, want) {
		t.Fatalf("last line %q missing %q", last, want)
	}
}
