package couch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/crmne/hyprmoncfg/internal/config"
	"github.com/crmne/hyprmoncfg/internal/hypr"
	"github.com/crmne/hyprmoncfg/internal/profile"
)

const (
	DefaultCloseAppsWaitSeconds = 5
	ControllerDebounceSeconds   = 10
	ControllerMinUsageSeconds   = 60
)

// DeskDuringCouch says what happens to the displays that are not the TV.
//
// It is an input to generating the console profile, not a runtime switch: the
// generated layout already encodes the answer, so nothing reads it during a
// session.
type DeskDuringCouch string

const (
	DeskDisabled DeskDuringCouch = "disabled"
	DeskEnabled  DeskDuringCouch = "enabled"
	DeskMirror   DeskDuringCouch = "mirror"
)

type Config struct {
	Enabled bool `json:"enabled"`
	// Layout is the small editable surface over the generated console profile.
	Layout               ConsoleLayout `json:"layout"`
	ExitOnControllersOff bool          `json:"exit_on_controllers_off,omitempty"`
	// WatchBigPicture enters console mode when Big Picture is opened from
	// Steam itself, rather than through couch mode.
	WatchBigPicture bool `json:"watch_big_picture,omitempty"`
	// EnterOnControllerConnect is the most console-like trigger there is: turn
	// the pad on and the TV comes up.
	EnterOnControllerConnect bool     `json:"enter_on_controller_connect,omitempty"`
	CloseAppsEnabled         bool     `json:"close_apps_enabled,omitempty"`
	CloseAppsWaitSeconds     int      `json:"close_apps_wait_seconds,omitempty"`
	AppsToClose              []string `json:"apps_to_close,omitempty"`
	// Hooks turns individual session hooks off by name. A hook missing from
	// the map is on, so a machine that gains a capability starts using it
	// without the user having to opt in again.
	Hooks map[string]bool `json:"hooks,omitempty"`
	// Gamescope runs Steam inside a nested gamescope compositor on the TV,
	// which is what buys per-game HDR, FSR and an fps cap.
	Gamescope GamescopeSettings `json:"gamescope,omitempty"`

	// Legacy fields, read once so an existing config can seed the generated
	// profile instead of being thrown away. They are never written back.
	LegacyTVProfile   string          `json:"tv_profile,omitempty"`
	LegacyDeskProfile string          `json:"desk_profile,omitempty"`
	LegacyDesk        DeskDuringCouch `json:"desk_during_couch,omitempty"`
}

func DefaultConfig() Config {
	return Config{CloseAppsWaitSeconds: DefaultCloseAppsWaitSeconds}
}

func ConfigPath(baseDir string) string {
	return filepath.Join(baseDir, "couch.json")
}

func LoadConfig(baseDir string) (Config, error) {
	path := ConfigPath(baseDir)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.Normalize()
	return cfg, nil
}

func SaveConfig(baseDir string, cfg Config) error {
	cfg.Normalize()
	// The legacy names have done their job once the layout exists; keeping them
	// around would leave two disagreeing sources of truth in the file.
	if cfg.Layout.TVKey != "" {
		cfg.LegacyTVProfile = ""
		cfg.LegacyDeskProfile = ""
		cfg.LegacyDesk = ""
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(ConfigPath(baseDir), append(data, '\n'), 0o644)
}

func (c *Config) Normalize() {
	if c.CloseAppsWaitSeconds <= 0 {
		c.CloseAppsWaitSeconds = DefaultCloseAppsWaitSeconds
	}
	c.Layout.TVKey = strings.TrimSpace(c.Layout.TVKey)
	c.Layout.TVName = strings.TrimSpace(c.Layout.TVName)
	c.Layout.Mode = strings.TrimSpace(c.Layout.Mode)
	if c.Layout.Desk == "" {
		c.Layout.Desk = DeskDisabled
	}
	c.AppsToClose = SanitizeApps(c.AppsToClose)
}

// GamescopeSettings configures the nested gamescope session. It is off by
// default: gamescope is a separate package, and running Steam inside it changes
// how every game is presented.
type GamescopeSettings struct {
	Enabled bool `json:"enabled,omitempty"`
	// FPSLimit caps the frame rate; 0 leaves it uncapped.
	FPSLimit int `json:"fps_limit,omitempty"`
	// MangoApp overlays the performance HUD.
	MangoApp bool `json:"mangoapp,omitempty"`
}

// HookEnabled reports whether a session hook should run. Absence means on.
func (c Config) HookEnabled(name string) bool {
	if c.Hooks == nil {
		return true
	}
	enabled, present := c.Hooks[name]
	return !present || enabled
}

// SetHookEnabled records a hook choice.
func (c *Config) SetHookEnabled(name string, enabled bool) {
	if c.Hooks == nil {
		c.Hooks = map[string]bool{}
	}
	c.Hooks[name] = enabled
}

// Configured reports whether a console layout has been chosen yet.
func (c Config) Configured() bool {
	return c.Layout.TVKey != ""
}

// EnsureConsoleProfile makes the generated profile match the configured layout,
// choosing a layout first if there is none.
//
// It runs before every session as well as on enable, so a profile that was
// edited or invalidated elsewhere -- a display swapped, a mode withdrawn -- is
// repaired rather than applied as-is.
func EnsureConsoleProfile(store *profile.Store, monitors []hypr.Monitor, cfg *Config) (profile.Profile, bool, error) {
	changed := false

	if !cfg.Configured() {
		seeded, ok := seedLayoutFromLegacy(store, monitors, *cfg)
		if !ok {
			suggested, err := SuggestConsoleLayout(monitors, LiveDisplayFacts())
			if err != nil {
				return profile.Profile{}, false, err
			}
			seeded = suggested
		}
		cfg.Layout = seeded
		changed = true
	}

	if err := ValidateConsoleLayout(cfg.Layout, monitors); err != nil {
		repaired, suggestErr := SuggestConsoleLayout(monitors, LiveDisplayFacts())
		if suggestErr != nil {
			return profile.Profile{}, changed, err
		}
		// Keep the choices that are still expressible; only the broken parts
		// are replaced, so a withdrawn mode does not also reset HDR and VRR.
		repaired.HDR = repaired.HDR && cfg.Layout.HDR
		repaired.VRR = cfg.Layout.VRR
		repaired.Desk = cfg.Layout.Desk
		if ValidateConsoleLayout(repaired, monitors) != nil {
			return profile.Profile{}, changed, err
		}
		cfg.Layout = repaired
		changed = true
	}

	// Keep the connector label current so the UI can name the TV even when it
	// is asleep.
	if tv, ok := monitorByKey(monitors, cfg.Layout.TVKey); ok && cfg.Layout.TVName != tv.Name {
		cfg.Layout.TVName = tv.Name
		changed = true
	}

	built, err := BuildConsoleProfile(cfg.Layout, monitors)
	if err != nil {
		return profile.Profile{}, changed, err
	}
	if err := store.Save(built); err != nil {
		return profile.Profile{}, changed, err
	}
	return built, changed, nil
}

// seedLayoutFromLegacy carries a hand-picked TV profile over to the generated
// one, so upgrading does not throw away a layout the user already tuned.
func seedLayoutFromLegacy(store *profile.Store, monitors []hypr.Monitor, cfg Config) (ConsoleLayout, bool) {
	if strings.TrimSpace(cfg.LegacyTVProfile) == "" || store == nil {
		return ConsoleLayout{}, false
	}
	old, err := store.Load(cfg.LegacyTVProfile)
	if err != nil {
		return ConsoleLayout{}, false
	}

	desk := cfg.LegacyDesk
	if desk == "" {
		desk = DeskDisabled
	}

	// The TV in the old profile is the enabled output that is not mirroring
	// something else; with several, the largest wins.
	var best profile.OutputConfig
	found := false
	for _, out := range old.Outputs {
		if !out.Enabled {
			continue
		}
		if !found || out.Width*out.Height > best.Width*best.Height {
			best, found = out, true
		}
	}
	if !found {
		return ConsoleLayout{}, false
	}
	if _, ok := monitorByKey(monitors, best.Key); !ok {
		return ConsoleLayout{}, false
	}

	layout := ConsoleLayout{
		TVKey:  best.Key,
		TVName: best.Name,
		Mode:   best.Mode,
		HDR:    strings.EqualFold(best.CM, "hdr"),
		VRR:    best.VRR != 0,
		Desk:   desk,
	}
	if ValidateConsoleLayout(layout, monitors) != nil {
		return ConsoleLayout{}, false
	}
	return layout, true
}
