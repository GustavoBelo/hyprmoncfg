package omarchywatch

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/crmne/hyprmoncfg/internal/config"
)

const (
	clamshellCommand = "omarchy-hyprland-monitor-clamshell"
	// Omarchy's Lua and shell sides agree on this location under the state home.
	togglesStateDir   = "omarchy/toggles/hypr"
	internalScaleFile = "internal-monitor-scale"
)

// StoreInternalScale records the scale an internal panel should run at where
// Omarchy's clamshell script looks for it.
//
// That script resolves a scale from hyprmoncfg's monitors.lua first, but its
// parser only matches single-line rules keyed by connector name while we write
// multi-line rules keyed by hardware description, so it never reads ours. It
// then falls back to this file, and finally to a hardcoded 2. Keeping the file
// current means a lid or wake event brings the panel back at the scale the
// applied profile asked for rather than Omarchy's default.
//
// It is a mitigation, not a fix: the same script hardcodes mode = "preferred"
// and falls back to position = "auto", which only re-applying the profile can
// put right.
func StoreInternalScale(scale float64) error {
	if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return nil
	}
	// Nothing reads the file where Omarchy's clamshell script is not installed,
	// so do not create its state directory on other systems.
	if _, err := exec.LookPath(clamshellCommand); err != nil {
		return nil
	}

	dir := togglesDir()
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	// Omarchy validates what it reads against ^[0-9]+([.][0-9]+)?$, so the value
	// must carry no exponent and no sign.
	value := strconv.FormatFloat(scale, 'f', -1, 64)
	path := filepath.Join(dir, internalScaleFile)
	if err := config.WriteFileAtomic(path, []byte(value+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func togglesDir() string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, togglesStateDir)
}
