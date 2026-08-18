package omarchywatch

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/crmne/hyprmoncfg/internal/config"
)

// Omarchy loads its dynamic toggles last, and one of them is the clamshell rule
// that `omarchy-hyprland-monitor-clamshell` writes before reloading Hyprland.
// A Lua config that requires hyprmoncfg's monitors before that point therefore
// hands the last word to Omarchy on every reload, whatever hyprmoncfg applied.
const (
	monitorsRequire = `require("hypr.monitors")`
	togglesRequire  = `require("default.hypr.toggles")`
)

// ConfigOrder describes where a Hyprland Lua config loads hyprmoncfg's monitors
// relative to Omarchy's toggles.
type ConfigOrder struct {
	// Applicable is false for configs this does not govern: no Omarchy
	// toggles, or no hyprmoncfg monitors require.
	Applicable bool
	// MonitorsLast is true when hyprmoncfg's rules already have the last word.
	MonitorsLast bool
}

// NeedsReorder reports whether Omarchy would override hyprmoncfg on reload.
func (o ConfigOrder) NeedsReorder() bool {
	return o.Applicable && !o.MonitorsLast
}

var (
	monitorsLine = regexp.MustCompile(`(?m)^[ \t]*require\("hypr\.monitors"\)[ \t]*$`)
	togglesLine  = regexp.MustCompile(`(?m)^[ \t]*require\("default\.hypr\.toggles"\)[ \t]*$`)
)

// InspectConfigOrder reads a Hyprland Lua config and reports the load order.
func InspectConfigOrder(path string) (ConfigOrder, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return ConfigOrder{}, fmt.Errorf("read %s: %w", path, err)
	}
	return inspectConfigOrder(string(content)), nil
}

func inspectConfigOrder(content string) ConfigOrder {
	monitors := monitorsLine.FindStringIndex(content)
	toggles := togglesLine.FindStringIndex(content)
	if monitors == nil || toggles == nil {
		return ConfigOrder{}
	}
	return ConfigOrder{Applicable: true, MonitorsLast: monitors[0] > toggles[0]}
}

// Reorder reports what EnsureConfigOrder did to a config.
type Reorder struct {
	Path       string
	Changed    bool
	BackupPath string
}

// EnsureConfigOrder gives hyprmoncfg's monitor rules the last word in a Hyprland
// Lua config, keeping the previous file alongside. A config it does not govern
// is left untouched.
func EnsureConfigOrder(path string) (Reorder, error) {
	result := Reorder{Path: path}
	if strings.TrimSpace(path) == "" {
		return result, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, fmt.Errorf("read %s: %w", path, err)
	}

	reordered, changed := ReorderConfig(string(content))
	if !changed {
		return result, nil
	}

	backup := path + backupSuffix
	if err := config.WriteFileAtomic(backup, content, 0o644); err != nil {
		return result, fmt.Errorf("back up %s: %w", path, err)
	}
	if err := config.WriteFileAtomic(path, []byte(reordered), 0o644); err != nil {
		return result, fmt.Errorf("rewrite %s: %w", path, err)
	}

	result.Changed = true
	result.BackupPath = backup
	return result, nil
}

const backupSuffix = ".hyprmoncfg-backup"

// ReorderConfig moves the monitors require below Omarchy's toggles so the rules
// hyprmoncfg generates are the last ones Hyprland reads. It returns the rewritten
// config and whether anything changed.
//
// Relocating is the only option that works: Lua caches modules, so a second
// require of the same module later in the file does nothing at all.
func ReorderConfig(content string) (string, bool) {
	if !inspectConfigOrder(content).NeedsReorder() {
		return content, false
	}

	lines := strings.Split(content, "\n")
	monitorsAt, togglesAt := -1, -1
	for idx, line := range lines {
		switch {
		case monitorsAt < 0 && monitorsLine.MatchString(line):
			monitorsAt = idx
		case togglesAt < 0 && togglesLine.MatchString(line):
			togglesAt = idx
		}
	}
	if monitorsAt < 0 || togglesAt < 0 || monitorsAt > togglesAt {
		return content, false
	}

	moved := lines[monitorsAt]
	rest := append(lines[:monitorsAt:monitorsAt], lines[monitorsAt+1:]...)
	insertAt := togglesAt - 1 // the toggles line moved up when the require left

	out := make([]string, 0, len(lines)+3)
	out = append(out, rest[:insertAt+1]...)
	out = append(out,
		"",
		"-- hyprmoncfg's monitor rules load last so Omarchy's dynamic toggles,",
		"-- including the clamshell rule, cannot override the applied layout.",
		moved,
	)
	out = append(out, rest[insertAt+1:]...)
	return strings.Join(out, "\n"), true
}
