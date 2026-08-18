package appstatus

import (
	"time"

	"github.com/crmne/hyprmoncfg/internal/hypr"
	"github.com/crmne/hyprmoncfg/internal/profile"
)

const SchemaVersion = 1

type Document struct {
	SchemaVersion      int               `json:"schema_version"`
	Version            string            `json:"version"`
	Daemon             Daemon            `json:"daemon"`
	ActiveProfile      *ProfileReference `json:"active_profile"`
	RecommendedProfile *ProfileMatch     `json:"recommended_profile"`
	Profiles           []ProfileSummary  `json:"profiles"`
	Monitors           []MonitorSummary  `json:"monitors"`
}

type Daemon struct {
	Running bool `json:"running"`
}

type ProfileReference struct {
	Name string `json:"name"`
}

type ProfileMatch struct {
	Name  string `json:"name"`
	Score int    `json:"score"`
}

type ProfileSummary struct {
	Name                    string    `json:"name"`
	OutputCount             int       `json:"output_count"`
	EnabledOutputs          int       `json:"enabled_outputs"`
	ConnectedEnabledOutputs int       `json:"connected_enabled_outputs"`
	MatchScore              int       `json:"match_score"`
	UpdatedAt               time.Time `json:"updated_at"`
	Active                  bool      `json:"active"`
	Recommended             bool      `json:"recommended"`
}

type MonitorSummary struct {
	Name          string  `json:"name"`
	Description   string  `json:"description"`
	Make          string  `json:"make"`
	Model         string  `json:"model"`
	Mode          string  `json:"mode"`
	Width         int     `json:"width"`
	Height        int     `json:"height"`
	RefreshRate   float64 `json:"refresh_rate"`
	X             int     `json:"x"`
	Y             int     `json:"y"`
	Scale         float64 `json:"scale"`
	Transform     int     `json:"transform"`
	LogicalWidth  int     `json:"logical_width"`
	LogicalHeight int     `json:"logical_height"`
	Internal      bool    `json:"internal"`
	Focused       bool    `json:"focused"`
	Enabled       bool    `json:"enabled"`
	// MirrorOf names the connector this monitor mirrors, empty when it drives
	// its own image. A mirroring monitor shares the position of its source, so
	// anything drawing a layout has to leave it out and name it separately.
	MirrorOf string `json:"mirror_of,omitempty"`
}

func Build(version string, daemonRunning bool, profiles []profile.Profile, monitors []hypr.Monitor, rules []hypr.WorkspaceRule) Document {
	document := Document{
		SchemaVersion: SchemaVersion,
		Version:       version,
		Daemon:        Daemon{Running: daemonRunning},
		Profiles:      make([]ProfileSummary, 0, len(profiles)),
		Monitors:      make([]MonitorSummary, 0, len(monitors)),
	}

	activeName := ""
	if active, ok := profile.ExactStateMatch(profiles, monitors, rules); ok {
		activeName = active.Name
		document.ActiveProfile = &ProfileReference{Name: active.Name}
	}

	recommendedName := ""
	if recommended, score, ok := profile.BestMatch(profiles, monitors); ok {
		recommendedName = recommended.Name
		document.RecommendedProfile = &ProfileMatch{Name: recommended.Name, Score: score}
	}

	for _, saved := range profiles {
		match := profile.EvaluateMatch(saved, monitors)
		enabledOutputs := 0
		for _, output := range saved.Outputs {
			if output.Enabled {
				enabledOutputs++
			}
		}
		document.Profiles = append(document.Profiles, ProfileSummary{
			Name:                    saved.Name,
			OutputCount:             len(saved.Outputs),
			EnabledOutputs:          enabledOutputs,
			ConnectedEnabledOutputs: match.ConnectedEnabledOutputs,
			MatchScore:              match.Score,
			UpdatedAt:               saved.UpdatedAt,
			Active:                  saved.Name == activeName,
			Recommended:             saved.Name == recommendedName,
		})
	}

	for _, monitor := range monitors {
		logicalWidth, logicalHeight := monitor.LogicalSize()
		document.Monitors = append(document.Monitors, MonitorSummary{
			Name:          monitor.Name,
			Description:   monitor.Description,
			Make:          monitor.Make,
			Model:         monitor.Model,
			Mode:          monitor.ModeString(),
			Width:         monitor.Width,
			Height:        monitor.Height,
			RefreshRate:   monitor.RefreshRate,
			X:             monitor.X,
			Y:             monitor.Y,
			Scale:         monitor.Scale,
			Transform:     monitor.Transform,
			LogicalWidth:  logicalWidth,
			LogicalHeight: logicalHeight,
			Internal:      monitor.IsInternal(),
			Focused:       monitor.Focused,
			Enabled:       !monitor.Disabled,
			MirrorOf:      monitor.MirrorOf,
		})
	}

	return document
}
