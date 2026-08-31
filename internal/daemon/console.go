package daemon

import (
	"context"
	"os/exec"
	"sync"
	"time"

	"github.com/crmne/hyprmoncfg/internal/config"
	"github.com/crmne/hyprmoncfg/internal/console"
)

// consoleController turns a controller being switched on into a console
// session.
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
	// armed is the pending entry, cancelled by unplugging the pad again or by
	// `hyprmoncfg console cancel`.
	armed  bool
	cancel context.CancelFunc
}

// consoleGrace is how long the user has to change their mind. It is generous on
// purpose: the notification is the only warning, and the cost of missing it is
// a desktop closed out from under them.
const consoleGrace = 20 * time.Second

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
	armed := c.armed
	c.mu.Unlock()

	// Unplugging again while the countdown runs is the cheapest way to say no.
	if now == 0 && armed {
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
	c.arm(ctx)
}

// isConsoleConnectEdge reports the transition from no controllers to some.
//
// Only the edge counts. A level test re-enters one poll after every exit, which
// is exactly what happened when this was first written: leaving the session put
// the user straight back into it, over and over, because the pad was still on.
func isConsoleConnectEdge(previous, now int) bool {
	return previous == 0 && now > 0
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
func (c *consoleController) Arm(ctx context.Context, trigger string) error {
	c.svc.cfg.Logf("console: entry requested by %s", trigger)
	c.arm(ctx)
	return nil
}

func (c *consoleController) arm(ctx context.Context) {
	c.mu.Lock()
	if c.armed {
		c.mu.Unlock()
		return
	}
	armCtx, cancel := context.WithCancel(ctx)
	c.armed, c.cancel = true, cancel
	c.mu.Unlock()

	c.svc.cfg.Logf("console: a controller connected; entering console mode in %s", consoleGrace)
	notify("Console mode", "A controller connected. Entering console mode in 20 seconds.")

	go func() {
		defer func() {
			c.mu.Lock()
			c.armed, c.cancel = false, nil
			c.mu.Unlock()
		}()
		deadline := time.After(consoleGrace)
		poll := time.NewTicker(time.Second)
		defer poll.Stop()
		for waiting := true; waiting; {
			select {
			case <-armCtx.Done():
				return
			case <-poll.C:
				if runtimeDir, err := console.RuntimeDir(); err == nil && console.TakeCancel(runtimeDir) {
					c.svc.cfg.Logf("console: entry cancelled")
					notify("Console mode", "Entering console mode was cancelled.")
					return
				}
			case <-deadline:
				waiting = false
			}
		}
		self, err := exec.LookPath("hyprmoncfg")
		if err != nil {
			c.svc.cfg.Logf("console: cannot enter, hyprmoncfg is not on PATH: %v", err)
			return
		}
		// --yes because the countdown has already happened, out here, where the
		// user could see it.
		if out, err := exec.CommandContext(armCtx, self, "console", "enter", "--yes").CombinedOutput(); err != nil {
			c.svc.cfg.Logf("console: entering failed: %v: %s", err, out)
		}
	}()
}

func (c *consoleController) cancelArmed(why string) bool {
	c.mu.Lock()
	cancel, armed := c.cancel, c.armed
	c.mu.Unlock()
	if !armed || cancel == nil {
		return false
	}
	cancel()
	c.svc.cfg.Logf("console: entry %s", why)
	notify("Console mode", "Entering console mode was "+why+".")
	return true
}

// notify tells the user something is about to happen to their session. It is
// best effort: a machine with no notification daemon still gets the log line,
// and the daemon has no business failing over a missing helper.
func notify(title, body string) {
	path, err := exec.LookPath("notify-send")
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, path, "-a", "hyprmoncfg", title, body).Run()
}
