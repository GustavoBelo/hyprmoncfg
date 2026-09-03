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

// gamescopeEntry and desktopEntry are the two session files every check in this
// file turns on. The gamescope session is recognised by the DesktopNames it
// declares, never by a package name or a path, so that is what has to be right.
const (
	gamescopeEntry = `[Desktop Entry]
Name=Gamescope
Exec=/usr/bin/gamescope-session
DesktopNames=gamescope
Type=Application
`
	desktopEntry = `[Desktop Entry]
Name=Hyprland
Exec=Hyprland
DesktopNames=Hyprland
Type=Application
`
)

// sessionFixture points session discovery at a directory of .desktop files the
// test wrote, so what the doctor sees does not depend on what happens to be
// installed on the machine running it. Two of the real directories are absolute
// system paths, and this machine has sessions in them.
//
// Called with no files it is a machine with no sessions at all, which is what
// the refusal paths need.
func sessionFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", t.TempDir())
	original := console.SessionRoots
	console.SessionRoots = []string{dir}
	t.Cleanup(func() { console.SessionRoots = original })
	return dir
}

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

// runConsoleCmd executes one console subcommand against a config directory the
// test owns, and hands back whatever it printed.
func runConsoleCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	// Never nil: cobra reads os.Args when it is, which under `go test` means the
	// command is handed -test.run and friends.
	if args == nil {
		args = []string{}
	}
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// consoleEnv is the shape every command test starts from: a runtime directory
// with no hosting marker in it, and no sessions installed anywhere the code can
// see. Both are what makes these tests answer the same on any machine.
func consoleEnv(t *testing.T, sessions map[string]string) (configDir, runtimeDir string) {
	t.Helper()
	runtimeDir = t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	sessionFixture(t, sessions)
	return t.TempDir(), runtimeDir
}

// Every line here is a question the user asked, so every line has to have an
// answer -- an empty value reads as a broken program, not as an unset setting.
func TestConsoleStatusOnAFreshMachine(t *testing.T) {
	dir, _ := consoleEnv(t, nil)

	got, err := runConsoleCmd(t, newConsoleStatusCmd(&dir))
	if err != nil {
		t.Fatalf("console status: %v", err)
	}

	for _, want := range []string{"Starts in:", "TV display:      not set", "Desktop session: not set"} {
		if !strings.Contains(got, want) {
			t.Errorf("status did not report %q:\n%s", want, got)
		}
	}
}

func TestConsoleStatusShowsWhatWasConfigured(t *testing.T) {
	dir, _ := consoleEnv(t, nil)
	if err := console.SaveConfig(dir, console.Config{
		TVName:         "DP-1",
		DesktopSession: "hyprland.desktop",
		Boot:           console.BootConsole,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	got, err := runConsoleCmd(t, newConsoleStatusCmd(&dir))
	if err != nil {
		t.Fatalf("console status: %v", err)
	}

	for _, want := range []string{"Starts in:       console", "TV display:      DP-1", "Desktop session: hyprland.desktop"} {
		if !strings.Contains(got, want) {
			t.Errorf("status did not report %q:\n%s", want, got)
		}
	}
}

// This is the only line that says whether `console enter` will be able to come
// back. Without a hosting session there is nothing to return to, and the switch
// is one way.
func TestConsoleStatusSaysWhetherTheSessionIsHosted(t *testing.T) {
	dir, _ := consoleEnv(t, nil)

	got, err := runConsoleCmd(t, newConsoleStatusCmd(&dir))
	if err != nil {
		t.Fatalf("console status: %v", err)
	}
	if !strings.Contains(got, "Hosted session:  no") {
		t.Errorf("status did not say the session is unhosted:\n%s", got)
	}
}

// Asking what the setting is must not be a way of changing it.
func TestConsoleTriggerWithoutArgumentsOnlyReports(t *testing.T) {
	dir, _ := consoleEnv(t, nil)

	got, err := runConsoleCmd(t, newConsoleTriggerCmd(&dir))
	if err != nil {
		t.Fatalf("console trigger: %v", err)
	}
	if !strings.Contains(got, "Enter on controller connect: off") {
		t.Errorf("trigger did not report the current setting:\n%s", got)
	}
	if _, err := os.Stat(console.ConfigPath(dir)); !os.IsNotExist(err) {
		t.Error("reporting the setting wrote the config file")
	}
}

// Turning this on means a controller waking up ends the desktop session with
// everything open on it. This message is the only warning the user gets before
// that happens, so it has to say the cost and how to call it off.
func TestConsoleTriggerOnSaysWhatItCosts(t *testing.T) {
	dir, _ := consoleEnv(t, nil)

	got, err := runConsoleCmd(t, newConsoleTriggerCmd(&dir), "on")
	if err != nil {
		t.Fatalf("console trigger on: %v", err)
	}
	if !strings.Contains(got, "close your desktop session") {
		t.Errorf("trigger on did not say what it costs:\n%s", got)
	}
	if !strings.Contains(got, "console cancel") {
		t.Errorf("trigger on did not say how to call it off:\n%s", got)
	}

	cfg, err := console.LoadConfig(dir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.EnterOnControllerConnect {
		t.Error("trigger on did not persist")
	}
}

func TestConsoleTriggerOffPersists(t *testing.T) {
	dir, _ := consoleEnv(t, nil)
	if err := console.SaveConfig(dir, console.Config{EnterOnControllerConnect: true}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if _, err := runConsoleCmd(t, newConsoleTriggerCmd(&dir), "off"); err != nil {
		t.Fatalf("console trigger off: %v", err)
	}

	cfg, err := console.LoadConfig(dir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.EnterOnControllerConnect {
		t.Error("trigger off did not persist")
	}
}

// BUG (cmd/hyprmoncfg/console.go:541-542, :556): the command declares ValidArgs
// but sets Args as well, and cobra only enforces ValidArgs through
// OnlyValidArgs. So a word that is neither "on" nor "off" falls through to
// `args[0] == "on"`, silently turns the trigger off, and confirms an action
// nobody asked for. This test pins the behaviour as it is today; see the commit
// that replaces it with TestConsoleTriggerRejectsAnythingElse.
func TestConsoleTriggerTreatsAnUnknownWordAsOff(t *testing.T) {
	dir, _ := consoleEnv(t, nil)
	if err := console.SaveConfig(dir, console.Config{EnterOnControllerConnect: true}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	got, err := runConsoleCmd(t, newConsoleTriggerCmd(&dir), "maybe")
	if err != nil {
		t.Fatalf("console trigger maybe: %v", err)
	}
	if !strings.Contains(got, "no longer start a console session") {
		t.Errorf("expected the current, wrong behaviour:\n%s", got)
	}

	cfg, err := console.LoadConfig(dir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.EnterOnControllerConnect {
		t.Error("the bug this pins has changed shape; rewrite the test")
	}
}

// The three modes are listed on every report, because the point of asking is
// usually to find out what else there is.
func TestConsoleBootWithoutArgumentsListsTheChoices(t *testing.T) {
	dir, _ := consoleEnv(t, nil)

	got, err := runConsoleCmd(t, newConsoleBootCmd(&dir))
	if err != nil {
		t.Fatalf("console boot: %v", err)
	}
	for _, want := range []string{"Starts in:", "desktop  always the desktop", "console  always the console", "last     wherever the last session ended"} {
		if !strings.Contains(got, want) {
			t.Errorf("boot did not list %q:\n%s", want, got)
		}
	}
	if _, err := os.Stat(console.ConfigPath(dir)); !os.IsNotExist(err) {
		t.Error("reporting the setting wrote the config file")
	}
}

// BUG (internal/console/config.go:44-50): LoadConfig returns a bare Config{}
// when the file does not exist, skipping the Normalize() that every other path
// runs -- so Boot is "" instead of "desktop". The machine still boots to the
// desktop; it just declines to say so, and only on a machine that has never
// configured console mode. Both `console boot` and `console status` print the
// blank. Saving anything at all repairs it, because SaveConfig normalizes.
func TestConsoleBootLeavesTheModeBlankOnAFreshMachine(t *testing.T) {
	dir, _ := consoleEnv(t, nil)

	fresh, err := runConsoleCmd(t, newConsoleBootCmd(&dir))
	if err != nil {
		t.Fatalf("console boot: %v", err)
	}
	if !strings.Contains(fresh, "Starts in: \n") {
		t.Errorf("the bug this pins has changed shape; rewrite the test:\n%s", fresh)
	}

	if err := console.SaveConfig(dir, console.Config{}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	saved, err := runConsoleCmd(t, newConsoleBootCmd(&dir))
	if err != nil {
		t.Fatalf("console boot: %v", err)
	}
	if !strings.Contains(saved, "Starts in: desktop") {
		t.Errorf("a saved config must report the mode it normalized to:\n%s", saved)
	}
}

func TestConsoleBootPersistsEveryMode(t *testing.T) {
	for _, tc := range []struct {
		arg  string
		want console.BootMode
	}{
		{"desktop", console.BootDesktop},
		{"console", console.BootConsole},
		{"last", console.BootLast},
	} {
		dir, _ := consoleEnv(t, nil)
		if _, err := runConsoleCmd(t, newConsoleBootCmd(&dir), tc.arg); err != nil {
			t.Fatalf("console boot %s: %v", tc.arg, err)
		}
		cfg, err := console.LoadConfig(dir)
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if cfg.Boot != tc.want {
			t.Errorf("console boot %s recorded %q, want %q", tc.arg, cfg.Boot, tc.want)
		}
	}
}

// Unlike trigger, this one validates: a machine that starts somewhere the user
// did not name is a machine that will not present a desktop.
func TestConsoleBootRejectsAnUnknownMode(t *testing.T) {
	dir, _ := consoleEnv(t, nil)

	_, err := runConsoleCmd(t, newConsoleBootCmd(&dir), "maybe")
	if err == nil {
		t.Fatal("console boot maybe was accepted")
	}
	if !strings.Contains(err.Error(), "unknown boot mode") {
		t.Errorf("error = %q, want it to name the problem", err)
	}
	if _, statErr := os.Stat(console.ConfigPath(dir)); !os.IsNotExist(statErr) {
		t.Error("a rejected mode still wrote the config file")
	}
}

// With no daemon to ask, the honest move is to leave the request behind for a
// countdown that may be running in some other process. Claiming there is
// nothing to call off would throw away a legitimate cancel.
func TestConsoleCancelWithoutADaemonLeavesTheRequest(t *testing.T) {
	_, runtimeDir := consoleEnv(t, nil)

	got, err := runConsoleCmd(t, newConsoleCancelCmd())
	if err != nil {
		t.Fatalf("console cancel: %v", err)
	}
	if !strings.Contains(got, "The desktop will stay.") {
		t.Errorf("cancel did not say the desktop stays:\n%s", got)
	}
	if _, err := os.Stat(console.CancelPath(runtimeDir)); err != nil {
		t.Errorf("cancel left no request behind: %v", err)
	}
}
