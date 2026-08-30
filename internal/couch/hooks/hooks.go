// Package hooks holds the parts of a console session that are not the display
// layout: audio on the TV, the lock screen held off, notifications silenced,
// the bar out of the way.
//
// A hook records what it found before changing anything, and that record is
// data rather than a closure. That is what makes it survive: a daemon killed
// mid-session loses every closure it held, and the desktop would be left with
// the bar hidden and sound on a TV nobody is watching. The record goes into the
// session file next to the display snapshot, so whoever starts next can put
// both back.
package hooks

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// State is what a hook captured before it changed anything. It is persisted, so
// it has to stay small and serialisable.
type State map[string]string

// Env is what a hook needs to know about the session.
type Env struct {
	// TVName is the connector the session plays on ("HDMI-A-1").
	TVName string
	// TVDescription is the display's EDID description, used to find its audio
	// sink when the connector name alone is not enough.
	TVDescription string
	Logf          func(format string, args ...any)
}

func (e Env) logf(format string, args ...any) {
	if e.Logf != nil {
		e.Logf(format, args...)
	}
}

// Hook is one reversible change a console session makes.
type Hook interface {
	// Name is the key used in the config and in the session record.
	Name() string
	// Description is one line for the settings UI.
	Description() string
	// Available reports whether this machine has what the hook needs. An
	// unavailable hook is not offered, rather than offered and broken.
	Available() bool
	// Capture records the current state. Returning a nil State means there is
	// nothing to change and nothing to put back.
	Capture(ctx context.Context, env Env) (State, error)
	// Apply enters the console state.
	Apply(ctx context.Context, env Env) error
	// Restore puts back what Capture recorded.
	Restore(ctx context.Context, env Env, prev State) error
}

// All returns every hook, in the order a session applies them.
func All() []Hook {
	return []Hook{
		&audioHook{},
		&idleHook{},
		&notificationsHook{},
		&barHook{},
		&nightLightHook{},
	}
}

// Available returns the hooks this machine can actually run.
func Available() []Hook {
	usable := make([]Hook, 0, 5)
	for _, h := range All() {
		if h.Available() {
			usable = append(usable, h)
		}
	}
	return usable
}

func byName(name string) (Hook, bool) {
	for _, h := range All() {
		if h.Name() == name {
			return h, true
		}
	}
	return nil, false
}

// Enter runs each enabled hook and returns what to put back later.
//
// A hook that fails is logged and skipped rather than aborting the session:
// losing the HDMI audio switch is not a reason to refuse to play, and whatever
// did apply is still recorded so it can be undone.
func Enter(ctx context.Context, env Env, enabled func(name string) bool) map[string]State {
	applied := make(map[string]State)
	for _, hook := range All() {
		if enabled != nil && !enabled(hook.Name()) {
			continue
		}
		if !hook.Available() {
			continue
		}
		prev, err := hook.Capture(ctx, env)
		if err != nil {
			env.logf("couch: hook %s could not read the current state: %v", hook.Name(), err)
			continue
		}
		if prev == nil {
			// Already in the state the session wants; leave it alone in both
			// directions.
			continue
		}
		if err := hook.Apply(ctx, env); err != nil {
			env.logf("couch: hook %s did not apply: %v", hook.Name(), err)
			continue
		}
		applied[hook.Name()] = prev
	}
	return applied
}

// Leave undoes everything recorded, and keeps going past a failure so one stuck
// hook cannot strand the rest of the desktop in console mode.
func Leave(ctx context.Context, env Env, applied map[string]State) error {
	var failures []error
	for name, prev := range applied {
		hook, ok := byName(name)
		if !ok {
			// A hook that no longer exists in this build; nothing to undo it
			// with, and guessing would be worse.
			env.logf("couch: no hook named %s to undo", name)
			continue
		}
		if err := hook.Restore(ctx, env, prev); err != nil {
			env.logf("couch: hook %s did not restore: %v", name, err)
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// Names lists what a session changed, for the log.
func Names(applied map[string]State) []string {
	names := make([]string, 0, len(applied))
	for _, hook := range All() {
		if _, ok := applied[hook.Name()]; ok {
			names = append(names, hook.Name())
		}
	}
	return names
}

// run executes a helper with a short deadline. Session hooks must never be able
// to hang the session on a command that does not return.
func run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

func have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
