package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crmne/hyprmoncfg/internal/config"
)

func TestConfirmApplySignalRejectsConfiguration(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	signals := make(chan os.Signal, 1)
	signals <- os.Interrupt
	var output bytes.Buffer

	keep, err := confirmApplyWithInput(10, reader, &output, signals)
	if err != nil {
		t.Fatalf("confirm after signal: %v", err)
	}
	if keep {
		t.Fatal("expected signal to reject unconfirmed configuration")
	}
}

func TestDoctorReportsMissingGeneratedMonitorConfig(t *testing.T) {
	dir := t.TempDir()
	rootPath := filepath.Join(dir, "hyprland.lua")
	monitorsPath := filepath.Join(dir, "hyprmoncfg-monitors.lua")
	if err := os.WriteFile(rootPath, []byte(config.IncludeLine(config.HyprConfigLua, monitorsPath)+"\n"), 0o644); err != nil {
		t.Fatalf("write root config: %v", err)
	}

	cmd := newDoctorCmd(&monitorsPath, &rootPath)
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor: %v", err)
	}

	got := output.String()
	if !strings.Contains(got, "PROBLEM") || !strings.Contains(got, "does not exist") {
		t.Fatalf("doctor output did not report the missing generated file:\n%s", got)
	}
}
