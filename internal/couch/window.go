package couch

import (
	"context"
	"strings"
	"syscall"

	"github.com/crmne/hyprmoncfg/internal/hypr"
)

// KeepBigPictureFullscreen forces the Gamepad UI window to fill the TV.
//
// Omarchy ships `o.window("steam", { float = true })` and a 1100x700 rule for
// `class=steam, title=Steam` (/usr/share/omarchy/default/hypr/apps/steam.lua).
// Big Picture opens under those rules, so without this it comes up as a small
// floating window in the middle of the TV -- and, because the rule keeps it
// non-fullscreen, none of the geometry-based detection tells can fire either.
//
// Steam also drops out of fullscreen when a game exits and comes back as that
// same floating window, so this is re-run on every window-title change rather
// than once at startup.
func KeepBigPictureFullscreen(ctx context.Context, client WindowCloser, detector *BigPictureDetector) int {
	fixed := 0
	for _, w := range detector.CertainWindows(ctx) {
		if w.Fullscreen > 0 {
			continue
		}
		// Tiling it first is what makes fullscreen stick: a floating window
		// toggled fullscreen returns to its floating geometry the moment it
		// loses the state.
		if w.Floating {
			_ = client.SetWindowTiled(ctx, w.Address)
		}
		if err := client.SetWindowFullscreen(ctx, w.Address, true); err != nil {
			continue
		}
		fixed++
	}
	return fixed
}

// WindowEventAddress pulls the window address out of a Hyprland event payload.
//
// openwindow is "ADDR,WORKSPACE,CLASS,TITLE" and windowtitlev2 is "ADDR,TITLE",
// while windowtitle and closewindow carry the address alone.
func WindowEventAddress(ev hypr.Event) string {
	value := strings.TrimSpace(ev.Value)
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, ","); idx >= 0 {
		return value[:idx]
	}
	return value
}

// EventLooksLikeBigPicture reads the class and title straight out of an
// openwindow or windowtitlev2 payload, so an obviously unrelated window costs
// nothing to reject.
//
// It is a filter, never proof: a bare windowtitle event carries only an
// address, so an empty payload has to fall through to a real query.
func EventLooksLikeBigPicture(ev hypr.Event) bool {
	parts := strings.Split(ev.Value, ",")
	if len(parts) < 2 {
		// Not enough payload to judge; let the caller query.
		return true
	}
	return steamishRe.MatchString(strings.Join(parts[1:], " "))
}

// KeepNestedFullscreen forces the nested compositor's window to fill the TV.
//
// gamescope is matched by the process the session started, not by class or
// title. Its class is "gamescope" rather than anything Steam-shaped, so the
// Gamepad UI tells cannot see it; and its title is whatever is focused inside
// it, which becomes the game's the moment one launches, so matching on that
// would be worse than not matching at all.
//
// This has to be re-run rather than done once. Re-applying the display layout
// -- which is what changing the TV's resolution mid-session does -- drops the
// window out of fullscreen, and with nothing to put it back the session is left
// showing a nested compositor floating over the desktop it was meant to cover.
func KeepNestedFullscreen(ctx context.Context, client WindowCloser, pid int) int {
	if pid <= 0 {
		return 0
	}
	windows, err := client.Clients(ctx)
	if err != nil {
		return 0
	}
	fixed := 0
	for _, w := range windows {
		if w.Fullscreen > 0 || !belongsToSession(w.Pid, pid) {
			continue
		}
		if w.Floating {
			_ = client.SetWindowTiled(ctx, w.Address)
		}
		if err := client.SetWindowFullscreen(ctx, w.Address, true); err != nil {
			continue
		}
		fixed++
	}
	return fixed
}

// belongsToSession reports whether a window's process is part of the group the
// session started.
//
// The group rather than the process: the launcher sets a session id, and a
// nested compositor is free to put its Wayland client in a child. Comparing
// only the leader's pid would miss it.
func belongsToSession(windowPID, leader int) bool {
	if windowPID <= 0 || leader <= 0 {
		return false
	}
	if windowPID == leader {
		return true
	}
	group, err := syscall.Getpgid(windowPID)
	return err == nil && group == leader
}
