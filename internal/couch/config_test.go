package couch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/crmne/hyprmoncfg/internal/profile"
)

func TestLoadConfigMissingFileReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("a missing config must not read as enabled")
	}
	if cfg.CloseAppsWaitSeconds != DefaultCloseAppsWaitSeconds {
		t.Fatalf("wait seconds = %d, want %d", cfg.CloseAppsWaitSeconds, DefaultCloseAppsWaitSeconds)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Config{
		Enabled: true,
		Layout: ConsoleLayout{
			TVKey:  "samsung|tv",
			TVName: "HDMI-A-1",
			Mode:   "2560x1440@120.00Hz",
			HDR:    true,
			VRR:    true,
			Desk:   DeskMirror,
		},
		ExitOnControllersOff: true,
		CloseAppsEnabled:     true,
		CloseAppsWaitSeconds: 9,
		AppsToClose:          []string{"retroarch"},
	}
	if err := SaveConfig(dir, want); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.Layout != want.Layout {
		t.Fatalf("layout round trip: got %+v, want %+v", got.Layout, want.Layout)
	}
	if !got.Enabled || !got.ExitOnControllersOff || !got.CloseAppsEnabled {
		t.Fatalf("flags lost in round trip: %+v", got)
	}
	if got.CloseAppsWaitSeconds != 9 {
		t.Fatalf("wait seconds = %d, want 9", got.CloseAppsWaitSeconds)
	}
}

func TestNormalizeFillsDeskDefault(t *testing.T) {
	cfg := Config{}
	cfg.Normalize()
	if cfg.Layout.Desk != DeskDisabled {
		t.Fatalf("desk default = %q, want %q", cfg.Layout.Desk, DeskDisabled)
	}
}

func TestLoadConfigRejectsCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(ConfigPath(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadConfig(dir); err == nil {
		t.Fatal("corrupt config must be reported, not silently reset")
	}
}

// Upgrading must not throw away a TV layout the user already tuned by hand.
func TestEnsureConsoleProfileSeedsFromTheOldTVProfile(t *testing.T) {
	dir := t.TempDir()
	store := profile.NewStore(dir)
	if err := store.Ensure(); err != nil {
		t.Fatalf("ensure store: %v", err)
	}

	monitors := hostMonitors()
	tv := monitors[0]

	// The shape of the user's own "game(tv-only)": TV enabled at 2560x1440 HDR,
	// desk disabled.
	old := profile.New("game(tv-only)", []profile.OutputConfig{
		{
			Key: tv.HardwareKey(), MatchKey: tv.HardwareKey(), Name: tv.Name,
			Enabled: true, Mode: "2560x1440@120.00Hz",
			Width: 2560, Height: 1440, Refresh: 120, Scale: 1, CM: "hdr", VRR: 1,
		},
	})
	if err := store.Save(old); err != nil {
		t.Fatalf("save legacy profile: %v", err)
	}

	cfg := Config{Enabled: true, LegacyTVProfile: "game(tv-only)", LegacyDesk: DeskDisabled}
	cfg.Normalize()

	built, changed, err := EnsureConsoleProfile(store, monitors, &cfg)
	if err != nil {
		t.Fatalf("EnsureConsoleProfile: %v", err)
	}
	if !changed {
		t.Fatal("seeding a layout should be reported as a change")
	}
	if cfg.Layout.TVKey != tv.HardwareKey() {
		t.Fatalf("the TV should carry over, got %q", cfg.Layout.TVKey)
	}
	if cfg.Layout.Mode != "2560x1440@120.00Hz" {
		t.Fatalf("the tuned mode should carry over, got %q", cfg.Layout.Mode)
	}
	if !cfg.Layout.HDR || !cfg.Layout.VRR {
		t.Fatalf("HDR and VRR should carry over, got %+v", cfg.Layout)
	}
	if built.Name != ConsoleProfileName {
		t.Fatalf("the generated profile should be %q, got %q", ConsoleProfileName, built.Name)
	}
	if _, err := store.Load(ConsoleProfileName); err != nil {
		t.Fatalf("the generated profile should be saved: %v", err)
	}
}

// Once a layout exists the legacy names must not linger; two sources of truth
// in one file is how the roles got swapped in the first place.
func TestSaveDropsLegacyProfileNames(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		Enabled:           true,
		Layout:            ConsoleLayout{TVKey: "k", TVName: "HDMI-A-1", Mode: "1920x1080@60.00Hz", Desk: DeskDisabled},
		LegacyTVProfile:   "game",
		LegacyDeskProfile: "escritório",
		LegacyDesk:        DeskEnabled,
	}
	if err := SaveConfig(dir, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "couch.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, gone := range []string{"tv_profile", "desk_profile", "desk_during_couch"} {
		if contains(string(data), gone) {
			t.Fatalf("%q should not survive into the saved config:\n%s", gone, data)
		}
	}
}

// A display that lost the configured mode must be repaired, not applied.
func TestEnsureConsoleProfileRepairsAnImpossibleMode(t *testing.T) {
	dir := t.TempDir()
	store := profile.NewStore(dir)
	if err := store.Ensure(); err != nil {
		t.Fatalf("ensure store: %v", err)
	}
	monitors := hostMonitors()

	cfg := Config{
		Enabled: true,
		Layout: ConsoleLayout{
			TVKey: monitors[0].HardwareKey(), TVName: "HDMI-A-1",
			Mode: "7680x4320@240.00Hz", HDR: true, VRR: true, Desk: DeskMirror,
		},
	}
	built, changed, err := EnsureConsoleProfile(store, monitors, &cfg)
	if err != nil {
		t.Fatalf("EnsureConsoleProfile: %v", err)
	}
	if !changed {
		t.Fatal("repairing a broken layout should be reported as a change")
	}
	if cfg.Layout.Mode == "7680x4320@240.00Hz" {
		t.Fatal("the impossible mode survived")
	}
	// Choices that are still expressible must survive the repair.
	if cfg.Layout.Desk != DeskMirror || !cfg.Layout.VRR {
		t.Fatalf("repair reset unrelated choices: %+v", cfg.Layout)
	}
	if len(built.Outputs) == 0 {
		t.Fatal("repair produced an empty profile")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
