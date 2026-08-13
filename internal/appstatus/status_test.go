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
	if len(document.Monitors) != 1 || !document.Monitors[0].Enabled {
		t.Fatalf("monitor summary = %#v", document.Monitors)
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
