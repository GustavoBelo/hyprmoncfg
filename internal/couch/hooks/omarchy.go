package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// idleHook holds off the lock screen and the screensaver for the session.
//
// Without it a controller in hand does not count as activity, so the lock
// screen lands mid-game. Omarchy's helper is idempotent -- stay-awake and
// allow-idle are states, not a toggle -- and it reports the state it was in, so
// the undo can put it back rather than assuming it was on.
type idleHook struct{}

func (*idleHook) Name() string        { return "idle" }
func (*idleHook) Description() string { return "Hold off the lock screen and screensaver" }
func (*idleHook) Available() bool     { return have("omarchy-toggle-idle") }

func (h *idleHook) Enter(ctx context.Context, env Env) (Undo, error) {
	wasAwake, err := idleStayAwake(ctx)
	if err != nil {
		return nil, err
	}
	if wasAwake {
		// Already staying awake for the user's own reasons; leave it alone in
		// both directions.
		return nil, nil
	}
	if _, err := run(ctx, "omarchy-toggle-idle", "stay-awake"); err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		_, err := run(ctx, "omarchy-toggle-idle", "allow-idle")
		return err
	}, nil
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

func (h *barHook) Enter(ctx context.Context, env Env) (Undo, error) {
	// on/off are states rather than a toggle, so this is safe to repeat and
	// safe to undo without reading first.
	if _, err := run(ctx, "omarchy-toggle-bar", "off"); err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		_, err := run(ctx, "omarchy-toggle-bar", "on")
		return err
	}, nil
}

// notificationsHook silences do-not-disturb for the session.
//
// Unlike the others this helper only toggles, so the state has to be read first
// or the undo would turn DND on for someone who never had it.
type notificationsHook struct{}

func (*notificationsHook) Name() string        { return "notifications" }
func (*notificationsHook) Description() string { return "Silence notifications" }
func (*notificationsHook) Available() bool     { return have("omarchy-shell") }

func (h *notificationsHook) Enter(ctx context.Context, env Env) (Undo, error) {
	already, err := dndEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if already {
		return nil, nil
	}
	if _, err := run(ctx, "omarchy-shell", "notifications", "toggleDnd"); err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		// Only toggle back if it is still on: the user may have turned it off
		// themselves during the session, and toggling then would turn it on.
		on, err := dndEnabled(ctx)
		if err != nil || !on {
			return err
		}
		_, err = run(ctx, "omarchy-shell", "notifications", "toggleDnd")
		return err
	}, nil
}

func dndEnabled(ctx context.Context) (bool, error) {
	out, err := run(ctx, "omarchy-shell", "notifications", "dnd")
	if err != nil {
		return false, err
	}
	value := strings.ToLower(strings.TrimSpace(out))
	return value == "true" || value == "1" || value == "enabled", nil
}

// nightLightHook takes the warm tint off, which is not what anyone wants over a
// game.
type nightLightHook struct{}

func (*nightLightHook) Name() string        { return "nightlight" }
func (*nightLightHook) Description() string { return "Turn off the night light" }
func (*nightLightHook) Available() bool     { return have("omarchy-toggle-nightlight") }

func (h *nightLightHook) Enter(ctx context.Context, env Env) (Undo, error) {
	on, err := nightLightOn(ctx)
	if err != nil || !on {
		// Off already, or unreadable: either way, nothing to change and
		// nothing to put back.
		return nil, nil
	}
	if _, err := run(ctx, "omarchy-toggle-nightlight"); err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		nowOn, err := nightLightOn(ctx)
		if err != nil || nowOn {
			return err
		}
		_, err = run(ctx, "omarchy-toggle-nightlight")
		return err
	}, nil
}

func nightLightOn(ctx context.Context) (bool, error) {
	out, err := run(ctx, "pgrep", "-x", "hyprsunset")
	if err != nil {
		// pgrep exits 1 when nothing matches, which is a clean "off".
		return false, nil
	}
	return strings.TrimSpace(out) != "", nil
}
