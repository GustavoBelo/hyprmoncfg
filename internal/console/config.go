package console

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/crmne/hyprmoncfg/internal/config"
)

// Config is the whole of what console mode needs to be told.
//
// It is deliberately small. The spike that produced this design found that the
// mode, HDR and VRR are not ours to choose: gamescope reads the connector's
// preferred mode and Steam changes it per game from its own UI, so a resolution
// recorded here would be a second, disagreeing source of truth. What is left is
// which display, which desktop to come back to, and what to close on the way.
type Config struct {
	Enabled bool `json:"enabled"`
	// TVKey identifies the display by EDID, so it survives being replugged into
	// a different connector.
	TVKey string `json:"tv_key,omitempty"`
	// TVName is the connector, which is what gamescope takes as OUTPUT_CONNECTOR.
	TVName string `json:"tv_name,omitempty"`
	// TVDescription is the EDID description, matched against ALSA's ELD to find
	// which HDMI audio pin belongs to this display.
	TVDescription string `json:"tv_description,omitempty"`
	// DesktopSession is the session entry to come back to, by file name.
	DesktopSession string `json:"desktop_session,omitempty"`
	// Boot says where a fresh login starts: at the desktop, in the console, or
	// wherever the last session ended. Empty means the desktop.
	Boot BootMode `json:"boot,omitempty"`
	// EnterOnControllerConnect turns the machine into a console when a pad is
	// switched on. Off by default: it now ends the desktop session, which is
	// not something to start doing because a controller woke up.
	EnterOnControllerConnect bool `json:"enter_on_controller_connect,omitempty"`
}

func ConfigPath(baseDir string) string { return filepath.Join(baseDir, "console.json") }

func LoadConfig(baseDir string) (Config, error) {
	data, err := os.ReadFile(ConfigPath(baseDir))
	if errors.Is(err, os.ErrNotExist) {
		// A machine upgrading from couch mode has its TV recorded in the file
		// this one replaces. Seeding from it beats making the user choose the
		// same display a second time; it is not written back until something
		// else saves.
		if migrated, ok := MigrateFromCouch(baseDir); ok {
			return migrated, nil
		}
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.Normalize()
	return cfg, nil
}

func SaveConfig(baseDir string, cfg Config) error {
	cfg.Normalize()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(ConfigPath(baseDir), append(data, '\n'), 0o644)
}

func (c *Config) Normalize() {
	c.TVKey = strings.TrimSpace(c.TVKey)
	c.TVName = strings.TrimSpace(c.TVName)
	c.TVDescription = strings.TrimSpace(c.TVDescription)
	c.DesktopSession = strings.TrimSpace(c.DesktopSession)
	if !c.Boot.Valid() {
		c.Boot = BootDesktop
	}
}

// Configured reports whether a TV has been chosen.
func (c Config) Configured() bool { return c.TVName != "" }

// MigrateFromCouch seeds a console config from the couch-mode file it replaces,
// keeping the choices that still mean something.
//
// The layout is not among them: couch mode generated a Hyprland profile that
// mirrored or disabled the desk display, and that is exactly the part gamescope
// does for itself. Only the identity of the TV and the app list carry over.
func MigrateFromCouch(baseDir string) (Config, bool) {
	data, err := os.ReadFile(filepath.Join(baseDir, "couch.json"))
	if err != nil {
		return Config{}, false
	}
	var old struct {
		Enabled bool `json:"enabled"`
		Layout  struct {
			TVKey  string `json:"tv_key"`
			TVName string `json:"tv_name"`
		} `json:"layout"`
		EnterOnControllerConnect bool `json:"enter_on_controller_connect"`
	}
	if json.Unmarshal(data, &old) != nil {
		return Config{}, false
	}
	if strings.TrimSpace(old.Layout.TVName) == "" {
		return Config{}, false
	}
	return Config{
		Enabled: old.Enabled,
		TVKey:   old.Layout.TVKey,
		TVName:  old.Layout.TVName,
		// Deliberately not carried over: entering now ends the desktop session,
		// so this has to be chosen again with that in mind.
		EnterOnControllerConnect: false,
	}, true
}
