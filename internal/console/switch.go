package console

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Mode is which compositor the hosting session should run next.
type Mode string

const (
	ModeDesktop Mode = "desktop"
	ModeConsole Mode = "console"
)

// requestFile is how a switch is asked for. A file rather than a socket because
// the process asking is about to be killed by the switch it is asking for: it
// has to leave the request somewhere that outlives it, and the wrapper reads it
// only after the compositor has gone.
const requestFile = "hyprmoncfg-next-session"

func RequestPath(runtimeDir string) string {
	return filepath.Join(runtimeDir, requestFile)
}

// Request records which compositor should run after the current one exits.
//
// No request means a real log out: the wrapper's loop ends and the session
// closes, which is what makes an ordinary logout still work.
func Request(runtimeDir string, mode Mode) error {
	return os.WriteFile(RequestPath(runtimeDir), []byte(string(mode)+"\n"), 0o600)
}

// TakeRequest reads and clears a pending request.
//
// Clearing is the point: a request that survived being acted on would switch
// again on the next exit, and the user would be unable to log out.
func TakeRequest(runtimeDir string) (Mode, bool) {
	path := RequestPath(runtimeDir)
	data, err := os.ReadFile(path)
	_ = os.Remove(path)
	if err != nil {
		return "", false
	}
	switch mode := Mode(strings.TrimSpace(string(data))); mode {
	case ModeDesktop, ModeConsole:
		return mode, true
	default:
		return "", false
	}
}

// ClearRequest drops a pending request without acting on it.
func ClearRequest(runtimeDir string) { _ = os.Remove(RequestPath(runtimeDir)) }

// StopCompositor ends the running compositor so the wrapper can start the other
// one.
//
// Verified by effect, never by exit status. `hyprctl dispatch` is accepted and
// silently ignored by Hyprland's Lua configuration parser -- it exits 0 and does
// nothing -- so a caller that trusted the return code would report success and
// leave the user staring at their desktop wondering why nothing happened.
func StopCompositor(ctx context.Context, runtimeDir string) error {
	attempts := [][]string{}
	if _, err := exec.LookPath("uwsm"); err == nil {
		attempts = append(attempts, []string{"uwsm", "stop"})
	}
	if _, err := exec.LookPath("hyprctl"); err == nil {
		attempts = append(attempts, []string{"hyprctl", "dispatch", "exit"})
	}
	if len(attempts) == 0 {
		return errors.New("no way to stop the compositor: neither uwsm nor hyprctl is installed")
	}

	var last error
	for _, attempt := range attempts {
		cmd := exec.CommandContext(ctx, attempt[0], attempt[1:]...)
		if err := cmd.Run(); err != nil {
			last = fmt.Errorf("%s: %w", strings.Join(attempt, " "), err)
			continue
		}
		if compositorGone(ctx, runtimeDir, 10*time.Second) {
			return nil
		}
		last = fmt.Errorf("%s was accepted but the compositor is still running", strings.Join(attempt, " "))
	}
	return last
}

// StopConsoleSession ends a running gamescope session.
//
// Stopping the target is what Steam's own "Switch to Desktop" does, so this is
// the same door rather than a second one.
func StopConsoleSession(ctx context.Context, sc Runner) error {
	if sc == nil {
		sc = Systemctl{}
	}
	if out, err := sc.Output(ctx, "is-active", "gamescope-session.target"); err != nil || out != "active" {
		return errors.New("no console session is running")
	}
	return sc.Run(ctx, "stop", "gamescope-session.target")
}

// compositorGone waits for the Hyprland instance to actually disappear.
func compositorGone(ctx context.Context, runtimeDir string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if !compositorRunning(runtimeDir) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// compositorRunning reports whether a Hyprland instance still holds a socket.
//
// The socket directory is the tell rather than a process name, because the
// wrapper may be running any compositor the user configured.
func compositorRunning(runtimeDir string) bool {
	instances, err := filepath.Glob(filepath.Join(runtimeDir, "hypr", "*", ".socket.sock"))
	if err != nil {
		return false
	}
	return len(instances) > 0
}

// RuntimeDir is where the request file lives. It has to be the same directory
// for the process asking and for the wrapper reading, which is the one thing
// they share.
func RuntimeDir() (string, error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		return "", errors.New("XDG_RUNTIME_DIR is not set, so there is nowhere to leave the request")
	}
	return dir, nil
}

// cancelFile stops a pending automatic entry. Same shape as the request file
// and for the same reason: the daemon that armed the entry and the command that
// calls it off are different processes, and the runtime directory is what they
// share.
const cancelFile = "hyprmoncfg-cancel-entry"

func CancelPath(runtimeDir string) string { return filepath.Join(runtimeDir, cancelFile) }

// cancelMaxAge is how long a stand-down stays meaningful.
//
// A countdown polls for the file every second, so one that a live countdown was
// going to consume is never more than a moment old. Anything older was written
// when nothing was counting down -- `hyprmoncfg console cancel` typed at a shell
// with no entry pending -- and honouring it later would call off the next
// legitimate entry, silently, without anyone having asked.
const cancelMaxAge = 30 * time.Second

// RequestCancel asks a pending automatic entry to stand down.
func RequestCancel(runtimeDir string) error {
	return os.WriteFile(CancelPath(runtimeDir), []byte("1\n"), 0o600)
}

// TakeCancel reports whether a stand-down was asked for, and clears it.
//
// A stale file is cleared and ignored rather than left alone, so the trap
// disarms itself instead of waiting for the next entry to walk into it.
func TakeCancel(runtimeDir string) bool {
	path := CancelPath(runtimeDir)
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	_ = os.Remove(path)
	return time.Since(info.ModTime()) <= cancelMaxAge
}

// DropCancel clears a stand-down without acting on it.
//
// A countdown calls this before it announces anything. Without it, a cancel
// written seconds earlier -- while nothing was pending -- would call off an
// entry that had not even been announced yet, and the user would see a countdown
// vanish for no stated reason.
func DropCancel(runtimeDir string) { _ = os.Remove(CancelPath(runtimeDir)) }
