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
	Name           string    `json:"name"`
	OutputCount    int       `json:"output_count"`
	EnabledOutputs int       `json:"enabled_outputs"`
	UpdatedAt      time.Time `json:"updated_at"`
	Active         bool      `json:"active"`
	Recommended    bool      `json:"recommended"`
}

type MonitorSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
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
		enabledOutputs := 0
		for _, output := range saved.Outputs {
			if output.Enabled {
				enabledOutputs++
			}
		}
		document.Profiles = append(document.Profiles, ProfileSummary{
			Name:           saved.Name,
			OutputCount:    len(saved.Outputs),
			EnabledOutputs: enabledOutputs,
			UpdatedAt:      saved.UpdatedAt,
			Active:         saved.Name == activeName,
			Recommended:    saved.Name == recommendedName,
		})
	}

	for _, monitor := range monitors {
		document.Monitors = append(document.Monitors, MonitorSummary{
			Name:        monitor.Name,
			Description: monitor.Description,
			Enabled:     !monitor.Disabled,
		})
	}

	return document
}
