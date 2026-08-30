package couch

import (
	"fmt"
	"os/exec"
	"strings"
)

// GamescopeAvailable reports whether the nested session can be offered.
func GamescopeAvailable() bool {
	_, err := exec.LookPath("gamescope")
	return err == nil
}

// GamescopeCommand builds the nested launch line.
//
// Nested is the useful shape here: gamescope runs as one fullscreen window
// inside Hyprland, on the TV, so couch mode can still be entered and left at
// will. The alternative -- gamescope-session as a login session -- replaces
// Hyprland outright and is therefore a choice at the greeter, not something a
// running daemon can switch to.
//
// Resolution and refresh come from the console layout rather than being asked
// for separately: two places to say what the TV runs at is one place too many.
func GamescopeCommand(layout ConsoleLayout, settings GamescopeSettings, launcher BigPictureLauncher) (BigPictureLauncher, error) {
	mode, ok := parseMode(layout.Mode)
	if !ok {
		return BigPictureLauncher{}, fmt.Errorf("cannot read the console mode %q", layout.Mode)
	}
	path, err := exec.LookPath("gamescope")
	if err != nil {
		return BigPictureLauncher{}, err
	}

	args := []string{
		"-f", // fullscreen
		"-W", fmt.Sprint(mode.width),
		"-H", fmt.Sprint(mode.height),
		"-r", fmt.Sprintf("%.0f", mode.refresh),
	}
	if layout.HDR {
		args = append(args, "--hdr-enabled")
	}
	if settings.FPSLimit > 0 {
		args = append(args, "--framerate-limit", fmt.Sprint(settings.FPSLimit))
	}
	if settings.MangoApp {
		args = append(args, "--mangoapp")
	}

	args = append(args, "--", launcher.Command)
	args = append(args, launcher.Args...)
	return BigPictureLauncher{Command: path, Args: args}, nil
}

// GamescopeSummary describes the launch line for the settings UI and the log.
func GamescopeSummary(layout ConsoleLayout, settings GamescopeSettings) string {
	if !settings.Enabled {
		return "off"
	}
	parts := []string{layout.Mode}
	if layout.HDR {
		parts = append(parts, "HDR")
	}
	if settings.FPSLimit > 0 {
		parts = append(parts, fmt.Sprintf("%d fps cap", settings.FPSLimit))
	}
	if settings.MangoApp {
		parts = append(parts, "HUD")
	}
	return strings.Join(parts, ", ")
}
