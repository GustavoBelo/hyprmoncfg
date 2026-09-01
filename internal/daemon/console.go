package daemon

import (
	"context"
	"os/exec"
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
	runtimeDir, err := console.RuntimeDir()
	if err != nil {
		return
	}
	// Without a hosting session there is no way back, so entering would strand
	// the user rather than move them.
	if !console.Hosted(runtimeDir) {
		c.svc.cfg.Logf("console: a controller connected, but this session is not hosted; not entering")
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
			return
		}
		// --yes because the countdown has already happened, out here, where the
		// user could see it. It is also what keeps this from being a loop:
		// `console enter` without it comes straight back to this daemon.
		if out, err := exec.CommandContext(armCtx, self, "console", "enter", "--yes").CombinedOutput(); err != nil {
			c.svc.cfg.Logf("console: entering failed: %v: %s", err, out)
		}
	}()
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
