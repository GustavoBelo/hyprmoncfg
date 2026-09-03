package daemon

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/crmne/hyprmoncfg/internal/config"
	"github.com/crmne/hyprmoncfg/internal/console"
	"github.com/crmne/hyprmoncfg/internal/notify"
)

// consoleController turns a request -- a controller switched on, a button, a
// command -- into a console session.
//
// It is deliberately harder to fire than the couch-mode trigger it replaces.
// Back then entering rearranged some displays; now it ends the desktop session
// and everything on it, so the trigger is off unless asked for, it announces
// itself, and it gives the user a window to stop it.
type consoleController struct {
	svc *Service

	mu sync.Mutex
	// controllers is the last count seen, so only the edge from none to some
	// counts. Polling a level would re-enter a second after every exit.
	controllers int
	// armed is the pending entry, cancelled by unplugging the pad again, by the
	// button on the notification, or by `hyprmoncfg console cancel`.
	armed  bool
	cancel context.CancelFunc
	// calledOff is who stopped the pending entry, recorded before the context
	// is cancelled so the countdown can say so on its way out.
	calledOff string
	// byController is whether the pending entry was started by a controller,
	// and so whether switching one off should call it off again.
	byController bool
}

func newConsoleController(svc *Service) *consoleController {
	return &consoleController{svc: svc, controllers: console.ConnectedControllers()}
}

// reportPastFailure announces a console session that could not start.
//
// The wrapper cannot say this itself. By the time it knows, it has already
// stopped the desktop compositor and taken the notification server with it, so
// it writes the reason down instead. This runs from the daemon's poll, inside
// the desktop that came back, which is the first moment there is anyone to tell
// -- and the user is sitting in front of a desktop with everything they had open
// gone, wondering what happened.
func (c *consoleController) reportPastFailure(ctx context.Context) {
	stateDir, err := console.StateDir()
	if err != nil {
		return
	}
	reason, ok := console.TakeFailure(stateDir)
	if !ok {
		return
	}
	c.svc.cfg.Logf("console: the last entry failed: %s", reason)

	notifier := notify.Dial()
	defer notifier.Close()
	showCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := notifier.Show(showCtx, notify.Notification{
		Summary:  "Console mode",
		Body:     "Console mode could not start, so the desktop came back. " + reason,
		Icon:     "input-gaming",
		Timeout:  15 * time.Second,
		Critical: true,
	}); err != nil {
		c.svc.cfg.Logf("console: could not report the last failure: %v", err)
	}
}

// observeControllers is called from the daemon's idle poll. Controller hotplug
// raises no Hyprland event, so there is nothing to subscribe to.
func (c *consoleController) observeControllers(ctx context.Context) {
	now := console.ConnectedControllers()

	c.mu.Lock()
	previous := c.controllers
	c.controllers = now
	byController := c.armed && c.byController
	c.mu.Unlock()

	// Switching the pad off again is the cheapest way to say no -- but only to
	// an entry the pad started. A machine with no controller sits at zero for
	// ever, so a level test here called off every countdown within one poll,
	// blaming a controller that was never there.
	if byController && isConsoleDisconnectEdge(previous, now) {
		c.cancelArmed("the controller was disconnected")
		return
	}
	if !isConsoleConnectEdge(previous, now) {
		return
	}

	base, err := config.EnsureBaseDir(c.svc.cfg.ConfigDir)
	if err != nil {
		return
	}
	cfg, err := console.LoadConfig(base)
	if err != nil || !cfg.EnterOnControllerConnect || !cfg.Configured() {
		return
	}
	if _, err := console.RuntimeDir(); err != nil {
		return
	}
	// Nothing about this was asked for out loud: a pad was switched on. So the
	// bar is the whole list, not just the hosting marker -- without a way back
	// or without gamescope, entering strands the user rather than moving them.
	entries := console.FindEntries(console.SessionDirs())
	if unmet := console.Unmet(console.Requirements(ctx, cfg, console.Systemctl{}, entries, console.ConfigPath(base))); len(unmet) > 0 {
		c.svc.cfg.Logf("console: a controller connected, but console mode is not ready (%s); not entering", strings.Join(unmet, "; "))
		return
	}
	c.arm(ctx, "A controller connected", console.TriggerGrace, true)
}

// isConsoleConnectEdge reports the transition from no controllers to some.
//
// Only the edge counts. A level test re-enters one poll after every exit, which
// is exactly what happened when this was first written: leaving the session put
// the user straight back into it, over and over, because the pad was still on.
func isConsoleConnectEdge(previous, now int) bool {
	return previous == 0 && now > 0
}

// isConsoleDisconnectEdge reports the transition from some controllers to none.
func isConsoleDisconnectEdge(previous, now int) bool {
	return previous > 0 && now == 0
}

// Arming reports whether an entry has been announced and is counting down.
func (c *consoleController) Arming() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.armed
}

// Arm announces an entry and starts the countdown. It returns straight away:
// whatever asked is about to be closed along with the rest of the desktop, so
// there is nothing useful to wait for.
//
// Nothing leads the announcement here. The trigger names who asked, which the
// log wants and the user already knows -- they are the one who asked.
func (c *consoleController) Arm(ctx context.Context, trigger string, grace time.Duration) error {
	c.svc.cfg.Logf("console: entry requested by %s", trigger)
	c.arm(ctx, "", consoleGrace(grace), false)
	return nil
}

// consoleGrace is how long the user gets. Zero means the caller had no opinion,
// which is the usual case: a panel that hard-coded a countdown would disagree
// with the daemon the day either of them changed its mind.
func consoleGrace(requested time.Duration) time.Duration {
	if requested <= 0 {
		return console.DefaultGrace
	}
	return requested
}

// arm counts down out here, in the daemon, because the countdown has to outlive
// whatever asked for it: a panel button closes its own window, and a command's
// terminal goes with the desktop.
//
// announce leads the notification when there is something worth saying about
// why this is happening; it is empty when the user asked outright. byController
// says whether switching a pad off should call this entry off again.
func (c *consoleController) arm(ctx context.Context, announce string, grace time.Duration, byController bool) {
	c.mu.Lock()
	if c.armed {
		c.mu.Unlock()
		return
	}
	armCtx, cancel := context.WithCancel(ctx)
	c.armed, c.cancel, c.calledOff, c.byController = true, cancel, "", byController
	c.mu.Unlock()

	c.svc.cfg.Logf("console: entering console mode in %s unless it is called off", grace)

	go func() {
		defer func() {
			c.mu.Lock()
			c.armed, c.cancel = false, nil
			c.mu.Unlock()
		}()

		notifier := notify.Dial()
		defer notifier.Close()

		runtimeDir, _ := console.RuntimeDir()
		if err := console.Countdown(armCtx, console.CountdownOpts{
			Grace:      grace,
			Trigger:    announce,
			Display:    c.chosenDisplay(),
			RuntimeDir: runtimeDir,
			Notifier:   notifier,
			Logf:       c.svc.cfg.Logf,
			Reason:     c.calledOffReason,
		}); err != nil {
			return
		}

		self, err := exec.LookPath("hyprmoncfg")
		if err != nil {
			c.svc.cfg.Logf("console: cannot enter, hyprmoncfg is not on PATH: %v", err)
			c.sayItFailed(armCtx, notifier, "hyprmoncfg is not on PATH")
			return
		}
		// --yes because the countdown has already happened, out here, where the
		// user could see it. It is also what keeps this from being a loop:
		// `console enter` without it comes straight back to this daemon.
		if out, err := exec.CommandContext(armCtx, self, "console", "enter", "--yes").CombinedOutput(); err != nil {
			c.svc.cfg.Logf("console: entering failed: %v: %s", err, out)
			c.sayItFailed(armCtx, notifier, firstLine(string(out)))
		}
	}()
}

// chosenDisplay names the display the console is about to take over.
//
// Read here, as the entry is announced, rather than passed in by whoever asked:
// the panel, the command line and a controller all arm the same countdown, and
// the one that matters is what the file says now. The wrapper reads the same
// file again when it actually switches, so announcing anything else would be
// promising a screen the machine is not going to use.
func (c *consoleController) chosenDisplay() string {
	base, err := config.EnsureBaseDir(c.svc.cfg.ConfigDir)
	if err != nil {
		return ""
	}
	cfg, err := console.LoadConfig(base)
	if err != nil {
		return ""
	}
	return cfg.TVName
}

// sayItFailed tells the user that the console did not start.
//
// The announcement promised the desktop was about to close, so silence after a
// failure reads as "it worked" -- and if the desktop did close before the
// failure, the user has just lost everything that was open on it and has been
// given no reason. The log line this replaces lived in a file nobody has a
// reason to know about.
func (c *consoleController) sayItFailed(ctx context.Context, notifier notify.Notifier, why string) {
	if notifier == nil {
		return
	}
	body := "Console mode could not start, so the desktop stays."
	if why = strings.TrimSpace(why); why != "" {
		body += " " + why
	}
	// A fresh context: the usual way to get here is a cancelled one, and a
	// cancelled context sends no D-Bus call.
	showCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := notifier.Show(showCtx, notify.Notification{
		Summary:  "Console mode",
		Body:     body,
		Icon:     "input-gaming",
		Timeout:  10 * time.Second,
		Critical: true,
	}); err != nil {
		c.svc.cfg.Logf("console: could not report the failure: %v", err)
	}
}

// firstLine keeps a notification to the sentence that matters: command output
// carries usage text and stack-shaped detail that a popup cannot hold.
func firstLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// cancelArmed stops a pending entry. The countdown does the saying-so, because
// it owns the notification and can replace it rather than stack a second one on
// top.
func (c *consoleController) cancelArmed(why string) bool {
	c.mu.Lock()
	cancel, armed := c.cancel, c.armed
	if armed && cancel != nil {
		c.calledOff = why
	}
	c.mu.Unlock()
	if !armed || cancel == nil {
		return false
	}
	cancel()
	return true
}

func (c *consoleController) calledOffReason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calledOff
}
