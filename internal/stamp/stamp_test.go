package stamp

import (
	"strings"
	"testing"
)

func TestEscapePS(t *testing.T) {
	cases := map[string]string{
		`plain`:             `plain`,
		`a(b)c`:             `a\(b\)c`,
		`back\slash`:        `back\\slash`,
		`--opt "x" (Legal)`: `--opt "x" \(Legal\)`,
	}
	for in, want := range cases {
		if got := escapePS(in); got != want {
			t.Errorf("escapePS(%q) = %q, want %q", in, got, want)
		}
	}
	// Non-ASCII bytes are replaced with spaces so the prolog stays valid ASCII.
	if got := escapePS("café"); got != "caf " {
		t.Errorf("escapePS(non-ascii) = %q, want %q", got, "caf ")
	}
}

func TestBuildProlog(t *testing.T) {
	ps := BuildProlog("TITLE", []string{"when: now", "cmd: pdfprint (x)"})

	// Structural expectations: an EndPage overlay that transmits real pages.
	for _, want := range []string{
		"/EndPage",
		"exch pop 2 ne dup",
		"setpagedevice",
		"(TITLE) show",
		"(when: now) show",
		`(cmd: pdfprint \(x\)) show`, // parens escaped
	} {
		if !strings.Contains(ps, want) {
			t.Errorf("prolog missing %q\n---\n%s", want, ps)
		}
	}

	// gsave/grestore must balance so the overlay never leaks graphics state.
	if g, r := strings.Count(ps, "gsave"), strings.Count(ps, "grestore"); g != r || g != 1 {
		t.Errorf("gsave=%d grestore=%d, want 1/1", g, r)
	}
}

func TestBuildPrologNoTitle(t *testing.T) {
	ps := BuildProlog("", []string{"only a line"})
	if strings.Contains(ps, "Courier-Bold") {
		t.Error("empty title should not emit a bold header font")
	}
	if !strings.Contains(ps, "(only a line) show") {
		t.Error("body line missing")
	}
}
