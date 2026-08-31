package console

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A wrapper wired for tests: nothing is started, every launch is recorded, and
// a launch can leave a switch request behind the way a real one would.
func testWrapper(t *testing.T, launched *[]string, onLaunch func(argv []string)) *Wrapper {
	t.Helper()
	return &Wrapper{
		DesktopExec:        []string{"desktop-compositor"},
		ConsoleExec:        []string{"start-gamescope-session"},
		ConsoleSessionName: "gamescope-session",
		StateDir:           t.TempDir(),
		RuntimeDir:         t.TempDir(),
		Systemctl:          &fakeRunner{},
		ShortRun:           time.Nanosecond, // no run is "short" in a test
		ShortRunLimit:      2,
		Launch: func(_ context.Context, argv []string, _ []string) error {
			*launched = append(*launched, argv[0])
			if onLaunch != nil {
				onLaunch(argv)
			}
			return nil
		},
	}
}

// The desktop is what starts when nobody asked for anything else, and a
// compositor exiting with no request pending is an ordinary logout.
func TestWrapperStartsTheDesktopAndEndsOnLogout(t *testing.T) {
	var launched []string
	w := testWrapper(t, &launched, nil)
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(launched) != 1 || launched[0] != "desktop-compositor" {
		t.Fatalf("launched %v, want one desktop session", launched)
	}
}

// The whole point: desktop, console, desktop, all inside one login session.
func TestWrapperSwitchesBothWaysWithinOneSession(t *testing.T) {
	var launched []string
	var w *Wrapper
	steps := []Mode{ModeConsole, ModeDesktop}
	w = testWrapper(t, &launched, func([]string) {
		if len(steps) == 0 {
			return
		}
		if err := Request(w.RuntimeDir, steps[0]); err != nil {
			t.Error(err)
		}
		steps = steps[1:]
	})
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"desktop-compositor", "start-gamescope-session", "desktop-compositor"}
	if strings.Join(launched, ",") != strings.Join(want, ",") {
		t.Fatalf("launched %v, want %v", launched, want)
	}
}

// The request the wrapper starts with is honoured, so `console enter` followed
// by the compositor exiting lands in the console rather than back on the
// desktop.
func TestWrapperHonoursAPendingRequestOnStartup(t *testing.T) {
	var launched []string
	w := testWrapper(t, &launched, nil)
	if err := Request(w.RuntimeDir, ModeConsole); err != nil {
		t.Fatal(err)
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(launched) == 0 || launched[0] != "start-gamescope-session" {
		t.Fatalf("launched %v, want the console first", launched)
	}
}

// Without this the loop spins forever on a compositor that cannot start, and the
// user never gets a screen at all.
func TestWrapperGivesUpAfterRepeatedInstantExits(t *testing.T) {
	var launched []string
	var w *Wrapper
	w = testWrapper(t, &launched, func([]string) {
		// Always ask for another one, so only the guard can end this.
		if err := Request(w.RuntimeDir, ModeDesktop); err != nil {
			t.Error(err)
		}
	})
	w.ShortRun = time.Hour // every run counts as instant
	w.ShortRunLimit = 3

	done := make(chan error, 1)
	go func() { done <- w.Run(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wrapper never gave up; it would spin forever on a broken compositor")
	}
	if len(launched) != 3 {
		t.Fatalf("launched %d sessions, want the limit of 3: %v", len(launched), launched)
	}
}

// A run that lasted resets the count, so an afternoon of playing followed by one
// bad start does not end the session.
func TestWrapperForgivesAnInstantExitAfterARealSession(t *testing.T) {
	var launched []string
	var w *Wrapper
	long := true
	w = testWrapper(t, &launched, func([]string) {
		if long {
			time.Sleep(20 * time.Millisecond)
			long = false
		}
		if len(launched) < 3 {
			if err := Request(w.RuntimeDir, ModeDesktop); err != nil {
				t.Error(err)
			}
		}
	})
	w.ShortRun = 10 * time.Millisecond
	w.ShortRunLimit = 2

	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(launched) != 3 {
		t.Fatalf("launched %v, want the long run to have reset the count", launched)
	}
}

// Falling back beats ending the session: a machine with no gamescope session
// installed should land on the desktop, not on a black screen.
func TestWrapperFallsBackToTheDesktopWhenTheConsoleCannotStart(t *testing.T) {
	var launched []string
	w := testWrapper(t, &launched, nil)
	w.ConsoleExec = nil
	if err := Request(w.RuntimeDir, ModeConsole); err != nil {
		t.Fatal(err)
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(launched) != 1 || launched[0] != "desktop-compositor" {
		t.Fatalf("launched %v, want a fallback to the desktop", launched)
	}
}

// There has to be a way back, so refusing up front beats hosting a session that
// can never return to a desktop.
func TestWrapperRefusesWithoutADesktopCommand(t *testing.T) {
	w := &Wrapper{RuntimeDir: t.TempDir(), StateDir: t.TempDir(), Systemctl: &fakeRunner{}}
	if err := w.Run(context.Background()); err == nil {
		t.Fatal("a wrapper with no way back must refuse to start")
	}
}

// The console needs to identify itself as gamescope, or the portals and the
// session's own units look at XDG_CURRENT_DESKTOP and load the wrong things.
func TestConsoleRunsWithTheGamescopeIdentity(t *testing.T) {
	var env []string
	w := &Wrapper{
		DesktopExec: []string{"desktop"}, ConsoleExec: []string{"console"},
		ConsoleSessionName: "gamescope-session",
		StateDir:           t.TempDir(), RuntimeDir: t.TempDir(), Systemctl: &fakeRunner{},
		ShortRun: time.Nanosecond,
		Launch: func(_ context.Context, _ []string, extra []string) error {
			env = append(env, extra...)
			return nil
		},
	}
	if err := Request(w.RuntimeDir, ModeConsole); err != nil {
		t.Fatal(err)
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, " ")
	for _, want := range []string{"XDG_CURRENT_DESKTOP=gamescope", "DESKTOP_SESSION=gamescope-session"} {
		if !strings.Contains(joined, want) {
			t.Errorf("console environment %v is missing %q", env, want)
		}
	}
}

// Big Picture's own "Switch to Desktop" stops the gamescope target and leaves
// no request behind. Treating that as a logout would drop the user at a
// greeter -- nobody logs out *from* a console, so leaving one means going home.
func TestConsoleExitWithNoRequestReturnsToTheDesktop(t *testing.T) {
	var launched []string
	w := testWrapper(t, &launched, nil)
	if err := Request(w.RuntimeDir, ModeConsole); err != nil {
		t.Fatal(err)
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"start-gamescope-session", "desktop-compositor"}
	if strings.Join(launched, ",") != strings.Join(want, ",") {
		t.Fatalf("launched %v, want the console to hand back to the desktop", launched)
	}
}
