package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// audioHook moves sound to the TV for the session and puts it back afterwards.
//
// Nothing else in couch mode does this, and without it the game plays on the
// desk speakers while the picture is in the living room.
type audioHook struct{}

func (*audioHook) Name() string        { return "audio" }
func (*audioHook) Description() string { return "Send sound to the TV over HDMI" }
func (*audioHook) Available() bool     { return have("pactl") }

func (h *audioHook) Enter(ctx context.Context, env Env) (Undo, error) {
	sinks, err := listSinks(ctx)
	if err != nil {
		return nil, err
	}
	previous, err := defaultSink(ctx)
	if err != nil {
		return nil, err
	}

	target, ok := pickHDMISink(sinks, env)
	if !ok {
		return nil, errors.New("no HDMI audio output found")
	}
	if target.Name == previous {
		// Already on the TV; leave it, and leave it alone on the way out.
		return nil, nil
	}

	if err := selectSink(ctx, target); err != nil {
		return nil, err
	}
	env.logf("couch: audio moved to %s", target.Description)

	return func(ctx context.Context) error {
		restore, ok := sinkByName(sinks, previous)
		if !ok {
			// The old output is gone -- headphones unplugged mid-session --
			// so there is nothing to go back to and forcing it would fail.
			return nil
		}
		return selectSink(ctx, restore)
	}, nil
}

type sink struct {
	// NodeID is PipeWire's node id, which is what wpctl and Omarchy's helper
	// take. It is not the pactl index, and mixing the two silently switches
	// the wrong output.
	NodeID      int
	Name        string
	Description string
}

func listSinks(ctx context.Context) ([]sink, error) {
	out, err := run(ctx, "pactl", "-f", "json", "list", "sinks")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Properties  map[string]string `json:"properties"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("read audio outputs: %w", err)
	}
	sinks := make([]sink, 0, len(raw))
	for _, entry := range raw {
		s := sink{Name: entry.Name, Description: entry.Description}
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

// pickHDMISink finds the output the TV is on.
//
// The display connector and the audio device are separate subsystems with no
// reliable link between them, so this matches on the sink naming itself. A sink
// whose description names the TV wins over a bare HDMI match, which matters on
// a machine with more than one HDMI audio device.
func pickHDMISink(sinks []sink, env Env) (sink, bool) {
	var fallback sink
	found := false
	for _, s := range sinks {
		if !isHDMISink(s) {
			continue
		}
		if env.TVDescription != "" && descriptionMatches(s, env.TVDescription) {
			return s, true
		}
		if !found {
			fallback, found = s, true
		}
	}
	return fallback, found
}

func isHDMISink(s sink) bool {
	name := strings.ToLower(s.Name)
	return strings.Contains(name, "hdmi") || strings.Contains(name, "displayport")
}

func descriptionMatches(s sink, tvDescription string) bool {
	desc := strings.ToLower(s.Description)
	for _, word := range strings.Fields(strings.ToLower(tvDescription)) {
		if len(word) < 4 {
			continue
		}
		if strings.Contains(desc, word) {
			return true
		}
	}
	return false
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
