package appstatus

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/crmne/hyprmoncfg/internal/hypr"
	"github.com/crmne/hyprmoncfg/internal/profile"
)

func TestBuildMarksActiveAndRecommendedProfile(t *testing.T) {
	monitor := hypr.Monitor{
		Name:        "eDP-1",
		Description: "Laptop panel",
		Make:        "Framework",
		Model:       "Display",
		Serial:      "123",
		Width:       2256,
		Height:      1504,
		RefreshRate: 60,
		Scale:       1.5,
	}
	saved := profile.FromState("Laptop", []hypr.Monitor{monitor}, nil)
	saved.UpdatedAt = time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)

	document := Build("1.11.0", true, []profile.Profile{saved}, []hypr.Monitor{monitor}, nil)

	if document.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", document.SchemaVersion, SchemaVersion)
	}
	if document.ActiveProfile == nil || document.ActiveProfile.Name != "Laptop" {
		t.Fatalf("active profile = %#v, want Laptop", document.ActiveProfile)
	}
	if document.RecommendedProfile == nil || document.RecommendedProfile.Name != "Laptop" {
		t.Fatalf("recommended profile = %#v, want Laptop", document.RecommendedProfile)
	}
	if !document.Daemon.Running {
		t.Fatal("expected daemon to be running")
	}
	if len(document.Profiles) != 1 || !document.Profiles[0].Active || !document.Profiles[0].Recommended {
		t.Fatalf("profile summary = %#v", document.Profiles)
	}
	if document.Profiles[0].ConnectedEnabledOutputs != 1 || document.Profiles[0].MatchScore != 100 {
		t.Fatalf("profile match metadata = %#v", document.Profiles[0])
	}
	if len(document.Monitors) != 1 || !document.Monitors[0].Enabled {
		t.Fatalf("monitor summary = %#v", document.Monitors)
	}
	monitorSummary := document.Monitors[0]
	if monitorSummary.Name != "eDP-1" || monitorSummary.Make != "Framework" || monitorSummary.Model != "Display" {
		t.Fatalf("monitor identity = %#v", monitorSummary)
	}
	if monitorSummary.Mode != "2256x1504@60.00Hz" || monitorSummary.Width != 2256 || monitorSummary.Height != 1504 || monitorSummary.RefreshRate != 60 {
		t.Fatalf("monitor mode = %#v", monitorSummary)
	}
	if monitorSummary.LogicalWidth != 1504 || monitorSummary.LogicalHeight != 1003 || monitorSummary.Scale != 1.5 || !monitorSummary.Internal {
		t.Fatalf("monitor layout metadata = %#v", monitorSummary)
	}
}

func TestBuildMarksProfilesWithoutConnectedEnabledOutputs(t *testing.T) {
	connected := hypr.Monitor{Name: "eDP-1", Make: "Framework", Model: "Display", Serial: "123"}
	projector := hypr.Monitor{Name: "HDMI-A-1", Make: "Epson", Model: "Projector", Serial: "P1"}
	available := profile.New("Laptop", []profile.OutputConfig{{Key: connected.HardwareKey(), Enabled: true, Scale: 1}})
	unavailable := profile.New("Projector", []profile.OutputConfig{{Key: projector.HardwareKey(), Enabled: true, Scale: 1}})

	document := Build("dev", true, []profile.Profile{available, unavailable}, []hypr.Monitor{connected}, nil)
	if document.Profiles[0].ConnectedEnabledOutputs != 1 || document.Profiles[0].MatchScore != 100 {
		t.Fatalf("available profile = %#v", document.Profiles[0])
	}
	if document.Profiles[1].ConnectedEnabledOutputs != 0 || document.Profiles[1].MatchScore != 0 {
		t.Fatalf("unavailable profile = %#v", document.Profiles[1])
	}
}

func TestBuildUsesStableEmptyCollectionsAndNullMatches(t *testing.T) {
	document := Build("dev", false, nil, nil, nil)

	data, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}
	jsonText := string(data)
	for _, expected := range []string{
		`"active_profile":null`,
		`"recommended_profile":null`,
		`"profiles":[]`,
		`"monitors":[]`,
	} {
		if !strings.Contains(jsonText, expected) {
			t.Fatalf("JSON %q does not contain %q", jsonText, expected)
		}
	}
}
