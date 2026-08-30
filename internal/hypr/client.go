package hypr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type EventType string

const (
	EventMonitorAdded   EventType = "monitoradded"
	EventMonitorRemoved EventType = "monitorremoved"
	EventWindowOpened   EventType = "openwindow"
	EventWindowClosed   EventType = "closewindow"
	EventWindowTitle    EventType = "windowtitle"
	EventConfigReloaded EventType = "configreloaded"
)

// eventNames maps Hyprland's socket2 event names onto the types above.
//
// The lookup is exact on purpose. Matching by prefix used to fold
// "activewindowv2" into "activewindow" and, worse, let every window event reach
// the monitor subscription, so the daemon re-derived the layout on each focus
// change. The v2 spellings that genuinely mean the same thing are listed here
// instead, which keeps monitoraddedv2 working without opening that door again.
var eventNames = map[string]EventType{
	"monitoradded":     EventMonitorAdded,
	"monitoraddedv2":   EventMonitorAdded,
	"monitorremoved":   EventMonitorRemoved,
	"monitorremovedv2": EventMonitorRemoved,
	"openwindow":       EventWindowOpened,
	"closewindow":      EventWindowClosed,
	"windowtitle":      EventWindowTitle,
	"windowtitlev2":    EventWindowTitle,
	"configreloaded":   EventConfigReloaded,
}

// MonitorEventTypes are the events that change which displays exist.
var MonitorEventTypes = []EventType{EventMonitorAdded, EventMonitorRemoved}

type Event struct {
	Type  EventType
	Value string
	Raw   string
}

type VersionInfo struct {
	Version string `json:"version"`
	Tag     string `json:"tag"`
}

type instanceInfo struct {
	Instance string `json:"instance"`
	WLSocket string `json:"wl_socket"`
}

type Client struct {
	hyprctl string
}

func NewClient() (*Client, error) {
	path, err := exec.LookPath("hyprctl")
	if err != nil {
		return nil, fmt.Errorf("hyprctl not found in PATH")
	}
	return &Client{hyprctl: path}, nil
}

func (c *Client) Monitors(ctx context.Context) ([]Monitor, error) {
	cmd, err := c.commandContext(ctx, "-j", "monitors", "all")
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to query monitors: %w", err)
	}
	var monitors []Monitor
	if err := json.Unmarshal(out, &monitors); err != nil {
		return nil, fmt.Errorf("failed to decode hyprctl monitors JSON: %w", err)
	}
	monitors = dropSyntheticMonitors(monitors)
	normalizeMirrorTargets(monitors)
	enrichMonitorConnectorPaths(monitors)
	return monitors, nil
}

// fallbackConnectorName is what Hyprland calls the placeholder head it invents
// when no real output is driving anything, so the compositor has somewhere to
// draw rather than falling over.
const fallbackConnectorName = "FALLBACK"

// dropSyntheticMonitors removes heads that exist only inside Hyprland. The
// placeholder appears exactly when every real output is off and reports itself
// as enabled, which is the one moment it does the most damage: it makes "is
// everything disabled?" answer no, so the guard that would switch a display
// back on stands down and leaves a black screen with no way back.
//
// Filtering here rather than at that guard keeps the rest of the program honest
// too. A head with no connector and no hardware behind it would otherwise be
// hashed into the monitor set, matched against saved profiles, drawn by the
// panel, and captured into a profile saved from live state.
func dropSyntheticMonitors(monitors []Monitor) []Monitor {
	filtered := monitors[:0]
	for _, monitor := range monitors {
		if strings.EqualFold(strings.TrimSpace(monitor.Name), fallbackConnectorName) {
			continue
		}
		filtered = append(filtered, monitor)
	}
	return filtered
}

// normalizeMirrorTargets rewrites what hyprctl reports for a mirroring monitor
// into the connector name every other code path keys on. Hyprland says "none"
// when nothing is mirrored and otherwise names the source by monitor ID, which
// matches no connector and would silently drop the mirror when a profile is
// saved from live state.
func normalizeMirrorTargets(monitors []Monitor) {
	nameByID := make(map[string]string, len(monitors))
	for _, monitor := range monitors {
		nameByID[strconv.Itoa(monitor.ID)] = monitor.Name
	}

	for i := range monitors {
		target := strings.TrimSpace(monitors[i].MirrorOf)
		if target == "" || target == "none" {
			monitors[i].MirrorOf = ""
			continue
		}
		if name, ok := nameByID[target]; ok {
			monitors[i].MirrorOf = name
			continue
		}
		monitors[i].MirrorOf = target
	}
}

func (c *Client) Workspaces(ctx context.Context) ([]WorkspaceState, error) {
	cmd, err := c.commandContext(ctx, "-j", "workspaces")
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to query workspaces: %w", err)
	}
	var workspaces []WorkspaceState
	if err := json.Unmarshal(out, &workspaces); err != nil {
		return nil, fmt.Errorf("failed to decode hyprctl workspaces JSON: %w", err)
	}
	return workspaces, nil
}

func (c *Client) WorkspaceRules(ctx context.Context) ([]WorkspaceRule, error) {
	cmd, err := c.commandContext(ctx, "-j", "workspacerules")
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to query workspace rules: %w", err)
	}
	var rules []WorkspaceRule
	if err := json.Unmarshal(out, &rules); err != nil {
		return nil, fmt.Errorf("failed to decode hyprctl workspace rules JSON: %w", err)
	}
	return rules, nil
}

func (c *Client) Version(ctx context.Context) (VersionInfo, error) {
	cmd, err := c.commandContext(ctx, "-j", "version")
	if err != nil {
		return VersionInfo{}, err
	}
	out, err := cmd.Output()
	if err != nil {
		return VersionInfo{}, fmt.Errorf("failed to query hyprctl version: %w", err)
	}
	var version VersionInfo
	if err := json.Unmarshal(out, &version); err != nil {
		return VersionInfo{}, fmt.Errorf("failed to decode hyprctl version JSON: %w", err)
	}
	return version, nil
}

func (c *Client) SupportsMonitorV2(ctx context.Context) (bool, error) {
	version, err := c.Version(ctx)
	if err != nil {
		return false, err
	}
	return versionAtLeast(firstNonEmpty(version.Version, version.Tag), 0, 50, 0), nil
}

func (c *Client) Reload(ctx context.Context) error {
	cmd, err := c.commandContext(ctx, "reload")
	if err != nil {
		return err
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to reload Hyprland: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Eval executes Lua in the active Hyprland config state and returns Hyprland's
// response. Hyprland 0.55 supports eval for Lua configs and replies with "ok"
// when the expression completes successfully.
func (c *Client) Eval(ctx context.Context, code string) (string, error) {
	cmd, err := c.commandContext(ctx, "eval", code)
	if err != nil {
		return "", err
	}
	out, err := cmd.CombinedOutput()
	response := strings.TrimSpace(string(out))
	if err != nil {
		return response, fmt.Errorf("failed evaluating Hyprland Lua: %w (%s)", err, response)
	}
	return response, nil
}

func (c *Client) KeywordMonitor(ctx context.Context, value string) error {
	cmd, err := c.commandContext(ctx, "keyword", "monitor", value)
	if err != nil {
		return err
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed applying monitor keyword %q: %w (%s)", value, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *Client) KeywordWorkspace(ctx context.Context, value string) error {
	cmd, err := c.commandContext(ctx, "keyword", "workspace", value)
	if err != nil {
		return err
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed applying workspace keyword %q: %w (%s)", value, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *Client) Dispatch(ctx context.Context, dispatcher string, args ...string) error {
	allArgs := append([]string{"dispatch", dispatcher}, args...)
	cmd, err := c.commandContext(ctx, allArgs...)
	if err != nil {
		return err
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed dispatch %q: %w (%s)", dispatcher, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// WakeDisplays turns DPMS on for every output. Hyprland running a Lua config
// only accepts the Lua dispatcher form, and a legacy config only the classic
// one, so the caller has to say which config is loaded.
func (c *Client) WakeDisplays(ctx context.Context, luaDispatch bool) error {
	if luaDispatch {
		return c.Dispatch(ctx, `hl.dsp.dpms({ action = "enable" })`)
	}
	return c.Dispatch(ctx, "dpms", "on")
}

func (c *Client) BatchKeywordMonitor(ctx context.Context, values []string) error {
	if len(values) == 0 {
		return nil
	}
	commands := make([]string, 0, len(values))
	for _, v := range values {
		commands = append(commands, "keyword monitor "+v)
	}
	return c.Batch(ctx, commands)
}

func (c *Client) BatchKeywordWorkspace(ctx context.Context, values []string) error {
	if len(values) == 0 {
		return nil
	}
	commands := make([]string, 0, len(values))
	for _, v := range values {
		commands = append(commands, "keyword workspace "+v)
	}
	return c.Batch(ctx, commands)
}

func (c *Client) Batch(ctx context.Context, commands []string) error {
	if len(commands) == 0 {
		return nil
	}
	cmd, err := c.commandContext(ctx, "--batch", strings.Join(commands, " ; "))
	if err != nil {
		return err
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed hyprctl batch apply: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *Client) commandContext(ctx context.Context, args ...string) (*exec.Cmd, error) {
	instance, err := c.resolveInstance(ctx)
	if err != nil {
		return nil, err
	}
	cmdArgs := append([]string{"--instance", instance}, args...)
	return exec.CommandContext(ctx, c.hyprctl, cmdArgs...), nil
}

func (c *Client) resolveInstance(ctx context.Context) (string, error) {
	if sig := strings.TrimSpace(os.Getenv("HYPRLAND_INSTANCE_SIGNATURE")); sig != "" {
		return sig, nil
	}
	return c.discoverInstance(ctx)
}

func (c *Client) discoverInstance(ctx context.Context) (string, error) {
	instances, err := c.instances(ctx)
	if err != nil {
		return "", err
	}
	return selectInstance(instances, strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")))
}

func selectInstance(instances []instanceInfo, waylandDisplay string) (string, error) {
	if len(instances) == 0 {
		return "", errors.New("no running Hyprland instances found")
	}
	if waylandDisplay != "" {
		matches := make([]instanceInfo, 0, len(instances))
		for _, inst := range instances {
			if inst.WLSocket == waylandDisplay {
				matches = append(matches, inst)
			}
		}
		if len(matches) == 1 {
			return matches[0].Instance, nil
		}
		if len(matches) > 1 {
			return "", fmt.Errorf("multiple Hyprland instances match WAYLAND_DISPLAY=%q", waylandDisplay)
		}
	}
	if len(instances) == 1 {
		return instances[0].Instance, nil
	}
	return "", errors.New("multiple Hyprland instances found; set HYPRLAND_INSTANCE_SIGNATURE or WAYLAND_DISPLAY")
}

func (c *Client) socket2Path(ctx context.Context) (string, error) {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", errors.New("XDG_RUNTIME_DIR is not set")
	}
	sig, err := c.resolveInstance(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(runtimeDir, "hypr", sig, ".socket2.sock"), nil
}

// SubscribeMonitorEvents reports only the events that change which displays
// exist. The daemon acts on every event it receives, so widening this feed
// makes it re-derive the layout for unrelated activity.
func (c *Client) SubscribeMonitorEvents(ctx context.Context) (<-chan Event, <-chan error) {
	return c.SubscribeEvents(ctx, MonitorEventTypes...)
}

// SubscribeEvents streams the named Hyprland events. Passing none streams every
// event the parser recognises.
func (c *Client) SubscribeEvents(ctx context.Context, types ...EventType) (<-chan Event, <-chan error) {
	wanted := make(map[EventType]struct{}, len(types))
	for _, t := range types {
		wanted[t] = struct{}{}
	}

	events := make(chan Event)
	errorsCh := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errorsCh)

		socketPath, err := c.socket2Path(ctx)
		if err != nil {
			errorsCh <- err
			return
		}

		dialer := net.Dialer{Timeout: 5 * time.Second}
		conn, err := dialer.DialContext(ctx, "unix", socketPath)
		if err != nil {
			errorsCh <- fmt.Errorf("failed to connect to hyprland socket2: %w", err)
			return
		}
		defer conn.Close()

		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			event, ok := parseEvent(line)
			if !ok {
				continue
			}
			if len(wanted) > 0 {
				if _, want := wanted[event.Type]; !want {
					continue
				}
			}
			select {
			case <-ctx.Done():
				return
			case events <- event:
			}
		}

		if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
			errorsCh <- err
		}
	}()

	return events, errorsCh
}

func parseEvent(line string) (Event, bool) {
	parts := strings.SplitN(line, ">>", 2)
	if len(parts) != 2 {
		return Event{}, false
	}
	typeName := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])

	eventType, ok := eventNames[typeName]
	if !ok {
		return Event{}, false
	}
	return Event{Type: eventType, Value: value, Raw: line}, true
}

func versionAtLeast(value string, wantMajor, wantMinor, wantPatch int) bool {
	parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(value, "v")), ".")
	if len(parts) == 0 {
		return false
	}
	parsed := []int{0, 0, 0}
	for idx := 0; idx < len(parsed) && idx < len(parts); idx++ {
		part := parts[idx]
		end := 0
		for end < len(part) && part[end] >= '0' && part[end] <= '9' {
			end++
		}
		if end == 0 {
			continue
		}
		n, err := strconv.Atoi(part[:end])
		if err != nil {
			continue
		}
		parsed[idx] = n
	}

	if parsed[0] != wantMajor {
		return parsed[0] > wantMajor
	}
	if parsed[1] != wantMinor {
		return parsed[1] > wantMinor
	}
	return parsed[2] >= wantPatch
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// OptionValue is one Hyprland config option as `hyprctl getoption -j` reports
// it. Only the field matching the option's type is filled.
type OptionValue struct {
	Option string  `json:"option"`
	Int    int     `json:"int"`
	Float  float64 `json:"float"`
	Str    string  `json:"str"`
	Bool   bool    `json:"bool"`
	Set    bool    `json:"set"`
}

// Option reads a live Hyprland config option.
//
// There is deliberately no setter. On Hyprland's Lua config parser (0.5x+)
// `hyprctl keyword` answers "keyword can't work with non-legacy parsers" and
// still exits 0, and the Lua hl.config() is accepted and then ignored, so any
// runtime setter would be a silent no-op. Options that matter to a console
// session are reported to the user to set in their own config instead.
func (c *Client) Option(ctx context.Context, name string) (OptionValue, error) {
	cmd, err := c.commandContext(ctx, "getoption", name, "-j")
	if err != nil {
		return OptionValue{}, err
	}
	out, err := cmd.Output()
	if err != nil {
		return OptionValue{}, fmt.Errorf("failed to read option %s: %w", name, err)
	}
	var value OptionValue
	if err := json.Unmarshal(out, &value); err != nil {
		return OptionValue{}, fmt.Errorf("failed to decode option %s: %w", name, err)
	}
	return value, nil
}

// Session names the Hyprland instance this client talks to and the Wayland
// socket that instance listens on.
type Session struct {
	Instance string
	WLSocket string
}

// Session reports the running Hyprland instance and its Wayland socket.
//
// A daemon started by systemd inherits almost nothing from the graphical
// session -- no HYPRLAND_INSTANCE_SIGNATURE, no WAYLAND_DISPLAY -- yet every
// program it launches on the user's behalf needs both. Discovery already
// happens here to find the socket, so the same answer is published rather than
// asking the environment a second time and getting nothing.
func (c *Client) Session(ctx context.Context) (Session, error) {
	instance, err := c.resolveInstance(ctx)
	if err != nil {
		return Session{}, err
	}
	session := Session{Instance: instance, WLSocket: strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY"))}
	if session.WLSocket != "" {
		return session, nil
	}
	instances, err := c.instances(ctx)
	if err != nil {
		// The instance resolved, so the socket is a bonus rather than a
		// failure; the caller falls back to scanning the runtime directory.
		return session, nil
	}
	for _, inst := range instances {
		if inst.Instance == instance {
			session.WLSocket = inst.WLSocket
			break
		}
	}
	return session, nil
}

func (c *Client) instances(ctx context.Context) ([]instanceInfo, error) {
	out, err := exec.CommandContext(ctx, c.hyprctl, "-j", "instances").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to query Hyprland instances: %w", err)
	}
	var instances []instanceInfo
	if err := json.Unmarshal(out, &instances); err != nil {
		return nil, fmt.Errorf("failed to decode hyprctl instances JSON: %w", err)
	}
	return instances, nil
}
