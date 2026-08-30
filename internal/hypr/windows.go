package hypr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type Window struct {
	Address      string        `json:"address"`
	Class        string        `json:"class"`
	InitialClass string        `json:"initialClass"`
	Title        string        `json:"title"`
	InitialTitle string        `json:"initialTitle"`
	Pid          int           `json:"pid"`
	Monitor      WindowMonitor `json:"monitor"`
	Floating     bool          `json:"floating"`
	Hidden       bool          `json:"hidden"`
	Pinned       bool          `json:"pinned"`
	At           [2]int        `json:"at"`
	Size         [2]int        `json:"size"`
	Fullscreen   int           `json:"fullscreen"`
}

type WindowMonitor string

func (m *WindowMonitor) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*m = WindowMonitor(s)
		return nil
	}
	var i int
	if err := json.Unmarshal(data, &i); err == nil {
		*m = WindowMonitor(fmt.Sprintf("%d", i))
		return nil
	}
	*m = WindowMonitor(strings.Trim(string(data), "\""))
	return nil
}

func (m WindowMonitor) String() string { return string(m) }

// Width and Height expose the window buffer size reported by hyprctl.
func (w Window) Width() int  { return w.Size[0] }
func (w Window) Height() int { return w.Size[1] }

func (w Window) MatchClass() string {
	if strings.TrimSpace(w.Class) != "" {
		return w.Class
	}
	return w.InitialClass
}

func (c *Client) Clients(ctx context.Context) ([]Window, error) {
	cmd, err := c.commandContext(ctx, "-j", "clients")
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to query clients: %w", err)
	}
	var windows []Window
	if err := json.Unmarshal(out, &windows); err != nil {
		return nil, fmt.Errorf("failed to decode hyprctl clients JSON: %w", err)
	}
	return windows, nil
}
