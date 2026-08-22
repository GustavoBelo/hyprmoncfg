package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "hyprland.lua")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func readConfig(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	return string(content)
}

func TestIncludeLineStaysShortWhenTheConfigHomeIsTheUsualOne(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/someone")

	lua := IncludeLine(HyprConfigLua, "/home/someone/.config/hypr/hyprmoncfg-monitors.lua")
	if lua != `dofile(os.getenv("HOME") .. "/.config/hypr/hyprmoncfg-monitors.lua")` {
		t.Fatalf("expected the short form, got %q", lua)
	}
}

func TestIncludeLineResolvesTheHomeAtLoadTime(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/home/someone/.config")

	lua := IncludeLine(HyprConfigLua, "/home/someone/.config/hypr/hyprmoncfg-monitors.lua")
	if strings.Contains(lua, "/home/someone") {
		t.Fatalf("expected no machine-specific prefix in %q", lua)
	}
	for _, want := range []string{"dofile(", "XDG_CONFIG_HOME", "HOME", "/hypr/hyprmoncfg-monitors.lua"} {
		if !strings.Contains(lua, want) {
			t.Fatalf("expected %q in %q", want, lua)
		}
	}
	if strings.Contains(lua, "require(") {
		t.Fatalf("require depends on package.path and caches, expected dofile in %q", lua)
	}

	legacy := IncludeLine(HyprConfigLegacy, "/home/someone/.config/hypr/hyprmoncfg-monitors.conf")
	if legacy != "source = ~/.config/hypr/hyprmoncfg-monitors.conf" {
		t.Fatalf("legacy include = %q", legacy)
	}

	// A target the user pointed somewhere else has to be named outright.
	outside := IncludeLine(HyprConfigLua, "/etc/hypr/custom.lua")
	if outside != `dofile("/etc/hypr/custom.lua")` {
		t.Fatalf("outside include = %q", outside)
	}
}

func TestEnsureIncludedAppendsOnceAndStaysPut(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/home/someone/.config")
	const original = "require(\"default.hypr.omarchy\")\nrequire(\"hypr.monitors\")\nrequire(\"default.hypr.toggles\")\n"
	path := writeConfig(t, original)
	target := "/home/someone/.config/hypr/hyprmoncfg-monitors.lua"

	first, err := EnsureIncluded(path, HyprConfigLua, target)
	if err != nil {
		t.Fatalf("EnsureIncluded: %v", err)
	}
	if !first.Added || first.MovedLast {
		t.Fatalf("expected the include to be added, got %+v", first)
	}

	content := readConfig(t, path)
	if !strings.HasPrefix(content, original) {
		t.Fatalf("expected every original line to survive, got:\n%s", content)
	}
	if !strings.HasSuffix(content, first.Line+"\n") {
		t.Fatalf("expected the include to be last, got:\n%s", content)
	}

	// Starting the daemon again, or applying again, must not keep rewriting.
	second, err := EnsureIncluded(path, HyprConfigLua, target)
	if err != nil {
		t.Fatalf("second EnsureIncluded: %v", err)
	}
	if second.Changed() {
		t.Fatalf("expected a settled config to be left alone, got %+v", second)
	}
	if readConfig(t, path) != content {
		t.Fatal("expected the settled config to be byte for byte the same")
	}
}

func TestEnsureIncludedMovesItselfBackToTheEnd(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/home/someone/.config")
	target := "/home/someone/.config/hypr/hyprmoncfg-monitors.lua"
	line := IncludeLine(HyprConfigLua, target)

	// Something was appended after our include, so its rules would win.
	path := writeConfig(t, "require(\"hypr.monitors\")\n"+line+"\nrequire(\"default.hypr.toggles\")\n")

	result, err := EnsureIncluded(path, HyprConfigLua, target)
	if err != nil {
		t.Fatalf("EnsureIncluded: %v", err)
	}
	if result.Added || !result.MovedLast {
		t.Fatalf("expected the include to be moved rather than added, got %+v", result)
	}

	content := readConfig(t, path)
	if strings.Count(content, line) != 1 {
		t.Fatalf("expected exactly one include, got:\n%s", content)
	}
	if !strings.HasSuffix(content, line+"\n") {
		t.Fatalf("expected the include to end up last, got:\n%s", content)
	}
	if !strings.Contains(content, "require(\"default.hypr.toggles\")") {
		t.Fatalf("expected the appended line to survive, got:\n%s", content)
	}
}

func TestEnsureIncludedHandlesLegacyConfigs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/home/someone/.config")
	target := "/home/someone/.config/hypr/hyprmoncfg-monitors.conf"
	path := writeConfig(t, "source = ~/.config/hypr/monitors.conf\n")

	result, err := EnsureIncluded(path, HyprConfigLegacy, target)
	if err != nil {
		t.Fatalf("EnsureIncluded: %v", err)
	}
	if !result.Added {
		t.Fatalf("expected the source line to be added, got %+v", result)
	}

	content := readConfig(t, path)
	if !strings.Contains(content, "source = ~/.config/hypr/monitors.conf") {
		t.Fatalf("expected the existing source to survive, got:\n%s", content)
	}
	if !strings.HasSuffix(content, "source = ~/.config/hypr/hyprmoncfg-monitors.conf\n") {
		t.Fatalf("expected our source to be last, got:\n%s", content)
	}
	if !strings.Contains(content, "# Added by hyprmoncfg") {
		t.Fatalf("expected a comment in the config's own syntax, got:\n%s", content)
	}
}

// A dotfile manager points ~/.config/hypr/hyprland.lua at a file it owns.
// Renaming onto the link would swap it for a plain file of ours, which is
// issue #45.
func TestEnsureIncludedWritesThroughASymlink(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "dotfiles-hyprland.lua")
	if err := os.WriteFile(source, []byte("monitor = eDP-1, preferred, auto, 1\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	link := filepath.Join(dir, "hyprland.lua")
	if err := os.Symlink(source, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := EnsureIncluded(link, HyprConfigLua, filepath.Join(dir, "hyprmoncfg-monitors.lua")); err != nil {
		t.Fatalf("EnsureIncluded: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced with a regular file")
	}
	if !strings.Contains(readConfig(t, source), "hyprmoncfg") {
		t.Fatal("the include did not reach the file the symlink points at")
	}
}

// A Nix store target never becomes writable, so every apply would otherwise
// fail on a file that is not hyprmoncfg's to change.
func TestIncludeLeavesAReadOnlyConfigAlone(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "nix-store-hyprland.lua")
	original := "monitor = eDP-1, preferred, auto, 1\n"
	if err := os.WriteFile(source, []byte(original), 0o444); err != nil {
		t.Fatalf("write source: %v", err)
	}
	link := filepath.Join(dir, "hyprland.lua")
	if err := os.Symlink(source, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	result, err := EnsureIncluded(link, HyprConfigLua, filepath.Join(dir, "hyprmoncfg-monitors.lua"))
	if err != nil {
		t.Fatalf("EnsureIncluded should not fail on a read-only config: %v", err)
	}
	if !result.ReadOnly {
		t.Fatal("expected the read-only config to be reported")
	}
	if result.Added || result.MovedLast {
		t.Fatal("nothing should have been written")
	}
	if result.Line == "" {
		t.Fatal("the user needs to be told which line to add")
	}
	if got := readConfig(t, source); got != original {
		t.Fatalf("read-only config was modified:\n%q", got)
	}
}
