package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// unmanagedMarker records that someone handed monitor configuration back to
// Hyprland. Absence means managed, so an install made before this file existed
// behaves exactly as it always did, and a failure to read the marker errs
// toward hyprmoncfg still doing its job.
const unmanagedMarker = "unmanaged"

const unmanagedNotice = "hyprmoncfg is not managing monitor configuration.\n" +
	"Delete this file, or run `hyprmoncfg manage`, to hand it back.\n"

func unmanagedPath(baseDir string) string {
	return filepath.Join(baseDir, unmanagedMarker)
}

// IsManaged reports whether hyprmoncfg should be steering monitor config.
//
// The daemon checks this before every apply. Removing the include is not enough
// on its own: a running daemon re-adds it on the next monitor event, so the
// choice has to be somewhere both the daemon and the CLI can see, and has to
// outlive a restart.
func IsManaged(baseDir string) bool {
	if strings.TrimSpace(baseDir) == "" {
		return true
	}
	_, err := os.Stat(unmanagedPath(baseDir))
	return err != nil
}

// SetManaged records the choice so it survives a daemon restart or a reboot.
func SetManaged(baseDir string, managed bool) error {
	if strings.TrimSpace(baseDir) == "" {
		return nil
	}

	path := unmanagedPath(baseDir)
	if managed {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}

	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", baseDir, err)
	}
	return WriteFileAtomic(path, []byte(unmanagedNotice), 0o644)
}
