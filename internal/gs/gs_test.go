package gs

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/catprintlabs/pdfprint/internal/ppd"
)

func loadPPD(t *testing.T) *ppd.PPD {
	t.Helper()
	p, err := ppd.ParseFile(filepath.Join("..", "..", "testdata", "sample.ppd"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	return p
}

func argsStr(c Command) string { return strings.Join(c.Args, " ") }

func mustContain(t *testing.T, hay, needle string) {
	t.Helper()
	if !strings.Contains(hay, needle) {
		t.Errorf("args missing %q\n  in: %s", needle, hay)
	}
}

func mustNotContain(t *testing.T, hay, needle string) {
	t.Helper()
	if strings.Contains(hay, needle) {
		t.Errorf("args unexpectedly contain %q\n  in: %s", needle, hay)
	}
}

// The core requirement: 8.5x14 Legal, no scaling.
func TestLegalNoScaling_PPD(t *testing.T) {
	p := loadPPD(t)
	cmd, err := Build(p, Options{Device: "pxlmono", PageSize: "Legal", InputPath: "job.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	a := argsStr(cmd)
	mustContain(t, a, "-dDEVICEWIDTHPOINTS=612")
	mustContain(t, a, "-dDEVICEHEIGHTPOINTS=1008")
	mustContain(t, a, "-dFIXEDMEDIA")
	mustContain(t, a, "-dPDFFitPage=false") // strict 1:1
	mustNotContain(t, a, "-dPDFFitPage ")   // must not enable fit
}

// Same must work with no PPD, from the built-in size table.
func TestLegalNoScaling_BuiltIn(t *testing.T) {
	cmd, err := Build(nil, Options{Device: "pxlmono", PageSize: "Legal", InputPath: "job.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	a := argsStr(cmd)
	mustContain(t, a, "-dDEVICEWIDTHPOINTS=612")
	mustContain(t, a, "-dDEVICEHEIGHTPOINTS=1008")
	mustContain(t, a, "-dFIXEDMEDIA")
}

func TestFitEnablesScaling(t *testing.T) {
	cmd, err := Build(nil, Options{Device: "pxlmono", PageSize: "Legal", Fit: true, InputPath: "job.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	a := argsStr(cmd)
	mustContain(t, a, "-dPDFFitPage")
	mustNotContain(t, a, "-dPDFFitPage=false")
}

func TestDeviceInferredFromPPD(t *testing.T) {
	p := loadPPD(t)
	cmd, err := Build(p, Options{InputPath: "job.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Device != "pxlcolor" { // sample PPD's Foomatic line names pxlcolor
		t.Errorf("Device = %q, want pxlcolor", cmd.Device)
	}
}

func TestCopiesAndDuplex(t *testing.T) {
	cmd, err := Build(nil, Options{Device: "pxlcolor", Copies: 3, Duplex: DuplexLong, InputPath: "job.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	a := argsStr(cmd)
	mustContain(t, a, "/NumCopies 3")
	mustContain(t, a, "/Duplex true /Tumble false")
}

func TestNoDeviceIsError(t *testing.T) {
	if _, err := Build(nil, Options{InputPath: "job.pdf"}); err == nil {
		t.Fatal("expected error when no device can be determined")
	}
}

func TestResolvePageDims(t *testing.T) {
	p := loadPPD(t)
	// From *PaperDimension.
	if w, h, ok := resolvePageDims(p, "Legal"); !ok || w != 612 || h != 1008 {
		t.Errorf("Legal dims = %d,%d ok=%v; want 612,1008,true", w, h, ok)
	}
	// From built-in table, no PPD.
	if w, h, ok := resolvePageDims(nil, "a4"); !ok || w != 595 || h != 842 {
		t.Errorf("a4 dims = %d,%d ok=%v; want 595,842,true", w, h, ok)
	}
	// Unknown, no PPD -> falls back to PDF MediaBox (ok=false).
	if _, _, ok := resolvePageDims(nil, "bogus"); ok {
		t.Error("bogus size should not resolve")
	}
}
