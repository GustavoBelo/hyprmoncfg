package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveFillsWhatIsMissing(t *testing.T) {
	resolved := Resolve(map[string]string{"PATH": "/usr/bin"}, Facts{
		Instance: "sig",
		WLSocket: "wayland-1",
		XDisplay: ":0",
		PathDirs: []string{"/usr/share/omarchy/bin"},
	})

	want := map[string]string{
		"HYPRLAND_INSTANCE_SIGNATURE": "sig",
		"WAYLAND_DISPLAY":             "wayland-1",
		"DISPLAY":                     ":0",
		"XDG_CURRENT_DESKTOP":         "Hyprland",
		"PATH":                        "/usr/bin:/usr/share/omarchy/bin",
	}
	for key, value := range want {
		if resolved[key] != value {
			t.Errorf("%s = %q, want %q", key, resolved[key], value)
		}
	}
}

// A daemon launched from a working session already has the right answers, and
// discovery must not talk over them.
func TestResolveNeverOverridesWhatIsSet(t *testing.T) {
	env := map[string]string{
		"HYPRLAND_INSTANCE_SIGNATURE": "mine",
		"WAYLAND_DISPLAY":             "wayland-9",
		"DISPLAY":                     ":3",
		"XDG_CURRENT_DESKTOP":         "Sway",
		"PATH":                        "/usr/bin",
	}
	resolved := Resolve(env, Facts{Instance: "other", WLSocket: "wayland-1", XDisplay: ":0"})

	for _, key := range []string{"HYPRLAND_INSTANCE_SIGNATURE", "WAYLAND_DISPLAY", "DISPLAY", "XDG_CURRENT_DESKTOP"} {
		if _, ok := resolved[key]; ok {
			t.Errorf("%s was overridden with %q", key, resolved[key])
		}
	}
}

// An empty fact is "not found", not "set this to nothing".
func TestResolveSkipsEmptyFacts(t *testing.T) {
	resolved := Resolve(map[string]string{}, Facts{Instance: "sig"})

	if _, ok := resolved["WAYLAND_DISPLAY"]; ok {
		t.Error("WAYLAND_DISPLAY was set from an empty fact")
	}
	if resolved["HYPRLAND_INSTANCE_SIGNATURE"] != "sig" {
		t.Errorf("instance = %q, want sig", resolved["HYPRLAND_INSTANCE_SIGNATURE"])
	}
}

func TestExtendPathDoesNotDuplicate(t *testing.T) {
	got, changed := extendPath("/usr/bin:/usr/share/omarchy/bin", []string{"/usr/share/omarchy/bin"})
	if changed {
		t.Errorf("PATH reported as changed: %q", got)
	}

	got, changed = extendPath("/usr/bin", []string{"/a", "/a", "/b"})
	if !changed || got != "/usr/bin:/a:/b" {
		t.Errorf("PATH = %q changed=%v, want /usr/bin:/a:/b true", got, changed)
	}
}

// Appending rather than prepending keeps a system install ahead of a stale
// copy in ~/.local/bin -- the two-binaries problem this repo has hit before.
func TestExtendPathAppends(t *testing.T) {
	got, _ := extendPath("/usr/bin", []string{"/home/u/.local/bin"})
	if !strings.HasPrefix(got, "/usr/bin:") {
		t.Errorf("PATH = %q, want the existing entries first", got)
	}
}

func TestDiscoverWaylandSocket(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "wayland-1"))
	writeFile(t, filepath.Join(dir, "wayland-1.lock"))

	if got := DiscoverWaylandSocket(dir); got != "wayland-1" {
		t.Errorf("socket = %q, want wayland-1", got)
	}

	// Two compositors: guessing would send the session to the wrong screen.
	writeFile(t, filepath.Join(dir, "wayland-2"))
	if got := DiscoverWaylandSocket(dir); got != "" {
		t.Errorf("socket = %q, want empty when ambiguous", got)
	}
}

func TestDiscoverXDisplay(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "X0"))
	if got := DiscoverXDisplay(dir); got != ":0" {
		t.Errorf("display = %q, want :0", got)
	}

	writeFile(t, filepath.Join(dir, "X1"))
	if got := DiscoverXDisplay(dir); got != "" {
		t.Errorf("display = %q, want empty when ambiguous", got)
	}
}

func TestDiscoverXDisplayIgnoresNonDisplaySockets(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "X0"))
	writeFile(t, filepath.Join(dir, "Xwhatever"))
	if got := DiscoverXDisplay(dir); got != ":0" {
		t.Errorf("display = %q, want :0", got)
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The built-in list names one distribution's script directory, which is fine
// where it exists and wrong to impose. Anyone whose desktop keeps its helpers
// elsewhere replaces the whole list.
func TestDiscoverPathDirsHonoursTheConfiguredList(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	t.Setenv(PathDirsEnv, first+string(os.PathListSeparator)+second)

	dirs := DiscoverPathDirs()

	if len(dirs) != 2 || dirs[0] != first || dirs[1] != second {
		t.Fatalf("DiscoverPathDirs = %v, want exactly the configured list in order", dirs)
	}
}

// A directory that is not there is not worth putting on PATH, configured or
// not, and an empty entry from a trailing separator is not a directory.
func TestDiscoverPathDirsSkipsWhatIsNotThere(t *testing.T) {
	real := t.TempDir()
	t.Setenv(PathDirsEnv, real+string(os.PathListSeparator)+"/nonexistent-"+t.Name()+string(os.PathListSeparator))

	dirs := DiscoverPathDirs()

	if len(dirs) != 1 || dirs[0] != real {
		t.Fatalf("DiscoverPathDirs = %v, want only the directory that exists", dirs)
	}
}

// An unset variable leaves the built-in list in charge, which is what every
// machine that has never heard of this variable relies on.
func TestDiscoverPathDirsFallsBackToTheBuiltInList(t *testing.T) {
	t.Setenv(PathDirsEnv, "")

	// Whatever this machine has, every entry must come from the built-in list.
	home, _ := os.UserHomeDir()
	allowed := map[string]bool{}
	for _, dir := range candidatePathDirs {
		if strings.HasPrefix(dir, "~/") && home != "" {
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~/"))
		}
		allowed[dir] = true
	}
	for _, got := range DiscoverPathDirs() {
		if !allowed[got] {
			t.Errorf("DiscoverPathDirs returned %q, which is not in the built-in list", got)
		}
	}
}
