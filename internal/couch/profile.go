package couch

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/crmne/hyprmoncfg/internal/hypr"
	"github.com/crmne/hyprmoncfg/internal/profile"
)

// ConsoleProfileName is the reserved profile the couch mode generates and owns.
// It is deliberately not one of the user's own profiles: picking two existing
// ones by hand is how a session ended up logged as
// "play: starting (TV=escritório desk=game)", with the roles swapped.
const ConsoleProfileName = "couch"

// ManagedByCouch marks the generated profile so the daemon's automatic matching
// skips it and the Profiles tab hides it.
const ManagedByCouch = "couch"

// ConsoleLayout is everything a user may decide about the console layout.
//
// The set is deliberately small. Every field outside it -- position, transform,
// scale, bit depth, luminance -- is derived, because those are the ones that
// turn a TV that works into a black screen you cannot fix with a controller in
// your hand.
type ConsoleLayout struct {
	// TVKey is the hardware identity of the display to play on.
	TVKey string `json:"tv_key"`
	// TVName is the connector last seen for TVKey, kept only so the UI can
	// name it before monitors are resolved.
	TVName string `json:"tv_name,omitempty"`
	// Mode must be one the TV actually reports.
	Mode string `json:"mode"`
	HDR  bool   `json:"hdr"`
	VRR  bool   `json:"vrr"`
	// Desk says what happens to the other displays during a session.
	Desk DeskDuringCouch `json:"desk"`
}

// modeRe matches the mode strings hyprctl reports, e.g. "2560x1440@120.00Hz".
var modeRe = regexp.MustCompile(`^(\d+)x(\d+)@([0-9.]+)(?:Hz)?$`)

type parsedMode struct {
	width   int
	height  int
	refresh float64
}

func parseMode(mode string) (parsedMode, bool) {
	m := modeRe.FindStringSubmatch(strings.TrimSpace(mode))
	if m == nil {
		return parsedMode{}, false
	}
	width, err := strconv.Atoi(m[1])
	if err != nil {
		return parsedMode{}, false
	}
	height, err := strconv.Atoi(m[2])
	if err != nil {
		return parsedMode{}, false
	}
	refresh, err := strconv.ParseFloat(m[3], 64)
	if err != nil {
		return parsedMode{}, false
	}
	if width <= 0 || height <= 0 || refresh <= 0 {
		return parsedMode{}, false
	}
	return parsedMode{width: width, height: height, refresh: refresh}, true
}

// AvailableModes lists the modes a display reports, best first.
//
// hyprctl answers this even for a disabled output -- the TV on this host is off
// and still reports 65 modes -- so the picker is populated before the user has
// ever switched to it.
func AvailableModes(m hypr.Monitor) []string {
	seen := make(map[string]parsedMode, len(m.AvailableModes)+1)
	for _, raw := range m.AvailableModes {
		if parsed, ok := parseMode(raw); ok {
			seen[formatMode(parsed)] = parsed
		}
	}
	// A disabled output reports 0x0; never offer that as a mode.
	if current, ok := parseMode(m.ModeString()); ok {
		seen[formatMode(current)] = current
	}

	modes := make([]string, 0, len(seen))
	for name := range seen {
		modes = append(modes, name)
	}
	sort.Slice(modes, func(i, j int) bool {
		a, b := seen[modes[i]], seen[modes[j]]
		if a.width != b.width {
			return a.width > b.width
		}
		if a.height != b.height {
			return a.height > b.height
		}
		return a.refresh > b.refresh
	})
	return modes
}

func formatMode(p parsedMode) string {
	return fmt.Sprintf("%dx%d@%.2fHz", p.width, p.height, p.refresh)
}

// monitorByKey resolves a hardware key against the live displays.
func monitorByKey(monitors []hypr.Monitor, key string) (hypr.Monitor, bool) {
	for _, m := range monitors {
		if m.HardwareKey() == key {
			return m, true
		}
	}
	return hypr.Monitor{}, false
}

// SuggestConsoleLayout proposes a starting point: the TV is the first HDMI
// output, or failing that the largest display that is not the internal panel.
//
// Choosing wrongly here is cheap because the user reviews it before the first
// session, but it must be deterministic -- picking the desk monitor as "the TV"
// would blank the screen the user is looking at.
// DisplayFacts are the things only the kernel knows about a display: whether it
// really does HDR, and what its native resolution is. They are passed in rather
// than read here so the choice is reproducible in a test.
type DisplayFacts struct {
	// HDRCapable is keyed by connector name.
	HDRCapable map[string]bool
	// PreferredResolution is the EDID-preferred mode per connector, "3840x2160".
	PreferredResolution map[string]string
}

// LiveDisplayFacts reads them from the kernel.
func LiveDisplayFacts() DisplayFacts {
	return DisplayFacts{
		HDRCapable:          hypr.HDRCapableConnectors(),
		PreferredResolution: hypr.PreferredModes(),
	}
}

func SuggestConsoleLayout(monitors []hypr.Monitor, facts DisplayFacts) (ConsoleLayout, error) {
	candidate, ok := suggestTV(monitors)
	if !ok {
		return ConsoleLayout{}, fmt.Errorf("no display to use as the TV; connect one and try again")
	}

	mode, ok := suggestMode(candidate, facts.PreferredResolution[candidate.Name])
	if !ok {
		return ConsoleLayout{}, fmt.Errorf("display %s reports no usable mode", candidate.Name)
	}

	return ConsoleLayout{
		TVKey:  candidate.HardwareKey(),
		TVName: candidate.Name,
		Mode:   mode,
		HDR:    facts.HDRCapable[candidate.Name],
		VRR:    true,
		Desk:   DeskDisabled,
	}, nil
}

// highRefreshHz is the bar a console mode should clear. 100 covers 100, 120 and
// 144 Hz panels while leaving 60 Hz-only displays on the fallback path.
const highRefreshHz = 100

// suggestMode picks what a console should start at: the largest picture that
// still runs at a high refresh rate, in the display's native shape.
//
// Neither half of that is optional. Taking the largest mode outright picks
// 4096x2160@120 on the development host's Samsung -- a 17:9 cinema mode that
// letterboxes a 16:9 picture, above the panel's native 3840x2160, which itself
// only reaches 60 Hz. Taking the highest refresh outright drops to
// 1920x1080@144 to gain 24 Hz over a far bigger picture. Ranking by resolution
// among the modes that clear the refresh bar yields 2560x1440@120, which is
// what the display's owner had already picked by hand.
func suggestMode(m hypr.Monitor, preferredResolution string) (string, bool) {
	modes := AvailableModes(m)
	if len(modes) == 0 {
		return "", false
	}

	native, hasNative := aspectOf(preferredResolution)
	best, bestParsed := "", parsedMode{}
	fallback, fallbackParsed := "", parsedMode{}

	for _, name := range modes {
		parsed, ok := parseMode(name)
		if !ok {
			continue
		}
		if hasNative && !sameAspect(float64(parsed.width)/float64(parsed.height), native) {
			continue
		}
		if betterMode(parsed, fallbackParsed, fallback == "") {
			fallback, fallbackParsed = name, parsed
		}
		if parsed.refresh < highRefreshHz {
			continue
		}
		if betterMode(parsed, bestParsed, best == "") {
			best, bestParsed = name, parsed
		}
	}

	switch {
	case best != "":
		return best, true
	case fallback != "":
		// Nothing clears the refresh bar, so take the biggest picture the
		// display can show at the best rate it has.
		return fallback, true
	default:
		// Nothing matched the native shape; fall back to the ranking
		// AvailableModes already applies.
		return modes[0], true
	}
}

// betterMode prefers more pixels, then more frames.
func betterMode(candidate, current parsedMode, currentEmpty bool) bool {
	if currentEmpty {
		return true
	}
	candidatePixels := candidate.width * candidate.height
	currentPixels := current.width * current.height
	if candidatePixels != currentPixels {
		return candidatePixels > currentPixels
	}
	return candidate.refresh > current.refresh
}

// aspectOf reads "3840x2160" as reported by DRM sysfs, which carries no refresh.
func aspectOf(resolution string) (float64, bool) {
	width, height, ok := strings.Cut(strings.TrimSpace(resolution), "x")
	if !ok {
		return 0, false
	}
	w, err := strconv.Atoi(width)
	if err != nil || w <= 0 {
		return 0, false
	}
	h, err := strconv.Atoi(height)
	if err != nil || h <= 0 {
		return 0, false
	}
	return float64(w) / float64(h), true
}

// sameAspect tolerates the rounding in modes like 1366x768, while still telling
// 16:9 (1.778) apart from 17:9 (1.896).
func sameAspect(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 0.03
}

func suggestTV(monitors []hypr.Monitor) (hypr.Monitor, bool) {
	var best hypr.Monitor
	found := false
	for _, m := range monitors {
		if m.IsInternal() {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(m.Name), "HDMI") {
			// An HDMI output is the strongest signal there is; take the first
			// one in connector order so the answer never depends on which
			// display happens to be awake.
			if !found || !strings.HasPrefix(strings.ToUpper(best.Name), "HDMI") || m.Name < best.Name {
				best, found = m, true
			}
			continue
		}
		if found && strings.HasPrefix(strings.ToUpper(best.Name), "HDMI") {
			continue
		}
		if !found || pixelCount(m) > pixelCount(best) {
			best, found = m, true
		}
	}
	return best, found
}

func pixelCount(m hypr.Monitor) int {
	if best := AvailableModes(m); len(best) > 0 {
		if parsed, ok := parseMode(best[0]); ok {
			return parsed.width * parsed.height
		}
	}
	return m.Width * m.Height
}

// ValidateConsoleLayout rejects any choice that would leave a session
// unplayable or unrecoverable. It is the first of two nets; the second is the
// apply engine's timed revert, which catches what no validation can -- a mode
// the display accepts but the cable cannot carry.
func ValidateConsoleLayout(layout ConsoleLayout, monitors []hypr.Monitor) error {
	if strings.TrimSpace(layout.TVKey) == "" {
		return fmt.Errorf("no TV display selected")
	}
	tv, ok := monitorByKey(monitors, layout.TVKey)
	if !ok {
		return fmt.Errorf("the TV display (%s) is not connected", displayLabel(layout))
	}
	modes := AvailableModes(tv)
	if len(modes) == 0 {
		return fmt.Errorf("display %s reports no usable mode", tv.Name)
	}
	if !containsFold(modes, layout.Mode) {
		return fmt.Errorf("%s cannot do %s; pick one of the modes it reports", tv.Name, layout.Mode)
	}
	switch layout.Desk {
	case DeskDisabled, DeskEnabled, DeskMirror:
	default:
		return fmt.Errorf("unknown desk behaviour %q", layout.Desk)
	}
	if layout.Desk == DeskMirror && len(monitors) < 2 {
		return fmt.Errorf("mirroring needs a second display")
	}
	return nil
}

func displayLabel(layout ConsoleLayout) string {
	if layout.TVName != "" {
		return layout.TVName
	}
	return layout.TVKey
}

func containsFold(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

// BuildConsoleProfile turns a validated layout into the profile a session
// applies.
//
// The invariants are enforced here rather than checked afterwards, so an
// invalid console profile cannot be represented:
//
//   - the TV is always enabled, and sits at 0,0;
//   - transform is always 0 -- a rotated TV breaks Big Picture's input mapping;
//   - the TV runs at scale 1, because Big Picture does its own scaling and any
//     other factor makes it resample twice and go soft;
//   - the desk is placed to the right of the TV's logical width, so the two can
//     never overlap;
//   - at least one output stays enabled.
func BuildConsoleProfile(layout ConsoleLayout, monitors []hypr.Monitor) (profile.Profile, error) {
	if err := ValidateConsoleLayout(layout, monitors); err != nil {
		return profile.Profile{}, err
	}
	tv, _ := monitorByKey(monitors, layout.TVKey)
	mode, _ := parseMode(layout.Mode)

	tvOutput := outputFor(tv)
	tvOutput.Enabled = true
	tvOutput.Mode = formatMode(mode)
	tvOutput.Width = mode.width
	tvOutput.Height = mode.height
	tvOutput.Refresh = mode.refresh
	tvOutput.X = 0
	tvOutput.Y = 0
	tvOutput.Scale = 1
	tvOutput.Transform = 0
	tvOutput.MirrorOf = ""
	tvOutput.VRR = vrrMode(layout.VRR)
	tvOutput.CM = colorPreset(layout.HDR)

	outputs := []profile.OutputConfig{tvOutput}

	for _, m := range monitors {
		if m.HardwareKey() == layout.TVKey {
			continue
		}
		outputs = append(outputs, deskOutput(m, layout, mode))
	}

	p := profile.New(ConsoleProfileName, outputs)
	p.ManagedBy = ManagedByCouch
	// Workspace placement is a session action, not a profile field: pinning
	// rules here would fight whatever workspace layout the user runs on the
	// desktop, and would still not decide where Steam opens. The session
	// focuses the TV before launching instead.
	p.Workspaces = profile.WorkspaceSettings{Enabled: false}
	return p, nil
}

func deskOutput(m hypr.Monitor, layout ConsoleLayout, tvMode parsedMode) profile.OutputConfig {
	out := outputFor(m)
	out.Transform = 0
	out.MirrorOf = ""

	switch layout.Desk {
	case DeskDisabled:
		out.Enabled = false
		return out

	case DeskMirror:
		out.Enabled = true
		out.MirrorOf = layout.TVKey
		out.X = 0
		out.Y = 0
		out.Scale = 1
		// A mirror has to run a mode the source also has; fall back to the TV's
		// own mode and let Hyprland scale when the panel cannot match it.
		if containsFold(AvailableModes(m), formatMode(tvMode)) {
			out.Mode = formatMode(tvMode)
			out.Width, out.Height, out.Refresh = tvMode.width, tvMode.height, tvMode.refresh
		}
		return out

	default: // DeskEnabled
		out.Enabled = true
		out.Scale = 1
		// Beside the TV, never on top of it. Positions are derived precisely so
		// an edit cannot produce overlapping outputs.
		out.X = tvMode.width
		out.Y = 0
		return out
	}
}

// outputFor seeds an output from the live display so identity, colour and
// luminance carry over untouched.
func outputFor(m hypr.Monitor) profile.OutputConfig {
	seed := profile.FromMonitors(ConsoleProfileName, []hypr.Monitor{m})
	if len(seed.Outputs) == 1 {
		return seed.Outputs[0]
	}
	return profile.OutputConfig{
		Key:      m.HardwareKey(),
		MatchKey: m.HardwareKey(),
		Name:     m.Name,
		Enabled:  !m.Disabled,
		Scale:    1,
	}
}

func vrrMode(on bool) int {
	if on {
		// 1 is "always on". Hyprland's 2 ("fullscreen only") sounds right for
		// games but flips modes mid-session on some panels.
		return 1
	}
	return 0
}

func colorPreset(hdr bool) string {
	if hdr {
		return "hdr"
	}
	return "auto"
}

// ModeMatchesPanelShape reports whether a chosen mode has the display's own
// aspect ratio, and names the native resolution when it does not.
//
// The mode list a TV advertises is not a list of good choices. This Samsung
// offers 4096x2160, which is 17:9 cinema on a 16:9 panel: picking it as "the
// biggest number" gets a letterboxed picture with black bars, and it is an easy
// mistake because it sorts above 3840x2160. The generator avoids it already;
// this is for the editor, where the user may pick any mode the display lists.
func ModeMatchesPanelShape(mode string, m hypr.Monitor, facts DisplayFacts) (native string, ok bool) {
	preferred := facts.PreferredResolution[m.Name]
	wanted, hasNative := aspectOf(preferred)
	if !hasNative {
		return "", true
	}
	parsed, valid := parseMode(mode)
	if !valid || parsed.height == 0 {
		return "", true
	}
	if sameAspect(float64(parsed.width)/float64(parsed.height), wanted) {
		return "", true
	}
	return preferred, false
}
