package render

import (
	"testing"

	"github.com/crmne/hyprmoncfg/internal/hypr"
)

func TestHyprlangEscapePreservesLiteralSyntax(t *testing.T) {
	got := hyprlangEscape(`desc:Dell #HASH {{raw}} {{{overlap}} path\{panel}`)
	want := `desc:Dell ##HASH \{\{raw}} \{\{\{overlap}} path\\{panel}`
	if got != want {
		t.Fatalf("hyprlangEscape() = %q, want %q", got, want)
	}
}

func TestHyprlangEscapeNormalizesLineBreaks(t *testing.T) {
	got := hyprlangEscape("left\nright\r\nthird\rfourth")
	want := "left right third fourth"
	if got != want {
		t.Fatalf("hyprlangEscape() = %q, want %q", got, want)
	}
}

func TestLegacyHyprlangSelectorFallsBackForUnsafeDescSyntax(t *testing.T) {
	monitor := hypr.Monitor{Name: "DP-1"}

	tests := []string{
		"desc:Dell $PANEL",
		"desc:Dell, Inc.",
		"desc:Dell\nPanel",
	}
	for _, selector := range tests {
		if got := legacyHyprlangSelector(selector, monitor); got != "DP-1" {
			t.Fatalf("legacyHyprlangSelector(%q) = %q, want DP-1", selector, got)
		}
	}

	safe := "desc:Dell #HASH {{raw}}"
	if got := legacyHyprlangSelector(safe, monitor); got != safe {
		t.Fatalf("legacyHyprlangSelector(%q) = %q, want unchanged selector", safe, got)
	}
}

func TestLuaQuoteEscapesLuaStringSyntax(t *testing.T) {
	got := luaQuote("desc:Dell #$,\n\"\\\x01")
	want := `"desc:Dell #$,\n\"\\\001"`
	if got != want {
		t.Fatalf("luaQuote() = %q, want %q", got, want)
	}
}
