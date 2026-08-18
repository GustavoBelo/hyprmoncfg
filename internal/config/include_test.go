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
