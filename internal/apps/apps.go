package apps

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/crmne/hyprmoncfg/internal/hypr"
)

// processAlive reports whether a pid is still there. EPERM counts: the process
// exists, it just is not ours to signal.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Closer is the slice of a compositor client this package needs.
//
// Named for what it means rather than exposing a raw dispatcher: Hyprland on a
// Lua config refuses the classic dispatch syntax outright, and deciding which
// form to send is the client's job, not a caller's.
type Closer interface {
	Clients(ctx context.Context) ([]hypr.Window, error)
	CloseWindow(ctx context.Context, address string) error
}

// DiscoveredApp holds information parsed from a .desktop file.
type DiscoveredApp struct {
	Name       string
	Exec       string
	Categories string
}

// DiscoverApps scans standard .desktop file directories and returns apps
// whose Exec name looks closeable (not browser-like, not a desktop shell, etc.).
func DiscoverApps() []DiscoveredApp {
	xdgDataHome := os.Getenv("XDG_DATA_HOME")
	if xdgDataHome == "" {
		xdgDataHome = filepath.Join(os.Getenv("HOME"), ".local", "share")
	}
	xdgDataDirs := os.Getenv("XDG_DATA_DIRS")
	if xdgDataDirs == "" {
		xdgDataDirs = "/usr/local/share:/usr/share"
	}

	dirs := []string{
		filepath.Join(xdgDataHome, "applications"),
	}
	for _, d := range strings.Split(xdgDataDirs, ":") {
		if d != "" {
			dirs = append(dirs, filepath.Join(d, "applications"))
		}
	}
	// Flatpak exports
	dirs = append(dirs,
		filepath.Join(os.Getenv("HOME"), ".local", "share", "flatpak", "exports", "share", "applications"),
		"/var/lib/flatpak/exports/share/applications",
		"/var/lib/snapd/desktop/applications",
	)

	seen := make(map[string]struct{})
	var results []DiscoveredApp
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".desktop") {
				continue
			}
			app := parseDesktopFile(filepath.Join(dir, entry.Name()))
			if app.Name == "" || app.SkipBoolean {
				continue
			}
			key := strings.ToLower(app.Exec)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			results = append(results, DiscoveredApp{Name: app.Name, Exec: app.Exec, Categories: app.Categories})
		}
	}
	return results
}

type desktopApp struct {
	Name        string
	Exec        string
	Categories  string
	SkipBoolean bool
}

// launcherCommands are wrappers that start something else. Their name is never
// the process a session should close: killing "flatpak" or "xdg-terminal-exec"
// reaches whatever else happens to be using them.
//
// The real name only exists once the app is running -- an Omarchy web app opens
// a Chromium window whose class is built from the URL -- so these entries are
// dropped rather than guessed at. The picker lists open windows first precisely
// because that is where the exact name comes from.
var launcherCommands = map[string]struct{}{
	"env": {}, "sh": {}, "bash": {}, "zsh": {}, "exec": {},
	"flatpak": {}, "snap": {}, "gtk-launch": {}, "dbus-launch": {},
	"xdg-open": {}, "xdg-terminal-exec": {}, "omarchy-launch-webapp": {},
	"omarchy-launch-floating-terminal-with-presentation": {}, "systemd-run": {},
	"wine": {}, "proton": {}, "steam": {}, "prime-run": {}, "gamemoderun": {},
}

func isLauncherCommand(name string) bool {
	_, ok := launcherCommands[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

func parseDesktopFile(path string) desktopApp {
	f, err := os.Open(path)
	if err != nil {
		return desktopApp{}
	}
	defer f.Close()

	var app desktopApp
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "Name=") {
			app.Name = strings.TrimPrefix(line, "Name=")
		} else if strings.HasPrefix(line, "Exec=") {
			exec := strings.TrimPrefix(line, "Exec=")
			exec = strings.Fields(exec)[0]
			app.Exec = filepath.Base(exec)
		} else if strings.HasPrefix(line, "Categories=") {
			app.Categories = strings.TrimPrefix(line, "Categories=")
		} else if strings.HasPrefix(line, "NoDisplay=true") {
			return desktopApp{SkipBoolean: true}
		}
	}
	return app
}

// execProcessName reads the process name out of a desktop entry's Exec line.
//
// It steps over an "env" prefix and any VAR=value assignments before it, which
// would otherwise be taken as the command itself.
func execProcessName(execLine string) string {
	for _, field := range strings.Fields(execLine) {
		if field == "env" || strings.Contains(field, "=") && !strings.HasPrefix(field, "/") {
			continue
		}
		return filepath.Base(strings.Trim(field, `"'`))
	}
	return ""
}

// SuggestCloseableApps filters discovered apps to those worth suggesting
// for couch mode. It excludes web browsers, desktop shells, and protected
// processes.
func SuggestCloseableApps() []DiscoveredApp {
	all := DiscoverApps()
	browsers := map[string]bool{
		"firefox": true, "chromium": true, "google-chrome": true,
		"brave": true, "vivaldi": true, "opera": true, "edge": true,
	}
	protected := make(map[string]bool)
	for k := range ProtectedProcesses {
		protected[strings.ToLower(k)] = true
	}
	var suggestions []DiscoveredApp
	for _, a := range all {
		exec := strings.ToLower(a.Exec)
		if browsers[exec] || protected[exec] || isLauncherCommand(exec) {
			continue
		}
		if strings.Contains(a.Categories, "System") || strings.Contains(a.Categories, "Settings") {
			continue
		}
		suggestions = append(suggestions, a)
	}
	return suggestions
}

// RunningApps returns processes that match the given app names and are currently running.
func RunningApps(apps []string) []RunningApp {
	targets := make(map[string]struct{}, len(apps))
	for _, app := range apps {
		targets[strings.ToLower(app)] = struct{}{}
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	var running []RunningApp
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 {
			continue
		}
		commBytes, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(commBytes))
		if _, ok := targets[strings.ToLower(name)]; ok {
			running = append(running, RunningApp{
				Name: name,
				PID:  pid,
			})
		}
	}
	return running
}

// RunningApp holds info about a running process.
type RunningApp struct {
	Name string `json:"name"`
	PID  int    `json:"pid"`
}

// closeAppsEscalationDelay gives a gracefully closed window time to disappear
// before its process receives SIGTERM.
var closeAppsEscalationDelay = 2 * time.Second

var ProtectedProcesses = map[string]struct{}{
	"Hyprland":           {},
	"hyprmoncfg":         {},
	"hyprmoncfgd":        {},
	"quickshell":         {},
	"omarchy-shell":      {},
	"waybar":             {},
	"steam":              {},
	"steamwebhelper":     {},
	"systemd":            {},
	"dbus-daemon":        {},
	"pipewire":           {},
	"pipewire-pulse":     {},
	"wireplumber":        {},
	"Xwayland":           {},
	"xdg-desktop-portal": {},
}

func SanitizeApps(apps []string) []string {
	seen := make(map[string]struct{}, len(apps))
	cleaned := make([]string, 0, len(apps))
	for _, app := range apps {
		name := strings.TrimSpace(app)
		// /proc/<pid>/comm tops out at 15 characters, but a Hyprland window
		// class does not: Chromium PWAs look like
		// "chrome-web.whatsapp.com__-Default". Both are valid targets.
		if name == "" || len(name) > maxTargetNameLength {
			continue
		}
		if !validProcessName(name) {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		cleaned = append(cleaned, name)
	}
	return cleaned
}

func validProcessName(name string) bool {
	for i := 0; i < len(name); i++ {
		b := name[i]
		switch {
		case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		case b == '.' || b == '_' || b == '-':
		default:
			return false
		}
	}
	return len(name) > 0
}

// CloseTrackedApps ends the configured apps for the rest of the play session.
// Native apps are matched by their /proc comm; windowed apps whose process
// name does not give them away (Electron and Chromium PWAs report a generic
// comm like "chromium") are found through Hyprland windows instead: first the
// window is closed gracefully, then any survivor gets SIGTERM on its PID.
// CloseResult says what actually happened, because "nothing was signalled" and
// "nothing matched" are different outcomes that used to look the same.
//
// A window that closes politely needs no signal, so reporting only the killed
// PIDs made every successful close read as "no running process matched" in the
// log -- which is exactly what a real session reported while it was in fact
// closing the window it was asked to.
type CloseResult struct {
	// ClosedWindows is how many windows were asked to close.
	ClosedWindows int
	// Signalled lists processes that had to be signalled to go away.
	Signalled []int
}

// Matched reports whether anything on the close list was found at all.
func (r CloseResult) Matched() bool {
	return r.ClosedWindows > 0 || len(r.Signalled) > 0
}

func CloseTrackedApps(ctx context.Context, client Closer, apps []string) CloseResult {
	targets := make(map[string]struct{}, len(apps))
	for _, app := range SanitizeApps(apps) {
		if _, protected := ProtectedProcesses[app]; protected {
			continue
		}
		targets[strings.ToLower(app)] = struct{}{}
	}
	if len(targets) == 0 {
		return CloseResult{}
	}

	closed, signalled := closeWindowedApps(ctx, client, targets)
	result := CloseResult{ClosedWindows: closed, Signalled: signalled}
	result.Signalled = append(result.Signalled, closeCommApps(targets)...)
	return result
}

// closeWindowedApps asks Hyprland to close windows whose class or title names
// a tracked app, then escalates to SIGTERM when a window survived the grace
// period.
func closeWindowedApps(ctx context.Context, client Closer, targets map[string]struct{}) (int, []int) {
	windows, err := client.Clients(ctx)
	if err != nil {
		return 0, nil
	}

	survivors := make([]hypr.Window, 0, 2)
	for _, w := range windows {
		if !windowMatchesTarget(w, targets) {
			continue
		}
		if w.Pid == os.Getpid() || pidProtected(w.Pid) {
			continue
		}
		if err := client.CloseWindow(ctx, w.Address); err == nil {
			survivors = append(survivors, w)
		}
	}

	closed := len(survivors)
	if closed == 0 {
		return 0, nil
	}

	select {
	case <-ctx.Done():
	case <-time.After(closeAppsEscalationDelay):
	}

	still, _ := client.Clients(ctx)
	addresses := make(map[string]struct{}, len(still))
	for _, w := range still {
		addresses[w.Address] = struct{}{}
	}

	killed := make([]int, 0, len(survivors))
	for _, w := range survivors {
		// A closed window no longer shows up in clients; only what persisted
		// after the graceful close earns a signal.
		if _, alive := addresses[w.Address]; !alive || w.Pid <= 0 {
			continue
		}
		if err := syscall.Kill(w.Pid, syscall.SIGTERM); err == nil {
			killed = append(killed, w.Pid)
		}
	}
	return closed, sigkillEscalation(killed)
}

// windowMatchesTarget decides whether a window belongs to a tracked app.
//
// Matching is exact, on identity fields only. The previous version asked
// whether the lowercased "class title" string *contained* a target, which meant
// a short or common target swept up unrelated windows by their titles: one
// session in the log closed 32 processes in a single pass. A window title is
// user content that changes every minute and must never select a kill target.
func windowMatchesTarget(w hypr.Window, targets map[string]struct{}) bool {
	for _, class := range []string{w.Class, w.InitialClass} {
		if class == "" {
			continue
		}
		if _, ok := targets[strings.ToLower(class)]; ok {
			return true
		}
	}
	// Electron apps and Chromium PWAs report a generic comm, so the class is
	// usually the only handle; but a native app named by its binary should
	// still match through its window.
	if w.Pid > 0 {
		if comm := processComm(w.Pid); comm != "" {
			if _, ok := targets[strings.ToLower(comm)]; ok {
				return true
			}
		}
	}
	return false
}

func processComm(pid int) string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func closeCommApps(targets map[string]struct{}) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	killed := make([]int, 0)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		commBytes, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(commBytes))
		if _, target := targets[strings.ToLower(name)]; !target {
			continue
		}
		if pidProtected(pid) {
			continue
		}
		if err := syscall.Kill(pid, syscall.SIGTERM); err == nil {
			killed = append(killed, pid)
		}
	}
	return sigkillEscalation(killed)
}

func pidProtected(pid int) bool {
	name := processComm(pid)
	if name == "" {
		return false
	}
	_, protected := ProtectedProcesses[name]
	return protected
}

// sigkillEscalation waits for processes to exit after SIGTERM, and sends
// SIGKILL to any survivors. Returns the list of PIDs that were escalated.
func sigkillEscalation(pids []int) []int {
	if len(pids) == 0 {
		return nil
	}
	time.Sleep(closeAppsEscalationDelay)
	escalated := make([]int, 0)
	for _, pid := range pids {
		if pid <= 0 {
			continue
		}
		if !processAlive(pid) {
			continue
		}
		if err := syscall.Kill(pid, syscall.SIGKILL); err == nil {
			escalated = append(escalated, pid)
		}
	}
	return escalated
}

// maxTargetNameLength bounds a close-list entry. It has to clear a Hyprland
// window class, which is far longer than the 15 characters /proc/<pid>/comm
// allows.
const maxTargetNameLength = 64

// DescribeCloseResult turns an outcome into the line a session logs.
func DescribeCloseResult(result CloseResult, requested []string) string {
	switch {
	case !result.Matched():
		return fmt.Sprintf("nothing on the close list is running %v", requested)
	case len(result.Signalled) == 0:
		return fmt.Sprintf("closed %d window(s) from the close list", result.ClosedWindows)
	case result.ClosedWindows == 0:
		return fmt.Sprintf("signalled %v from the close list", result.Signalled)
	default:
		return fmt.Sprintf("closed %d window(s) and signalled %v", result.ClosedWindows, result.Signalled)
	}
}
