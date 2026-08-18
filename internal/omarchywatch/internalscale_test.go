package omarchywatch

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// omarchyScaleFormat is the validator Omarchy applies to whatever it reads back.
var omarchyScaleFormat = regexp.MustCompile(`^[0-9]+([.][0-9]+)?$`)

func withFakeClamshell(t *testing.T) string {
	t.Helper()

	binDir := t.TempDir()
	script := filepath.Join(binDir, clamshellCommand)
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake clamshell: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	return filepath.Join(stateHome, togglesStateDir, internalScaleFile)
}

func TestStoreInternalScaleWritesWhatOmarchyCanReadBack(t *testing.T) {
	path := withFakeClamshell(t)

	for _, tc := range []struct {
		scale float64
		want  string
	}{
		{1.33333, "1.33333"},
		{2, "2"},
		{1.5, "1.5"},
	} {
		if err := StoreInternalScale(tc.scale); err != nil {
			t.Fatalf("StoreInternalScale(%v): %v", tc.scale, err)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read scale state: %v", err)
		}
		value := strings.TrimSuffix(string(content), "\n")
		if value != tc.want {
			t.Fatalf("stored %q for %v, want %q", value, tc.scale, tc.want)
		}
		if !omarchyScaleFormat.MatchString(value) {
			t.Fatalf("Omarchy would reject %q as a scale", value)
		}
	}
}

func TestStoreInternalScaleIgnoresValuesOmarchyWouldReject(t *testing.T) {
	path := withFakeClamshell(t)

	for _, scale := range []float64{0, -1} {
		if err := StoreInternalScale(scale); err != nil {
			t.Fatalf("StoreInternalScale(%v): %v", scale, err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected no scale state to be written, got err=%v", err)
	}
}

func TestStoreInternalScaleStaysOffSystemsWithoutOmarchy(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("PATH", t.TempDir())

	if err := StoreInternalScale(1.5); err != nil {
		t.Fatalf("StoreInternalScale: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateHome, "omarchy")); !os.IsNotExist(err) {
		t.Fatalf("expected no Omarchy state directory to be created, got err=%v", err)
	}
}
