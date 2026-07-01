package ppd

import (
	"path/filepath"
	"strings"
	"testing"
)

func loadSample(t *testing.T) *PPD {
	t.Helper()
	p, err := ParseFile(filepath.Join("..", "..", "testdata", "sample.ppd"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	return p
}

func TestIdentityFields(t *testing.T) {
	p := loadSample(t)
	if p.Manufacturer != "HP" {
		t.Errorf("Manufacturer = %q, want HP", p.Manufacturer)
	}
	if !strings.Contains(p.NickName, "PCL-XL") {
		t.Errorf("NickName = %q, want it to mention PCL-XL", p.NickName)
	}
	if !p.ColorDevice {
		t.Errorf("ColorDevice = false, want true")
	}
	if p.DefaultResolution != "600dpi" {
		t.Errorf("DefaultResolution = %q, want 600dpi", p.DefaultResolution)
	}
}

func TestFoomaticCommandLine(t *testing.T) {
	p := loadSample(t)
	if !strings.Contains(p.FoomaticRIPCommandLine, "-sDEVICE=pxlcolor") {
		t.Errorf("FoomaticRIPCommandLine = %q, want it to contain -sDEVICE=pxlcolor", p.FoomaticRIPCommandLine)
	}
}

func TestPageSizeOption(t *testing.T) {
	p := loadSample(t)
	opt := p.Option("PageSize")
	if opt == nil {
		t.Fatal("PageSize option missing")
	}
	if opt.Default != "Letter" {
		t.Errorf("PageSize default = %q, want Letter", opt.Default)
	}
	if len(opt.Choices) != 3 {
		t.Fatalf("PageSize choices = %d, want 3", len(opt.Choices))
	}
	a4 := opt.Choice("A4")
	if a4 == nil {
		t.Fatal("A4 choice missing")
	}
	if !strings.Contains(a4.Code, "595 842") {
		t.Errorf("A4 code = %q, want it to contain the A4 dimensions", a4.Code)
	}
	if dc := opt.DefaultChoice(); dc == nil || dc.Keyword != "Letter" {
		t.Errorf("DefaultChoice = %v, want Letter", dc)
	}
}

func TestDuplexOption(t *testing.T) {
	p := loadSample(t)
	opt := p.Option("Duplex")
	if opt == nil {
		t.Fatal("Duplex option missing")
	}
	if opt.Choice("DuplexNoTumble") == nil {
		t.Error("DuplexNoTumble choice missing")
	}
}
