package main

import (
	"bytes"
	"io"
	"os"
	"testing"
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
