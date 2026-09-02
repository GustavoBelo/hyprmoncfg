// Package session fills in the graphical-session environment that a systemd
// user service does not inherit.
//
// A daemon started as a user unit gets XDG_RUNTIME_DIR and a bare PATH, and
// nothing else: no WAYLAND_DISPLAY, no DISPLAY, no
// HYPRLAND_INSTANCE_SIGNATURE. That is enough to talk to Hyprland, because
// hyprctl can be pointed at a discovered instance, and not nearly enough for
// anything the daemon launches on the user's behalf. Steam exits with "Unable
// to open X11 display" and the Omarchy helper scripts are not even on PATH, so
// the hooks that call them report themselves unavailable and vanish from the
// session without a word.
//
// `systemctl --user import-environment` is the usual answer and a fragile one:
// it depends on the login having run it before the unit started, and when it
// has not, nothing says so. Resolving the values in process works wherever the
// daemon runs.
package session

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Facts is what discovery found about the running graphical session. An empty
// field means "not found", and is skipped rather than exported as empty.
type Facts struct {
	// Instance is the Hyprland instance signature.
	Instance string
	// WLSocket is the Wayland socket name, e.g. "wayland-1".
	WLSocket string
	// XDisplay is the X11 display XWayland listens on, e.g. ":0".
	XDisplay string
	// PathDirs are directories to add to PATH, in order, if missing.
	PathDirs []string
}

// Resolve returns the variables that env is missing and facts can supply.
//
// It never replaces a value that is already set: a daemon started from a
// working session, or one the user configured deliberately, knows more than
// discovery does. PATH is the exception, since extending it cannot invalidate
// what is already there.
func Resolve(env map[string]string, facts Facts) map[string]string {
	resolved := map[string]string{}
	fill := func(key, value string) {
		if value == "" || strings.TrimSpace(env[key]) != "" {
			return
		}
		resolved[key] = value
	}
	fill("HYPRLAND_INSTANCE_SIGNATURE", facts.Instance)
	fill("WAYLAND_DISPLAY", facts.WLSocket)
	fill("DISPLAY", facts.XDisplay)
	fill("XDG_CURRENT_DESKTOP", "Hyprland")
	if path, changed := extendPath(env["PATH"], facts.PathDirs); changed {
		resolved["PATH"] = path
	}
	return resolved
}

// extendPath appends the directories PATH does not already list, keeping the
// caller's order. Appending rather than prepending means a system installation
// still wins over a stale copy in ~/.local/bin.
func extendPath(current string, dirs []string) (string, bool) {
	if len(dirs) == 0 {
		return current, false
	}
	present := map[string]bool{}
	entries := []string{}
	if current != "" {
		entries = strings.Split(current, string(os.PathListSeparator))
		for _, entry := range entries {
			present[entry] = true
		}
	}
	changed := false
	for _, dir := range dirs {
		if dir == "" || present[dir] {
			continue
		}
		entries = append(entries, dir)
		present[dir] = true
		changed = true
	}
	if !changed {
		return current, false
	}
	return strings.Join(entries, string(os.PathListSeparator)), true
}

// Apply sets the resolved variables on this process, so both the daemon's own
// lookups and every child it spawns see them. It returns the keys it set, for
// logging: a daemon that had to invent its own session is worth a line.
func Apply(resolved map[string]string) []string {
	keys := make([]string, 0, len(resolved))
	for key, value := range resolved {
		if err := os.Setenv(key, value); err != nil {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Environ reads the current process environment into the map Resolve wants.
func Environ() map[string]string {
	env := make(map[string]string, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}
	return env
}

// candidatePathDirs are directories a login shell has and a user unit does
// not: hyprmoncfg's own binaries when installed without root, and the helper
// scripts a desktop's hooks shell out to.
//
// /usr/share/omarchy/bin is one distribution's, and it is here because it is
// where that distribution keeps the scripts this daemon calls. It costs nothing
// anywhere else -- a directory that does not exist is not added -- and
// PathDirsEnv replaces the whole list for anyone whose desktop keeps them
// somewhere else.
var candidatePathDirs = []string{
	"~/.local/bin",
	"/usr/share/omarchy/bin",
	"/usr/local/bin",
}

// PathDirsEnv names the variable that replaces the built-in list, as a
// colon-separated path. Empty entries and ~ are handled the same way.
const PathDirsEnv = "HYPRMONCFG_PATH_DIRS"

// DiscoverPathDirs returns the candidate directories that exist on this
// machine. A directory that is not there is not worth putting on PATH.
func DiscoverPathDirs() []string {
	candidates := candidatePathDirs
	if configured := strings.TrimSpace(os.Getenv(PathDirsEnv)); configured != "" {
		candidates = strings.Split(configured, string(os.PathListSeparator))
	}
	home, _ := os.UserHomeDir()
	dirs := make([]string, 0, len(candidates))
	for _, dir := range candidates {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if strings.HasPrefix(dir, "~/") {
			if home == "" {
				continue
			}
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~/"))
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// DiscoverWaylandSocket finds the Wayland socket in the runtime directory.
//
// It only answers when there is exactly one: with several compositors running,
// guessing which one the user means would send the session to the wrong screen.
func DiscoverWaylandSocket(runtimeDir string) string {
	if runtimeDir == "" {
		return ""
	}
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		return ""
	}
	found := ""
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "wayland-") || strings.HasSuffix(name, ".lock") {
			continue
		}
		if found != "" {
			return ""
		}
		found = name
	}
	return found
}

// DiscoverXDisplay finds the X11 display XWayland is serving.
//
// Steam is an X11 client, so without this the daemon launches it and it exits
// immediately with "Unable to open X11 display" -- which is exactly what every
// automatic couch trigger did.
func DiscoverXDisplay(socketDir string) string {
	if socketDir == "" {
		socketDir = "/tmp/.X11-unix"
	}
	entries, err := os.ReadDir(socketDir)
	if err != nil {
		return ""
	}
	found := ""
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "X") {
			continue
		}
		number := strings.TrimPrefix(name, "X")
		if number == "" || strings.Trim(number, "0123456789") != "" {
			continue
		}
		if found != "" {
			return ""
		}
		found = ":" + number
	}
	return found
}

// required are the variables a program launched for the user cannot do without:
// the Wayland socket, the X11 display XWayland serves for clients like Steam,
// and the compositor instance every hyprctl call is aimed at.
var required = []string{"WAYLAND_DISPLAY", "DISPLAY", "HYPRLAND_INSTANCE_SIGNATURE"}

// Missing names the graphical-session variables this process still lacks after
// adoption, so a daemon can report the condition rather than launching things
// that exit without explaining themselves.
func Missing() []string {
	absent := []string{}
	for _, key := range required {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			absent = append(absent, key)
		}
	}
	return absent
}
