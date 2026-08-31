package apps

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/crmne/hyprmoncfg/internal/hypr"
)

type fakeCloser struct {
	mu           sync.Mutex
	windows      []hypr.Window
	dispatch     []string
	closeRemoves bool
}

func (f *fakeCloser) Clients(context.Context) ([]hypr.Window, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]hypr.Window, len(f.windows))
	copy(out, f.windows)
	return out, nil
}

func (f *fakeCloser) Monitors(ctx context.Context) ([]hypr.Monitor, error) {
	return nil, nil
}

func (f *fakeCloser) CloseWindow(_ context.Context, address string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dispatch = append(f.dispatch, "closewindow address:"+address)
	if f.closeRemoves {
		kept := f.windows[:0]
		for _, w := range f.windows {
			if w.Address != address {
				kept = append(kept, w)
			}
		}
		f.windows = kept
	}
	return nil
}

func (f *fakeCloser) SetWindowFullscreen(_ context.Context, address string, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dispatch = append(f.dispatch, fmt.Sprintf("fullscreen %t address:%s", on, address))
	for i := range f.windows {
		if f.windows[i].Address == address {
			if on {
				f.windows[i].Fullscreen = 2
			} else {
				f.windows[i].Fullscreen = 0
			}
		}
	}
	return nil
}

func (f *fakeCloser) SetWindowTiled(_ context.Context, address string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dispatch = append(f.dispatch, "settiled address:"+address)
	for i := range f.windows {
		if f.windows[i].Address == address {
			f.windows[i].Floating = false
		}
	}
	return nil
}

func (f *fakeCloser) dispatched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.dispatch))
	copy(out, f.dispatch)
	return out
}

func spawnSleeper(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn sleeper: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd.Process.Pid
}

func TestCloseTrackedAppsNeverTargetsProtectedOrSelf(t *testing.T) {
	client := &fakeCloser{}
	if killed := CloseTrackedApps(context.Background(), client, []string{"hyprland", "steam"}); killed.Matched() {
		t.Fatalf("protected processes must never be touched, got %+v", killed)
	}
	if got := client.dispatched(); len(got) != 0 {
		t.Fatalf("no window should be closed for protected apps, got %v", got)
	}
}

// processGone reports whether a pid stopped running, reaping zombie children
// spawned by the test itself.
func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
		return true
	}
	var status syscall.WaitStatus
	reaped, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	return err == nil && reaped == pid
}

// Chromium PWAs report a generic comm, so they are found through Hyprland
// windows and closed via closewindow.
// A Chromium PWA reports a generic comm, so its window class is the handle.
// On this host that class is "chrome-web.whatsapp.com__-Default"; the title is
// "web.whatsapp.com" and is never what selects it.
func TestCloseTrackedAppsClosesWindowedPWA(t *testing.T) {
	client := &fakeCloser{windows: []hypr.Window{
		{Address: "0xaa", Class: "chrome-web.whatsapp.com__-Default", Title: "web.whatsapp.com", Pid: 12345},
	}, closeRemoves: true}
	oldDelay := closeAppsEscalationDelay
	closeAppsEscalationDelay = time.Millisecond
	defer func() { closeAppsEscalationDelay = oldDelay }()

	killed := CloseTrackedApps(context.Background(), client, []string{"chrome-web.whatsapp.com__-Default"})
	dispatch := client.dispatched()

	want := "closewindow address:0xaa"
	found := false
	for _, d := range dispatch {
		if d == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected dispatch %q, got %v", want, dispatch)
	}
	if len(killed.Signalled) != 0 {
		t.Fatalf("graceful close should not escalate, killed %v", killed.Signalled)
	}
}

// A window that survives the graceful close gets SIGTERM on its PID.
func TestCloseTrackedAppsEscalatesToSIGTERM(t *testing.T) {
	pid := spawnSleeper(t)
	oldDelay := closeAppsEscalationDelay
	closeAppsEscalationDelay = time.Millisecond
	defer func() { closeAppsEscalationDelay = oldDelay }()

	client := &fakeCloser{windows: []hypr.Window{
		{Address: "0xbb", Class: "chrome-web.whatsapp.com__-Default", Title: "web.whatsapp.com", Pid: pid},
	}}
	// Matching is case-insensitive, but on the class -- not the title.
	killed := CloseTrackedApps(context.Background(), client, []string{"Chrome-Web.WhatsApp.com__-Default"})
	if len(killed.Signalled) != 1 || killed.Signalled[0] != pid {
		t.Fatalf("expected SIGTERM escalation on PID %d, got %v", pid, killed.Signalled)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if processGone(pid) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sleeper PID %d still alive after SIGTERM", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A gracefully closed window that disappears must not be signalled.
func TestCloseTrackedAppsNoEscalationWhenWindowGone(t *testing.T) {
	pid := spawnSleeper(t)
	oldDelay := closeAppsEscalationDelay
	closeAppsEscalationDelay = time.Millisecond
	defer func() { closeAppsEscalationDelay = oldDelay }()

	client := &fakeCloser{windows: []hypr.Window{
		{Address: "0xcc", Class: "chrome-web.whatsapp.com__-Default", Title: "web.whatsapp.com", Pid: pid},
	}, closeRemoves: true}
	killed := CloseTrackedApps(context.Background(), client, []string{"chrome-web.whatsapp.com__-Default"})
	if len(client.dispatched()) == 0 {
		t.Fatal("the window should have been asked to close")
	}
	if len(killed.Signalled) != 0 {
		t.Fatalf("closed windows must not be signalled, killed %v", killed.Signalled)
	}
	if syscall.Kill(pid, 0) != nil {
		t.Fatalf("sleeper PID %d should still be alive", pid)
	}
}

// The /proc comm sweep must ignore unrelated processes.
func TestCloseTrackedAppsCommSweepIgnoresUnrelated(t *testing.T) {
	spawnSleeper(t)
	killed := CloseTrackedApps(context.Background(), &fakeCloser{}, []string{"definitely-not-running-app"})
	if len(killed.Signalled) != 0 {
		t.Fatalf("unrelated processes must survive, killed %v", killed)
	}
}

// A close-list entry may be a process name or a Hyprland window class. comm
// tops out at 15 characters but a class does not -- Chromium PWAs look like
// "chrome-web.whatsapp.com__-Default" -- so the old 15-character cap rejected
// exactly the targets that need the window path.
func TestSanitizeAppsKeepsWindowClassesAndDropsJunk(t *testing.T) {
	got := SanitizeApps([]string{
		"ok-name",
		"with space",
		"",
		"a.b_c-d",
		"chrome-web.whatsapp.com__-Default",
		"UPPER",
		strings.Repeat("x", maxTargetNameLength+1),
	})
	want := map[string]bool{
		"ok-name":                           true,
		"a.b_c-d":                           true,
		"chrome-web.whatsapp.com__-Default": true,
		"UPPER":                             true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, name := range got {
		if !want[name] {
			t.Fatalf("unexpected app %q in %v", name, got)
		}
	}
}

// The 32-process kill in the session log came from asking whether the
// lowercased "class title" string contained a target. Titles are user content;
// they must never select a kill target.
func TestWindowMatchingIsExactAndIgnoresTitles(t *testing.T) {
	targets := map[string]struct{}{"chromium": {}, "retroarch": {}}

	cases := []struct {
		name string
		win  hypr.Window
		want bool
	}{
		{"exact class", hypr.Window{Class: "chromium"}, true},
		{"exact initial class", hypr.Window{InitialClass: "RetroArch"}, true},
		{"title mentioning a target", hypr.Window{Class: "foot", Title: "vim retroarch.cfg"}, false},
		{"class containing a target", hypr.Window{Class: "chromium-browser"}, false},
		{"unrelated", hypr.Window{Class: "code", Title: "Visual Studio Code"}, false},
		{"empty", hypr.Window{}, false},
	}
	for _, tc := range cases {
		if got := windowMatchesTarget(tc.win, targets); got != tc.want {
			t.Fatalf("%s: windowMatchesTarget = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A desktop entry's Exec often starts with something that is not the app.
func TestExecProcessNameStepsOverEnvAndPaths(t *testing.T) {
	cases := map[string]string{
		"chromium --new-window":                     "chromium",
		"/usr/bin/gnome-disks":                      "gnome-disks",
		"env GDK_SCALE=2 inkscape %F":               "inkscape",
		"LC_ALL=C code --unity-launch %F":           "code",
		`"/opt/app/bin/thing" --flag`:               "thing",
		"omarchy-launch-webapp https://discord.com": "omarchy-launch-webapp",
		"": "",
	}
	for line, want := range cases {
		if got := execProcessName(line); got != want {
			t.Fatalf("execProcessName(%q) = %q, want %q", line, got, want)
		}
	}
}

// Offering a wrapper as a close target is worse than offering nothing: closing
// "flatpak" or "xdg-terminal-exec" reaches whatever else is using them. The
// real name only exists once the app runs, which is what the open-windows list
// is for.
func TestLauncherCommandsAreNeverOfferedAsTargets(t *testing.T) {
	for _, wrapper := range []string{
		"flatpak", "snap", "env", "sh", "gtk-launch",
		"xdg-terminal-exec", "omarchy-launch-webapp", "steam", "gamemoderun",
	} {
		if !isLauncherCommand(wrapper) {
			t.Fatalf("%q should be treated as a launcher", wrapper)
		}
	}
	for _, real := range []string{"chromium", "code", "retroarch", "gnome-disks"} {
		if isLauncherCommand(real) {
			t.Fatalf("%q is a real application, not a launcher", real)
		}
	}
}

func TestSuggestCloseableAppsExcludesLaunchers(t *testing.T) {
	for _, app := range SuggestCloseableApps() {
		if isLauncherCommand(app.Exec) {
			t.Fatalf("launcher %q was suggested (from %q)", app.Exec, app.Name)
		}
	}
}

// A window that closes politely needs no signal. Reporting only the killed
// PIDs made every successful close read as "no running process matched" -- a
// real session logged exactly that while it was in fact closing the window.
func TestCloseResultTellsSuccessApartFromNoMatch(t *testing.T) {
	client := &fakeCloser{windows: []hypr.Window{
		{Address: "0xaa", Class: "chrome-web.whatsapp.com__-Default", Title: "web.whatsapp.com", Pid: 12345},
	}, closeRemoves: true}
	oldDelay := closeAppsEscalationDelay
	closeAppsEscalationDelay = time.Millisecond
	defer func() { closeAppsEscalationDelay = oldDelay }()

	got := CloseTrackedApps(context.Background(), client, []string{"chrome-web.whatsapp.com__-Default"})
	if !got.Matched() {
		t.Fatal("closing a window is a match, not a miss")
	}
	if got.ClosedWindows != 1 {
		t.Fatalf("expected one window closed, got %d", got.ClosedWindows)
	}
	if len(got.Signalled) != 0 {
		t.Fatalf("a polite close needs no signal, got %v", got.Signalled)
	}

	miss := CloseTrackedApps(context.Background(), &fakeCloser{}, []string{"definitely-not-running"})
	if miss.Matched() {
		t.Fatal("nothing was running; that is a miss")
	}
}

func TestDescribeCloseResultReadsCorrectly(t *testing.T) {
	cases := []struct {
		result CloseResult
		want   string
	}{
		{CloseResult{}, "nothing on the close list is running"},
		{CloseResult{ClosedWindows: 2}, "closed 2 window(s)"},
		{CloseResult{Signalled: []int{42}}, "signalled [42]"},
		{CloseResult{ClosedWindows: 1, Signalled: []int{42}}, "closed 1 window(s) and signalled [42]"},
	}
	for _, tc := range cases {
		if got := DescribeCloseResult(tc.result, []string{"app"}); !strings.Contains(got, tc.want) {
			t.Fatalf("DescribeCloseResult(%+v) = %q, want it to contain %q", tc.result, got, tc.want)
		}
	}
}
