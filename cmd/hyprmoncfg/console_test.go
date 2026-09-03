package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/crmne/hyprmoncfg/internal/console"
)

// The status line is the only place a user sees whether a controller will close
// their desktop. "true" and "false" read as debug output; "on" and "off" read as
// a setting.
func TestOnOffReadsAsASetting(t *testing.T) {
	if got := onOff(true); got != "on" {
		t.Errorf("onOff(true) = %q, want %q", got, "on")
	}
	if got := onOff(false); got != "off" {
		t.Errorf("onOff(false) = %q, want %q", got, "off")
	}
}

// A blank status line is indistinguishable from a broken one. The user has to
// see that *nothing* was chosen, not an empty space where a display should be.
func TestOrNoneNamesTheAbsence(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "not set"},
		{"spaces only", "   ", "not set"},
		{"whitespace only", "\t\n", "not set"},
		{"a connector", "DP-1", "DP-1"},
		{"a session file", "hyprland.desktop", "hyprland.desktop"},
	} {
		if got := orNone(tc.in); got != tc.want {
			t.Errorf("%s: orNone(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// orNone trims only to decide, never to answer: what it is handed came out of
// the config file, and a status line that quietly tidies it up would hide the
// stray space that is the reason the connector is not being matched.
func TestOrNoneDoesNotTrimWhatItReturns(t *testing.T) {
	if got := orNone("  DP-1  "); got != "  DP-1  " {
		t.Errorf("orNone(%q) = %q, want it returned unchanged", "  DP-1  ", got)
	}
}

func TestYesNoReadsAsAnAnswer(t *testing.T) {
	if got := yesNo(true); got != "yes" {
		t.Errorf("yesNo(true) = %q, want %q", got, "yes")
	}
	if got := yesNo(false); got != "no" {
		t.Errorf("yesNo(false) = %q, want %q", got, "no")
	}
}

// entryFixture is the three shapes every list of sessions has to tell apart: an
// ordinary desktop, a hosting entry flagged by the marker `console setup`
// writes, and a hosting entry recognisable only by its command line because it
// was written by hand or by an older version.
func entryFixture() []console.Entry {
	return []console.Entry{
		{Path: "/usr/share/wayland-sessions/hyprland.desktop", Name: "Hyprland", Exec: []string{"Hyprland"}},
		{Path: "/usr/share/wayland-sessions/hyprmoncfg-session.desktop", Name: "Hyprland (console switch)", Hosting: true},
		{Path: "/usr/share/wayland-sessions/handwritten.desktop", Name: "Handwritten", Exec: []string{"hyprmoncfg", "console", "session"}},
	}
}

// A hosting session offered as somewhere to come back to makes the wrapper host
// itself for ever, and the user never reaches a desktop at all.
func TestPlainEntryFilesLeavesOutHostingSessions(t *testing.T) {
	got := plainEntryFiles(entryFixture())
	want := []string{"hyprland.desktop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("plainEntryFiles = %q, want %q", got, want)
	}
}

// The contrast: when the complaint is that the configured session was not found
// at all, the list has to name every installed session, hosting ones included,
// because one of them is what the user actually typed.
func TestEntryFilesKeepsEverything(t *testing.T) {
	got := entryFiles(entryFixture())
	want := []string{"hyprland.desktop", "hyprmoncfg-session.desktop", "handwritten.desktop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("entryFiles = %q, want %q", got, want)
	}
}

// Cobra consults the root or the executed command, never an intermediate
// parent, so a flag set only on `console` leaves `console enter` printing its
// whole flag list underneath the one sentence the user has to act on.
func TestSilenceUsageTreeReachesEveryChild(t *testing.T) {
	grandchild := &cobra.Command{Use: "enter"}
	child := &cobra.Command{Use: "console"}
	child.AddCommand(grandchild)
	root := &cobra.Command{Use: "hyprmoncfg"}
	root.AddCommand(child)

	silenceUsageTree(root)

	for _, cmd := range []*cobra.Command{root, child, grandchild} {
		if !cmd.SilenceUsage {
			t.Errorf("%q was left printing its usage on failure", cmd.Use)
		}
	}
}

// A session's stderr goes wherever the login manager decided, which on SDDM is
// nowhere reachable. Without this file there is no way to find out why a session
// that lasted five seconds gave up.
func TestConsoleLoggerWritesWhereItCanBeReadLater(t *testing.T) {
	dir := t.TempDir()
	consoleLogger(dir)("hello %s", "world")

	data, err := os.ReadFile(filepath.Join(dir, "console.log"))
	if err != nil {
		t.Fatalf("read the log: %v", err)
	}
	line := strings.TrimSuffix(string(data), "\n")
	if !strings.HasSuffix(line, "hello world") {
		t.Errorf("log line = %q, want it to end with %q", line, "hello world")
	}
	stamp, _, ok := strings.Cut(line, " ")
	if !ok {
		t.Fatalf("log line = %q, want a timestamp before the message", line)
	}
	if _, err := time.Parse(time.RFC3339, stamp); err != nil {
		t.Errorf("log line starts with %q, which is not RFC3339: %v", stamp, err)
	}
}

// The file is opened O_APPEND. If it ever becomes O_TRUNC, each entry erases the
// investigation the one before it was written for.
func TestConsoleLoggerAppends(t *testing.T) {
	dir := t.TempDir()
	logf := consoleLogger(dir)
	logf("first")
	logf("second")

	data, err := os.ReadFile(filepath.Join(dir, "console.log"))
	if err != nil {
		t.Fatalf("read the log: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("log has %d lines, want 2:\n%s", len(lines), data)
	}
	if !strings.HasSuffix(lines[0], "first") || !strings.HasSuffix(lines[1], "second") {
		t.Errorf("log is out of order:\n%s", data)
	}
}

// A command that writes to the process's own stdout cannot be tested, and
// cannot be redirected by whoever called it. Everything below this line depends
// on the output arriving through the command.
func TestConsoleStatusWritesThroughTheCommand(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	dir := t.TempDir()

	cmd := newConsoleStatusCmd(&dir)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("console status: %v", err)
	}

	if !strings.Contains(out.String(), "Starts in:") {
		t.Fatalf("nothing was captured through the command:\n%s", out.String())
	}
}

// A logger that blows up takes the whole session down instead of recording the
// problem it exists to record.
func TestConsoleLoggerStillLogsWhenTheFileCannotBeOpened(t *testing.T) {
	logf := consoleLogger(filepath.Join(t.TempDir(), "does-not-exist"))
	if logf == nil {
		t.Fatal("consoleLogger returned nothing to log with")
	}
	logf("the session gave up: %v", "no gamescope")
}
