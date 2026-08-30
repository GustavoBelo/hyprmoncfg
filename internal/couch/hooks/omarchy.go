package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// idleHook holds off the lock screen and the screensaver for the session.
//
// Without it a controller in hand does not count as activity, so the lock
// screen lands mid-game.
type idleHook struct{}

func (*idleHook) Name() string        { return "idle" }
func (*idleHook) Description() string { return "Hold off the lock screen and screensaver" }
func (*idleHook) Available() bool     { return have("omarchy-toggle-idle") }

func (h *idleHook) Capture(ctx context.Context, _ Env) (State, error) {
	awake, err := idleStayAwake(ctx)
	if err != nil {
		return nil, err
	}
	if awake {
		// Already staying awake for the user's own reasons.
		return nil, nil
	}
	return State{"stay_awake": "false"}, nil
}

func (h *idleHook) Apply(ctx context.Context, _ Env) error {
	_, err := run(ctx, "omarchy-toggle-idle", "stay-awake")
	return err
}

func (h *idleHook) Restore(ctx context.Context, _ Env, prev State) error {
	if prev["stay_awake"] == "true" {
		return nil
	}
	_, err := run(ctx, "omarchy-toggle-idle", "allow-idle")
	return err
}

func idleStayAwake(ctx context.Context) (bool, error) {
	out, err := run(ctx, "omarchy-toggle-idle", "status")
	if err != nil {
		return false, err
	}
	var status struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		return false, fmt.Errorf("read idle status: %w", err)
	}
	return status.Enabled, nil
}

// barHook gets the desktop bar off the TV.
type barHook struct{}

func (*barHook) Name() string        { return "bar" }
func (*barHook) Description() string { return "Hide the desktop bar" }
func (*barHook) Available() bool     { return have("omarchy-toggle-bar") }

func (h *barHook) Capture(_ context.Context, _ Env) (State, error) {
	if barHidden() {
		return nil, nil
	}
	return State{"hidden": "false"}, nil
}

// Apply hides the bar.
//
// The argument names the flag, not the bar: omarchy-toggle-bar forwards to
// `omarchy-toggle bar-off <action>`, so "on" enables bar-off and hides the bar,
// while "off" clears it and shows the bar. Reading that the other way round hid
// the bar when a session ended instead of while it ran.
func (h *barHook) Apply(ctx context.Context, _ Env) error {
	_, err := run(ctx, "omarchy-toggle-bar", "on")
	return err
}

func (h *barHook) Restore(ctx context.Context, _ Env, prev State) error {
	if prev["hidden"] == "true" {
		return nil
	}
	_, err := run(ctx, "omarchy-toggle-bar", "off")
	return err
}

// barHidden reads the flag omarchy-toggle writes, which is the state the shell
// reads too.
func barHidden() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".local", "state", "omarchy", "toggles", "bar-off"))
	return err == nil
}

// notificationsHook silences do-not-disturb for the session.
type notificationsHook struct{}

func (*notificationsHook) Name() string        { return "notifications" }
func (*notificationsHook) Description() string { return "Silence notifications" }
func (*notificationsHook) Available() bool     { return have("omarchy-shell") }

func (h *notificationsHook) Capture(ctx context.Context, _ Env) (State, error) {
	on, err := dndEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if on {
		return nil, nil
	}
	return State{"dnd": "off"}, nil
}

func (h *notificationsHook) Apply(ctx context.Context, _ Env) error {
	return setDND(ctx, true)
}

func (h *notificationsHook) Restore(ctx context.Context, _ Env, prev State) error {
	if prev["dnd"] == "on" {
		return nil
	}
	return setDND(ctx, false)
}

// dndEnabled and setDND use the shell's own state accessors.
//
// `notifications dnd` is not a method -- it answers "Function not found." and
// still exits 0 -- and toggleDnd flips whatever is there, which races anyone
// changing it during a session. dndState and setDnd are the readable and
// idempotent pair.
func dndEnabled(ctx context.Context) (bool, error) {
	out, err := run(ctx, "omarchy-shell", "notifications", "dndState")
	if err != nil {
		return false, err
	}
	value := strings.ToLower(strings.TrimSpace(out))
	if value != "on" && value != "off" {
		return false, fmt.Errorf("unexpected do-not-disturb state %q", out)
	}
	return value == "on", nil
}

func setDND(ctx context.Context, on bool) error {
	value := "off"
	if on {
		value = "on"
	}
	_, err := run(ctx, "omarchy-shell", "notifications", "setDnd", value)
	return err
}

// nightLightHook takes the warm tint off, which is not what anyone wants over a
// game.
type nightLightHook struct{}

func (*nightLightHook) Name() string        { return "nightlight" }
func (*nightLightHook) Description() string { return "Turn off the night light" }
func (*nightLightHook) Available() bool     { return have("omarchy-toggle-nightlight") }

func (h *nightLightHook) Capture(ctx context.Context, _ Env) (State, error) {
	if !nightLightOn(ctx) {
		return nil, nil
	}
	return State{"night_light": "on"}, nil
}

func (h *nightLightHook) Apply(ctx context.Context, _ Env) error {
	_, err := run(ctx, "omarchy-toggle-nightlight")
	return err
}

func (h *nightLightHook) Restore(ctx context.Context, _ Env, prev State) error {
	if prev["night_light"] != "on" || nightLightOn(ctx) {
		return nil
	}
	_, err := run(ctx, "omarchy-toggle-nightlight")
	return err
}

func nightLightOn(ctx context.Context) bool {
	out, err := run(ctx, "pgrep", "-x", "hyprsunset")
	if err != nil {
		// pgrep exits 1 when nothing matches, which is a clean "off".
		return false
	}
	return strings.TrimSpace(out) != ""
}
