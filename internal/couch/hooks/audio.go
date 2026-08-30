package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// audioHook moves sound to the TV for the session and puts it back afterwards.
//
// Nothing else in couch mode does this, and without it the game plays on the
// desk speakers while the picture is in the living room.
//
// Finding "the TV's audio output" is the whole difficulty. A graphics card
// presents one HDMI audio device per connector as a *pin*, and the sinks
// PulseAudio derives from them are named after the card, not the display:
// every one of them says "Navi 48 HDMI/DP Audio Controller". On a machine with
// the desk monitor on one connector and the TV on another, picking a sink by
// its name or description picks the wrong one about half the time -- and the
// half it got wrong here was the common one, so a console session played its
// sound out of the desk monitor.
//
// The link that does hold is the EDID. ALSA publishes each pin's ELD under
// /proc/asound, monitor name included, and that name comes from the same EDID
// Hyprland reports for the connector. Matching there says which pin is the TV;
// the pin index then names the port (hdmi-output-N) and the card profile that
// exposes it.
type audioHook struct{}

func (*audioHook) Name() string        { return "audio" }
func (*audioHook) Description() string { return "Send sound to the TV over HDMI" }
func (*audioHook) Available() bool     { return have("pactl") }

// Capture records which output sound is on now, so it can go back there.
func (h *audioHook) Capture(ctx context.Context, env Env) (State, error) {
	target, err := resolveTVAudio(ctx, env)
	if err != nil {
		return nil, err
	}
	previous, err := defaultSink(ctx)
	if err != nil {
		return nil, err
	}

	state := State{"previous_sink": previous}
	if target.CardProfile != "" && target.CardProfile != target.CardActiveProfile {
		state["card"] = target.Card
		state["previous_card_profile"] = target.CardActiveProfile
	}
	if len(state) == 1 && target.SinkName != "" && target.SinkName == previous {
		// Already on the TV, on the right port: leave it, and leave it alone on
		// the way out.
		return nil, nil
	}
	return state, nil
}

func (h *audioHook) Apply(ctx context.Context, env Env) error {
	target, err := resolveTVAudio(ctx, env)
	if err != nil {
		return err
	}
	sink, err := target.activate(ctx)
	if err != nil {
		return err
	}
	if err := selectSink(ctx, sink); err != nil {
		return err
	}
	env.logf("couch: audio moved to the TV on %s (%s)", target.Port, sink.Name)
	return nil
}

func (h *audioHook) Restore(ctx context.Context, env Env, prev State) error {
	// Move sound off the TV before the port that carries it goes away;
	// switching the card profile first would leave the streams to be rehomed
	// by whatever PipeWire picks.
	if previous := prev["previous_sink"]; previous != "" {
		sinks, err := listSinks(ctx)
		if err != nil {
			return err
		}
		if restore, ok := sinkByName(sinks, previous); ok {
			if err := selectSink(ctx, restore); err != nil {
				return err
			}
		} else {
			// The old output is gone -- headphones unplugged mid-session -- so
			// there is nothing to go back to and forcing it would fail.
			env.logf("couch: the previous audio output %s is gone; leaving sound where it is", previous)
		}
	}

	card, profile := prev["card"], prev["previous_card_profile"]
	if card == "" || profile == "" {
		return nil
	}
	if _, err := run(ctx, "pactl", "set-card-profile", card, profile); err != nil {
		return fmt.Errorf("restore audio card profile %s: %w", profile, err)
	}
	return nil
}

// tvAudio is everything needed to put sound on the TV: which card, which
// profile exposes the TV's pin, and which port that pin is.
type tvAudio struct {
	Card              string
	CardActiveProfile string
	CardProfile       string
	Port              string
	// SinkName is the sink already serving the TV's port, empty when the card
	// has to switch profile before one exists.
	SinkName string
}

// activate makes the TV's port live and returns the sink serving it.
//
// The card profile is verified by effect rather than by exit status. pactl
// accepts a profile switch and reports success whether or not a usable sink
// comes out the other side, and this repository has been bitten enough times by
// commands that exit 0 and do nothing.
func (t tvAudio) activate(ctx context.Context) (sink, error) {
	if t.CardProfile != "" && t.CardProfile != t.CardActiveProfile {
		if _, err := run(ctx, "pactl", "set-card-profile", t.Card, t.CardProfile); err != nil {
			return sink{}, fmt.Errorf("select audio card profile %s: %w", t.CardProfile, err)
		}
	}
	// The sink is created asynchronously, so a read straight after the switch
	// finds nothing.
	deadline := time.Now().Add(5 * time.Second)
	for {
		sinks, err := listSinks(ctx)
		if err == nil {
			if s, ok := sinkOnPort(sinks, t.Port); ok {
				return s, nil
			}
		}
		if time.Now().After(deadline) {
			return sink{}, fmt.Errorf("no audio output appeared on the TV's port %s", t.Port)
		}
		select {
		case <-ctx.Done():
			return sink{}, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// resolveTVAudio finds the card, profile and port the TV's audio lives on.
//
// It refuses rather than guesses. Falling back to "any HDMI output" is what
// sent sound to the desk monitor, and a session that plays out of the wrong
// speakers is worse than one that leaves the sound where the user had it.
func resolveTVAudio(ctx context.Context, env Env) (tvAudio, error) {
	if strings.TrimSpace(env.TVDescription) == "" {
		return tvAudio{}, errors.New("the TV has no display description to match its audio output against")
	}
	pin, err := awaitTVPin(ctx, env)
	if err != nil {
		return tvAudio{}, err
	}
	cards, err := listCards(ctx)
	if err != nil {
		return tvAudio{}, err
	}
	card, ok := cardForALSAIndex(cards, pin.Card)
	if !ok {
		return tvAudio{}, fmt.Errorf("no audio card found for ALSA card %d", pin.Card)
	}
	port := portForPin(pin.Pin)
	profile, ok := card.profileForPort(port)
	if !ok {
		return tvAudio{}, fmt.Errorf("no available audio profile carries the TV's port %s", port)
	}
	return tvAudio{
		Card:              card.Name,
		CardActiveProfile: card.ActiveProfile,
		CardProfile:       profile,
		Port:              port,
		SinkName:          card.sinkOnPort(port),
	}, nil
}

// awaitTVPin waits for ALSA to notice the TV.
//
// A display that has just been switched on takes a moment to present its ELD,
// and the layout is applied immediately before this hook runs, so the first
// read is often too early.
func awaitTVPin(ctx context.Context, env Env) (eldPin, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if pin, ok := matchTVPin(readELDPins(eldRoot), env.TVDescription); ok {
			return pin, nil
		}
		if time.Now().After(deadline) {
			return eldPin{}, fmt.Errorf("no HDMI audio pin is presenting the TV (%s)", env.TVDescription)
		}
		select {
		case <-ctx.Done():
			return eldPin{}, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// portForPin names the PulseAudio port for an ALSA HDMI pin index.
func portForPin(pin int) string {
	return fmt.Sprintf("hdmi-output-%d", pin)
}

// eldRoot is where ALSA publishes each HDMI pin's ELD. A variable so tests can
// point it at a fixture.
var eldRoot = "/proc/asound"

// eldPin is one HDMI audio pin and the display attached to it.
type eldPin struct {
	Card int
	Pin  int
	// MonitorName is the EDID monitor name, which is what ties this pin to a
	// display connector.
	MonitorName string
}

// readELDPins lists the HDMI audio pins that currently have a display on them.
//
// Files are named eld#<device>.<pin>; a pin with no display reports
// monitor_present 0 and is skipped, since it cannot be anyone's TV.
func readELDPins(root string) []eldPin {
	cards, err := filepath.Glob(filepath.Join(root, "card*"))
	if err != nil {
		return nil
	}
	pins := []eldPin{}
	for _, cardDir := range cards {
		index, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(cardDir), "card"))
		if err != nil {
			continue
		}
		elds, err := filepath.Glob(filepath.Join(cardDir, "eld#*"))
		if err != nil {
			continue
		}
		for _, path := range elds {
			pin, ok := readELD(path, index)
			if ok {
				pins = append(pins, pin)
			}
		}
	}
	sort.Slice(pins, func(i, j int) bool {
		if pins[i].Card != pins[j].Card {
			return pins[i].Card < pins[j].Card
		}
		return pins[i].Pin < pins[j].Pin
	})
	return pins
}

func readELD(path string, card int) (eldPin, bool) {
	_, after, ok := strings.Cut(filepath.Base(path), ".")
	if !ok {
		return eldPin{}, false
	}
	pinIndex, err := strconv.Atoi(after)
	if err != nil {
		return eldPin{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return eldPin{}, false
	}

	pin := eldPin{Card: card, Pin: pinIndex}
	present := false
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "monitor_present":
			present = value == "1"
		case "monitor_name":
			pin.MonitorName = value
		}
	}
	if !present || pin.MonitorName == "" {
		return eldPin{}, false
	}
	return pin, true
}

// matchTVPin finds the pin whose EDID monitor name appears in the display's
// description, which is where Hyprland puts the same string.
//
// The longest match wins, so a TV named "SAMSUNG" does not lose to a pin whose
// name is a shorter substring of the same description.
func matchTVPin(pins []eldPin, tvDescription string) (eldPin, bool) {
	description := strings.ToLower(tvDescription)
	best := eldPin{}
	found := false
	for _, pin := range pins {
		name := strings.ToLower(strings.TrimSpace(pin.MonitorName))
		// Two- and three-letter names match half the descriptions on a machine.
		if len(name) < 4 || !strings.Contains(description, name) {
			continue
		}
		if !found || len(name) > len(best.MonitorName) {
			best, found = pin, true
		}
	}
	return best, found
}

type card struct {
	Name          string
	ALSACard      int
	ActiveProfile string
	// Profiles maps a port name to the available profiles that carry it.
	Profiles map[string][]string
	// Sinks maps a port name to the sink currently serving it.
	Sinks map[string]string
}

// profileForPort picks the profile to switch to so the port becomes usable.
//
// The active profile wins when it already carries the port, so a session does
// not disturb a card that is already right. Otherwise the shortest name wins,
// which is how stereo beats the surround variants of the same port.
func (c card) profileForPort(port string) (string, bool) {
	candidates := c.Profiles[port]
	if len(candidates) == 0 {
		return "", false
	}
	for _, name := range candidates {
		if name == c.ActiveProfile {
			return name, true
		}
	}
	best := candidates[0]
	for _, name := range candidates[1:] {
		if len(name) < len(best) {
			best = name
		}
	}
	return best, true
}

func (c card) sinkOnPort(port string) string { return c.Sinks[port] }

func cardForALSAIndex(cards []card, index int) (card, bool) {
	for _, c := range cards {
		if c.ALSACard == index {
			return c, true
		}
	}
	return card{}, false
}

// listCards reads the sound cards, their profiles and which port each one
// carries.
//
// Ports are read from the profile list rather than from the card's own port
// names: pactl's JSON writer emits a null name for every port on a system whose
// device descriptions are not ASCII, and this machine's are Portuguese.
func listCards(ctx context.Context) ([]card, error) {
	out, err := run(ctx, "pactl", "-f", "json", "list", "cards")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name       string            `json:"name"`
		Properties map[string]string `json:"properties"`
		Profiles   map[string]struct {
			Sinks     int  `json:"sinks"`
			Available bool `json:"available"`
		} `json:"profiles"`
		ActiveProfile string `json:"active_profile"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("read audio cards: %w", err)
	}

	sinks, err := listSinks(ctx)
	if err != nil {
		return nil, err
	}

	cards := make([]card, 0, len(raw))
	for _, entry := range raw {
		c := card{
			Name:          entry.Name,
			ALSACard:      -1,
			ActiveProfile: entry.ActiveProfile,
			Profiles:      map[string][]string{},
			Sinks:         map[string]string{},
		}
		if index, err := strconv.Atoi(entry.Properties["alsa.card"]); err == nil {
			c.ALSACard = index
		}
		for name, profile := range entry.Profiles {
			if !profile.Available || profile.Sinks == 0 {
				continue
			}
			port, ok := portForProfile(name)
			if !ok {
				continue
			}
			c.Profiles[port] = append(c.Profiles[port], name)
		}
		for _, port := range c.Profiles {
			sort.Strings(port)
		}
		for _, s := range sinks {
			if s.ALSACard == c.ALSACard && s.ActivePort != "" {
				c.Sinks[s.ActivePort] = s.Name
			}
		}
		cards = append(cards, c)
	}
	return cards, nil
}

// portForProfile maps an HDMI output profile to the port it exposes.
//
// PulseAudio's ALSA module names them by pin: the first is bare, the rest carry
// an "extraN" suffix that is the pin index.
func portForProfile(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, "output:hdmi-")
	if !ok {
		return "", false
	}
	_, suffix, hasExtra := strings.Cut(rest, "-extra")
	if !hasExtra {
		return portForPin(0), true
	}
	pin, err := strconv.Atoi(suffix)
	if err != nil {
		return "", false
	}
	return portForPin(pin), true
}

type sink struct {
	// NodeID is PipeWire's node id, which is what wpctl and Omarchy's helper
	// take. It is not the pactl index, and mixing the two silently switches
	// the wrong output.
	NodeID      int
	Name        string
	Description string
	// ALSACard is the card index this sink belongs to and ActivePort the port
	// on it. Together they say which display the sound actually comes out of,
	// which the name and description never do.
	ALSACard   int
	ActivePort string
}

func listSinks(ctx context.Context) ([]sink, error) {
	out, err := run(ctx, "pactl", "-f", "json", "list", "sinks")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		ActivePort  string            `json:"active_port"`
		Properties  map[string]string `json:"properties"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("read audio outputs: %w", err)
	}
	sinks := make([]sink, 0, len(raw))
	for _, entry := range raw {
		s := sink{
			Name:        entry.Name,
			Description: entry.Description,
			ActivePort:  entry.ActivePort,
			ALSACard:    -1,
		}
		if index, err := strconv.Atoi(entry.Properties["alsa.card"]); err == nil {
			s.ALSACard = index
		}
		if id := entry.Properties["object.id"]; id != "" {
			_, _ = fmt.Sscanf(id, "%d", &s.NodeID)
		}
		if desc := entry.Properties["device.description"]; desc != "" {
			s.Description = desc
		}
		sinks = append(sinks, s)
	}
	return sinks, nil
}

func defaultSink(ctx context.Context) (string, error) {
	return run(ctx, "pactl", "get-default-sink")
}

func sinkByName(sinks []sink, name string) (sink, bool) {
	for _, s := range sinks {
		if s.Name == name {
			return s, true
		}
	}
	return sink{}, false
}

func sinkOnPort(sinks []sink, port string) (sink, bool) {
	for _, s := range sinks {
		if s.ActivePort == port {
			return s, true
		}
	}
	return sink{}, false
}

// selectSink switches the default output and drags playing streams with it, so
// something already making noise follows the picture to the TV.
func selectSink(ctx context.Context, target sink) error {
	if have("omarchy-audio-output-set-default") && target.NodeID > 0 {
		// Omarchy's helper does the same work and knows to leave DSP filter
		// chains where they are; prefer it where it exists.
		if _, err := run(ctx, "omarchy-audio-output-set-default",
			fmt.Sprint(target.NodeID), target.Name); err == nil {
			return nil
		}
	}
	if _, err := run(ctx, "pactl", "set-default-sink", target.Name); err != nil {
		return err
	}
	moveStreams(ctx, target)
	return nil
}

func moveStreams(ctx context.Context, target sink) {
	out, err := run(ctx, "pactl", "-f", "json", "list", "sink-inputs")
	if err != nil {
		return
	}
	var inputs []struct {
		Index      int               `json:"index"`
		Properties map[string]string `json:"properties"`
	}
	if json.Unmarshal([]byte(out), &inputs) != nil {
		return
	}
	for _, input := range inputs {
		// A DSP filter chain's own output is a sink input with no application
		// behind it; moving that rewires the processing into itself.
		if input.Properties["application.name"] == "" {
			continue
		}
		_, _ = run(ctx, "pactl", "move-sink-input", fmt.Sprint(input.Index), target.Name)
	}
}
