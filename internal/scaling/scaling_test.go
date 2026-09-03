package scaling

import "testing"

func TestRoundPreservesTypedScale(t *testing.T) {
	got := Round(1.33)
	if got != 1.33 {
		t.Fatalf("expected 1.33 scale to remain explicit, got %v", got)
	}
}

func TestClosestSharpFindsNearbySharpScale(t *testing.T) {
	got, ok := ClosestSharp(3840, 2160, 1.33)
	if !ok {
		t.Fatal("expected closest sharp scale")
	}
	if got != 1.33333 {
		t.Fatalf("expected 4K 1.33 scale to suggest 1.33333, got %v", got)
	}
}

func TestClosestSharpUsesHyprlandScaleGrid(t *testing.T) {
	got, ok := ClosestSharp(2560, 1600, 1.5)
	if !ok {
		t.Fatal("expected closest sharp scale")
	}
	if got != 1.6 {
		t.Fatalf("expected 2560x1600 at 1.5 to suggest 1.6, got %v", got)
	}
}

func TestSharpMatchesHyprlandScaleRules(t *testing.T) {
	for _, scale := range []float64{1, 1.28, 1.33333, 1.6, 2.66667} {
		if !Sharp(2560, 1600, scale) {
			t.Errorf("expected %v to be sharp", scale)
		}
	}
	for _, scale := range []float64{1.42857, 1.77778, 2.85714} {
		if Sharp(2560, 1600, scale) {
			t.Errorf("expected %v to be rejected", scale)
		}
	}
}

func TestRoundLeavesNonSharpScaleUnchanged(t *testing.T) {
	got := Round(1.37)
	if got != 1.37 {
		t.Fatalf("expected 1.37 to remain unchanged, got %v", got)
	}
	if Sharp(3840, 2160, got) {
		t.Fatalf("expected 1.37 to be detected as non-sharp")
	}
}

func TestFormatPreservesNeededScalePrecision(t *testing.T) {
	if got := Format(1.33333); got != "1.33333" {
		t.Fatalf("expected 5-decimal scale formatting, got %q", got)
	}
	if got := Format(1.50000); got != "1.5" {
		t.Fatalf("expected trailing zeros to be trimmed, got %q", got)
	}
	if got := Format(4.5); got != "4.5" {
		t.Fatalf("expected positive scales to format without UI clamping, got %q", got)
	}
}
