package couch

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"syscall"
	"time"

	"github.com/crmne/hyprmoncfg/internal/hypr"
)

const (
	steamDetectTimeout = 90 * time.Second
	// BigPictureWaitWindow bounds the wait for the window to appear. It only
	// runs out on a genuinely cold Steam: detection is polled twice a second
	// and returns the moment the window is recognised.
	BigPictureWaitWindow = 120 * time.Second
	pollInterval         = 500 * time.Millisecond
	// A Big Picture window fills its output almost completely and renders
	// without decorations, but on some setups it keeps a plain localized
	// title ("Steam"), so geometry is the only reliable tell there.
	fullscreenCoverageRatio = 0.85
)

// WindowSource is the slice of the Hyprland client the couch package needs,
// kept as an interface so detection can be tested against fixtures.
type WindowSource interface {
	Clients(ctx context.Context) ([]hypr.Window, error)
	Monitors(ctx context.Context) ([]hypr.Monitor, error)
}

// WindowCloser adds window dispatching to WindowSource for app cleanup and
// closing Big Picture.
type WindowCloser interface {
	WindowSource
	Dispatch(ctx context.Context, dispatcher string, args ...string) error
}

// Confidence ranks how sure a tell is that a window really is the Steam
// Gamepad UI, as opposed to the ordinary desktop Steam window.
//
// The distinction matters because the two uses pull in opposite directions.
// Tracking a session we started ourselves can afford a weak tell: the worst
// case is watching the wrong Steam window until it closes. Entering couch mode
// on our own must not — a false positive there yanks the user onto the TV
// because they opened their library.
type Confidence int

const (
	NotBigPicture Confidence = iota
	// LikelyBigPicture: a Steam window that appeared after we asked for Big
	// Picture, or one that covers a whole output.
	LikelyBigPicture
	// CertainBigPicture: the window names itself as the Gamepad UI, or is
	// genuinely fullscreen.
	CertainBigPicture
)

var (
	// Anything that could belong to Steam at all.
	steamishRe = regexp.MustCompile(`(?i)steam|gamepadui|big.?picture`)
	// The Gamepad UI naming itself.
	//
	// On Omarchy this is the only tell that survives, and it is why detection
	// failed for every session in the log. /usr/share/omarchy/default/hypr/apps/
	// steam.lua applies `o.window("steam", { float = true })` plus a 1100x700
	// rule for `class=steam, title=Steam`, so Big Picture comes up floating at
	// 1100x700 with fullscreen == 0. Neither the fullscreen tell nor the
	// coverage tell can fire, and `steam://open/bigpicture` reuses the existing
	// window so the "new window" tell cannot either. The title, which turns
	// into "Steam Big Picture Mode" shortly after the window maps, is all
	// that is left.
	gamepadUIRe = regexp.MustCompile(`(?i)gamepadui|big.?picture`)
)

// IsBigPictureWindow reports whether a window names itself as the Gamepad UI.
// Both the class and the title are consulted so that a browser tab about Big
// Picture cannot match.
func IsBigPictureWindow(class, title string) bool {
	if !steamishRe.MatchString(class) {
		return false
	}
	return gamepadUIRe.MatchString(title)
}

// isSteamWindow gates on the class alone, and on both spellings of it: Steam
// builds disagree about which one is set, and a window can lose its class
// before its initialClass.
//
// The title must never be part of this gate. A browser tab titled "Steam Big
// Picture Mode - Google" would otherwise pass it and then satisfy the title
// tell, closing the loop on itself.
func isSteamWindow(w hypr.Window) bool {
	return steamishRe.MatchString(w.Class) || steamishRe.MatchString(w.InitialClass)
}

// titles joins both title fields. Steam builds disagree about which one carries
// the Gamepad UI marker, and a window that opened as "Steam" keeps that as its
// initialTitle once the live title moves on to the running game.
func titles(w hypr.Window) string {
	return w.Title + " " + w.InitialTitle
}

// coversMonitor reports whether a window spans most of one output, which is
// how Big Picture presents itself when the title gives nothing away.
func coversMonitor(w hypr.Window, monitors []hypr.Monitor) bool {
	if w.Width() <= 0 || w.Height() <= 0 {
		return false
	}
	monStr := string(w.Monitor)
	for _, m := range monitors {
		if m.Name != monStr && fmt.Sprintf("%d", m.ID) != monStr {
			continue
		}
		if m.Width <= 0 || m.Height <= 0 {
			continue
		}
		area := float64(m.Width) * float64(m.Height)
		windowArea := float64(w.Width()) * float64(w.Height())
		return windowArea >= fullscreenCoverageRatio*area
	}
	return false
}

// BigPictureDetector finds the Gamepad UI window and says how sure it is. The
// snapshot of pre-existing Steam windows must be taken before Steam is asked
// for Big Picture, otherwise the "new window" tell is meaningless.
type BigPictureDetector struct {
	Source WindowSource
	Known  map[string]bool
}

func NewBigPictureDetector(ctx context.Context, source WindowSource) *BigPictureDetector {
	detector := &BigPictureDetector{Source: source, Known: make(map[string]bool)}
	if windows, err := source.Clients(ctx); err == nil {
		for _, w := range windows {
			if isSteamWindow(w) {
				detector.Known[w.Address] = true
			}
		}
	}
	return detector
}

// Classify ranks one window. Monitors are only queried for the coverage tell,
// so callers pass nil when they do not need it.
func (d *BigPictureDetector) Classify(w hypr.Window, monitors []hypr.Monitor) Confidence {
	if !isSteamWindow(w) {
		return NotBigPicture
	}
	// `fullscreen` and `fullscreenClient` are integers in hyprctl (0 = none),
	// not booleans; comparing them against true never matches.
	if gamepadUIRe.MatchString(titles(w)) || w.Fullscreen > 0 {
		return CertainBigPicture
	}
	if !d.Known[w.Address] || coversMonitor(w, monitors) {
		return LikelyBigPicture
	}
	return NotBigPicture
}

func (d *BigPictureDetector) find(ctx context.Context, min Confidence) []hypr.Window {
	windows, err := d.Source.Clients(ctx)
	if err != nil {
		return nil
	}
	var monitors []hypr.Monitor
	monitorsLoaded := false
	matched := make([]hypr.Window, 0, 2)
	for _, w := range windows {
		// Only the coverage tell needs monitors, and only for windows that got
		// that far, so the query stays off the common path.
		if !monitorsLoaded && d.Source != nil && isSteamWindow(w) {
			monitors, _ = d.Source.Monitors(ctx)
			monitorsLoaded = true
		}
		if d.Classify(w, monitors) >= min {
			matched = append(matched, w)
		}
	}
	return matched
}

// Windows returns every window that reaches at least LikelyBigPicture. Use
// CertainWindows to decide whether to enter couch mode on our own.
func (d *BigPictureDetector) Windows(ctx context.Context) []hypr.Window {
	return d.find(ctx, LikelyBigPicture)
}

func (d *BigPictureDetector) CertainWindows(ctx context.Context) []hypr.Window {
	return d.find(ctx, CertainBigPicture)
}

func (d *BigPictureDetector) Count(ctx context.Context) int {
	return len(d.find(ctx, LikelyBigPicture))
}

// CertainCount is what an automatic trigger looks at: opening the Steam library
// must never be enough to drag the user onto the TV.
func (d *BigPictureDetector) CertainCount(ctx context.Context) int {
	return len(d.find(ctx, CertainBigPicture))
}

func WaitForBigPicture(ctx context.Context, detector *BigPictureDetector, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if detector.Count(ctx) > 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(pollInterval):
		}
	}
}

func CloseBigPicture(ctx context.Context, client WindowCloser, detector *BigPictureDetector) int {
	closed := 0
	for _, w := range detector.Windows(ctx) {
		if err := client.Dispatch(ctx, "closewindow", "address:"+w.Address); err == nil {
			closed++
		}
	}
	return closed
}

type BigPictureLauncher struct {
	Command string
	Args    []string
}

// ResolveLauncher picks how to bring Big Picture up. An already-running Steam
// ignores -gamepadui from a second invocation, so in that case Big Picture is
// requested through the steam:// protocol, which the live instance handles.
func ResolveLauncher() (BigPictureLauncher, bool, error) {
	if path, err := exec.LookPath("bazzite-steam-bpm"); err == nil {
		return BigPictureLauncher{Command: path}, false, nil
	}
	path, err := exec.LookPath("steam")
	if err != nil {
		return BigPictureLauncher{}, false, err
	}
	if len(livePIDs()) > 0 {
		return BigPictureLauncher{Command: path, Args: []string{"steam://open/bigpicture"}}, true, nil
	}
	return BigPictureLauncher{Command: path, Args: []string{"-gamepadui"}}, false, nil
}

func LaunchBigPicture(launcher BigPictureLauncher) (*exec.Cmd, error) {
	cmd := exec.Command(launcher.Command, launcher.Args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd, cmd.Start()
}

func WaitForNewSteamPID(known map[int]struct{}, timeout time.Duration) (int, bool) {
	deadline := time.Now().Add(timeout)
	for {
		for _, pid := range livePIDs() {
			if _, seen := known[pid]; !seen {
				return pid, true
			}
		}
		if time.Now().After(deadline) {
			return 0, false
		}
		time.Sleep(time.Second)
	}
}

// LatestSteamPID returns the newest running Steam process, used as a fallback
// target when no new PID appears because Steam was already open.
func LatestSteamPID() int {
	pids := livePIDs()
	if len(pids) == 0 {
		return 0
	}
	return pids[len(pids)-1]
}

func KnownSteamPIDs() map[int]struct{} {
	known := make(map[int]struct{})
	for _, pid := range livePIDs() {
		known[pid] = struct{}{}
	}
	return known
}

// existingInstanceGrace bounds the look for an already-running Steam. Its PID
// is expected to be there immediately; this only covers Steam restarting itself
// at the moment we looked.
const existingInstanceGrace = 10 * time.Second

// ResolveSteamPID finds the Steam process to watch for the rest of the session.
//
// When Steam is already up it handles steam://open/bigpicture itself and the
// launcher exits, so no new PID ever appears. Waiting for one anyway burned the
// full steamDetectTimeout before detection even began: the session log shows
// "launched" at 20:13:26 and "no new Steam process" at 20:14:57, 91 seconds
// later, with the Big Picture window already on screen the whole time.
func ResolveSteamPID(existingInstance bool, known map[int]struct{}, stateDir string) int {
	if existingInstance {
		if pid, found := waitForAnySteamPID(existingInstanceGrace); found {
			AppendLog(stateDir, "play: Steam was already running; watching PID %d", pid)
			return pid
		}
		// Steam went away between resolving the launcher and now. Fall through
		// to the long wait, which is the right behaviour for a cold start.
		AppendLog(stateDir, "play: the running Steam disappeared; waiting for a new one")
	}
	if pid, found := WaitForNewSteamPID(known, steamDetectTimeout); found {
		AppendLog(stateDir, "play: Steam detected (PID %d)", pid)
		return pid
	}
	if pid := LatestSteamPID(); pid > 0 {
		AppendLog(stateDir, "play: no new Steam process; watching existing instance (PID %d)", pid)
		return pid
	}
	AppendLog(stateDir, "play: could not detect a Steam process after launch")
	return 0
}

// livePIDs is the only door to /proc in this file, so tests can stand in a
// fake Steam. The paths below decide how long a play session stares at a blank
// TV, and were untestable while they read the real process table.
var livePIDs = func() []int { return SteamPIDs(true) }

func waitForAnySteamPID(timeout time.Duration) (int, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if pid := LatestSteamPID(); pid > 0 {
			return pid, true
		}
		if time.Now().After(deadline) {
			return 0, false
		}
		time.Sleep(pollInterval)
	}
}
