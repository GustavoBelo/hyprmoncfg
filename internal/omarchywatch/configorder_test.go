package omarchywatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const omarchyConfig = `-- Learn how to configure Hyprland: https://wiki.hypr.land/Configuring/Start/

-- Omarchy's bootstrap keeps path setup out of this user config.
dofile((os.getenv("OMARCHY_PATH") or "/usr/share/omarchy") .. "/default/hypr/bootstrap.lua")

-- Load Omarchy defaults.
require("default.hypr.omarchy")

-- Load personal overrides after Omarchy's defaults and the current theme.
require("hypr.monitors")
require("hypr.input")
require("hypr.autostart")

-- Toggle config flags dynamically.
require("default.hypr.toggles")
`

func TestInspectConfigOrderFindsOmarchyOverridingHyprmoncfg(t *testing.T) {
	order := inspectConfigOrder(omarchyConfig)
	if !order.Applicable || !order.NeedsReorder() {
		t.Fatalf("expected the shipped Omarchy order to need a reorder, got %+v", order)
	}
}

func TestInspectConfigOrderIgnoresConfigsItDoesNotGovern(t *testing.T) {
	for name, content := range map[string]string{
		"no toggles":  "require(\"hypr.monitors\")\n",
		"no monitors": "require(\"default.hypr.toggles\")\n",
		"neither":     "-- nothing to see\n",
		"commented":   "-- require(\"hypr.monitors\")\nrequire(\"default.hypr.toggles\")\n",
	} {
		if order := inspectConfigOrder(content); order.Applicable {
			t.Fatalf("%s: expected no verdict, got %+v", name, order)
		}
	}
}

func TestReorderConfigGivesHyprmoncfgTheLastWord(t *testing.T) {
	reordered, changed := ReorderConfig(omarchyConfig)
	if !changed {
		t.Fatal("expected the shipped Omarchy order to be rewritten")
	}

	monitors := strings.Index(reordered, monitorsRequire)
	toggles := strings.Index(reordered, togglesRequire)
	if monitors < 0 || toggles < 0 || monitors < toggles {
		t.Fatalf("expected monitors to load after toggles, got:\n%s", reordered)
	}
	if strings.Count(reordered, monitorsRequire) != 1 {
		t.Fatalf("expected the require to move rather than be duplicated, got:\n%s", reordered)
	}
	for _, want := range []string{"require(\"hypr.input\")", "require(\"hypr.autostart\")", "bootstrap.lua"} {
		if !strings.Contains(reordered, want) {
			t.Fatalf("expected %q to survive the rewrite, got:\n%s", want, reordered)
		}
	}

	if !strings.HasSuffix(reordered, monitorsRequire+"\n") {
		t.Fatalf("expected the file to keep its trailing newline, got %q", reordered[max(0, len(reordered)-40):])
	}
	if strings.Contains(reordered, "\n\n\n") {
		t.Fatalf("expected the move to leave no double blank line, got:\n%s", reordered)
	}

	if order := inspectConfigOrder(reordered); order.NeedsReorder() {
		t.Fatalf("expected the rewrite to settle the order, got %+v", order)
	}
	if _, changed := ReorderConfig(reordered); changed {
		t.Fatal("expected a settled config to be left alone")
	}
}

func TestEnsureConfigOrderRewritesOnceAndKeepsTheOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hyprland.lua")
	if err := os.WriteFile(path, []byte(omarchyConfig), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	reorder, err := EnsureConfigOrder(path)
	if err != nil {
		t.Fatalf("EnsureConfigOrder: %v", err)
	}
	if !reorder.Changed || reorder.BackupPath == "" {
		t.Fatalf("expected the shipped order to be rewritten with a backup, got %+v", reorder)
	}

	backup, err := os.ReadFile(reorder.BackupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backup) != omarchyConfig {
		t.Fatal("expected the backup to hold the config as it was")
	}

	// Starting the daemon again must not keep rewriting a settled config.
	again, err := EnsureConfigOrder(path)
	if err != nil {
		t.Fatalf("second EnsureConfigOrder: %v", err)
	}
	if again.Changed {
		t.Fatal("expected a settled config to be left alone")
	}
}

func TestEnsureConfigOrderLeavesForeignConfigsAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hyprland.lua")
	const plain = "require(\"hypr.monitors\")\n"
	if err := os.WriteFile(path, []byte(plain), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	reorder, err := EnsureConfigOrder(path)
	if err != nil {
		t.Fatalf("EnsureConfigOrder: %v", err)
	}
	if reorder.Changed {
		t.Fatal("expected a config without Omarchy's toggles to be left alone")
	}
	if content, _ := os.ReadFile(path); string(content) != plain {
		t.Fatalf("expected the file to be untouched, got %q", content)
	}

	if reorder, err := EnsureConfigOrder(filepath.Join(dir, "missing.lua")); err != nil || reorder.Changed {
		t.Fatalf("expected a missing config to be a quiet no-op, got %+v (err=%v)", reorder, err)
	}
}
