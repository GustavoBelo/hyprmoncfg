package hypr

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Hyprland running a Lua config refuses the classic dispatcher syntax: it wraps
// the request as `hl.dispatch(<raw text>)`, which is not valid Lua, and answers
//
//	error: [string "return hl.dispatch(focuswindow address:0x…)"]:1: ')' expected
//
// Every window action a couch session needs -- closing Big Picture, putting it
// fullscreen, tiling it first, focusing the TV -- goes through here so the
// dialect is decided once instead of at each call site.

// windowAddress is validated before being spliced into a Lua snippet. Addresses
// come from hyprctl, but building source text from a value is not something to
// do on trust.
var windowAddress = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)

type dispatchDialect struct {
	once sync.Once
	lua  bool
}

var dialect dispatchDialect

// usesLuaDispatch reports whether this compositor takes the Lua dispatcher
// form. Detected once per process: the answer cannot change while Hyprland runs.
func (c *Client) usesLuaDispatch(ctx context.Context) bool {
	dialect.once.Do(func() {
		cmd, err := c.commandContext(ctx, "repl", "print(type(hl.dispatch))")
		if err != nil {
			return
		}
		out, err := cmd.Output()
		if err != nil {
			return
		}
		dialect.lua = strings.Contains(string(out), "function")
	})
	return dialect.lua
}

// evalWindow runs a Lua snippet with the window at address bound to `w`.
//
// Hyprland offers no lookup by address, so the window list is scanned and
// matched on its string form, which carries the address: "HL.Window(0x…)".
func (c *Client) evalWindow(ctx context.Context, address, action string) error {
	if !windowAddress.MatchString(address) {
		return fmt.Errorf("refusing to dispatch on malformed window address %q", address)
	}
	script := fmt.Sprintf(`local w
for _, candidate in ipairs(hl.get_windows()) do
  if tostring(candidate) == "HL.Window(%s)" then w = candidate end
end
if w then %s end`, address, action)

	cmd, err := c.commandContext(ctx, "repl", script)
	if err != nil {
		return err
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("window dispatch failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	// hyprctl exits 0 on a refused request, so the reply has to be read.
	if reply := strings.TrimSpace(string(out)); strings.HasPrefix(reply, "error:") {
		return fmt.Errorf("window dispatch refused: %s", reply)
	}
	return nil
}

// CloseWindow asks one window to close, by address.
//
// By address and never by pid: a pid takes down every window of that process,
// which for Steam means the desktop client goes with Big Picture.
func (c *Client) CloseWindow(ctx context.Context, address string) error {
	if c.usesLuaDispatch(ctx) {
		return c.evalWindow(ctx, address, `hl.dispatch(hl.dsp.window.close({ window = w }))`)
	}
	return c.Dispatch(ctx, "closewindow", "address:"+address)
}

// SetWindowFullscreen puts one window fullscreen, or takes it back out.
func (c *Client) SetWindowFullscreen(ctx context.Context, address string, on bool) error {
	action := "unset"
	if on {
		action = "set"
	}
	if c.usesLuaDispatch(ctx) {
		return c.evalWindow(ctx, address, fmt.Sprintf(
			`hl.dispatch(hl.dsp.window.fullscreen({ mode = "fullscreen", action = %q, window = w }))`, action))
	}
	if !on {
		return c.Dispatch(ctx, "fullscreenstate", "0", "address:"+address)
	}
	return c.Dispatch(ctx, "fullscreenstate", "2", "address:"+address)
}

// SetWindowTiled takes a window out of floating.
//
// Big Picture needs this before fullscreen: Omarchy pins Steam floating, and a
// floating window returns to its floating geometry the moment fullscreen ends.
func (c *Client) SetWindowTiled(ctx context.Context, address string) error {
	if c.usesLuaDispatch(ctx) {
		return c.evalWindow(ctx, address,
			`hl.dispatch(hl.dsp.window.float({ action = "unset", window = w }))`)
	}
	return c.Dispatch(ctx, "settiled", "address:"+address)
}

// FocusMonitor moves focus to a display, so the next window opens there.
func (c *Client) FocusMonitor(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("no monitor named")
	}
	if c.usesLuaDispatch(ctx) {
		cmd, err := c.commandContext(ctx, "repl",
			fmt.Sprintf(`local m = hl.get_monitor(%q)
if m then hl.dispatch(hl.dsp.focus({ monitor = m })) end`, name))
		if err != nil {
			return err
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("focus monitor %s: %w (%s)", name, err, strings.TrimSpace(string(out)))
		}
		if reply := strings.TrimSpace(string(out)); strings.HasPrefix(reply, "error:") {
			return fmt.Errorf("focus monitor %s refused: %s", name, reply)
		}
		return nil
	}
	return c.Dispatch(ctx, "focusmonitor", name)
}
