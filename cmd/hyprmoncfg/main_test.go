package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/crmne/hyprmoncfg/internal/apply"
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

func TestConfirmUnmanagedOverwriteRequiresExplicitYes(t *testing.T) {
	collision := &apply.UnmanagedMonitorConfigError{
		Path:            "/tmp/monitors.lua",
		AlternativePath: "/tmp/hyprmoncfg-monitors.lua",
	}

	for _, tt := range []struct {
		answer string
		want   bool
	}{
		{answer: "y\n", want: true},
		{answer: "yes\n", want: true},
		{answer: "\n", want: false},
		{answer: "n\n", want: false},
	} {
		var output bytes.Buffer
		got, err := confirmUnmanagedOverwrite(strings.NewReader(tt.answer), &output, nil, collision)
		if err != nil {
			t.Fatalf("confirm overwrite %q: %v", tt.answer, err)
		}
		if got != tt.want {
			t.Fatalf("confirm overwrite %q = %v, want %v", tt.answer, got, tt.want)
		}
		for _, wantText := range []string{collision.Path, collision.AlternativePath} {
			if !strings.Contains(output.String(), wantText) {
				t.Fatalf("expected prompt to contain %q, got %q", wantText, output.String())
			}
		}
	}
}
