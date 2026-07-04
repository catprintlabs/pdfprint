// Package probe queries a network printer for its capabilities so the right
// Ghostscript device can be chosen without guessing. It asks the printer
// directly — IPP first (richest: accepted formats, color, duplex, model), then
// SNMP as a fallback (the printer MIB's interpreter list). No third-party deps;
// both protocols are encoded by hand, like the rest of this project.
package probe

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Caps is what a printer advertises about itself. Pointer fields are nil when the
// printer didn't report that attribute.
type Caps struct {
	Source         string   // "IPP" or "SNMP"
	Model          string   // printer-make-and-model / device description
	Formats        []string // IPP document-format-supported (MIME types)
	Languages      []string // page-description languages (from formats or the MIB)
	Color          *bool    // color-supported
	Duplex         *bool    // two-sided capable
	MediaReady     []string // media currently loaded in the trays (IPP media-ready)
	MediaDefault   string   // default media
	MediaSupported []string // all media sizes the printer accepts
	Trays          []Tray   // input trays and the paper loaded in each (IPP media-col-ready / SNMP prtInputTable)
}

// Tray is one input source and the media currently loaded in it. Fields are
// empty when the printer didn't report them (e.g. Size is "" for an empty or
// unreported tray; Type is IPP-only).
type Tray struct {
	Source string // media-source (IPP) or prtInputDescription (SNMP), e.g. "tray-1", "Tray 1", "manual"
	Size   string // loaded paper as a friendly label, e.g. "Letter", "A4", or "216x279mm"
	Type   string // media-type, e.g. "stationery" (IPP only; empty via SNMP)
}

// Desc renders a tray as one human line: "Tray 2: Letter (stationery)".
func (t Tray) Desc() string {
	size := t.Size
	if size == "" {
		size = "empty / not reported"
	}
	s := fmt.Sprintf("%s: %s", prettySource(t.Source), size)
	if t.Type != "" {
		s += " (" + t.Type + ")"
	}
	return s
}

// Probe asks the printer at host for its capabilities, IPP then SNMP. Returns an
// error only if neither answered — callers must NOT assume a device in that case.
func Probe(host string, timeout time.Duration) (*Caps, error) {
	if c, err := probeIPP(host, timeout); err == nil && c != nil {
		return c, nil
	}
	if c, err := probeSNMP(host, timeout); err == nil && c != nil {
		return c, nil
	}
	return nil, fmt.Errorf("printer at %s did not answer IPP (631) or SNMP (161)", host)
}

// SuggestDevice maps advertised capabilities to a Ghostscript device. colorFlag
// is the user's --color choice (nil = auto). ok is false when the printer
// advertised no page-description language we can target (caller must not guess).
func (c *Caps) SuggestDevice(colorFlag *bool) (device, reason string, ok bool) {
	// Resolve color: explicit --color wins, else what the printer reports, else color.
	wantColor := true
	colorSrc := "default"
	if colorFlag != nil {
		wantColor = *colorFlag
		colorSrc = "--color"
	} else if c.Color != nil {
		wantColor = *c.Color
		colorSrc = "printer"
	}

	// A printer that explicitly advertises only PCL5 (no PCL-XL) → PCL5.
	pclXL := c.has("pclxl", "pcl-xl", "pcl6", "pcl 6")
	pcl5only := c.has("pcl5", "pcl 5") && !pclXL

	switch {
	case pcl5only:
		return "ljet4", "PCL5", true
	case pclXL || c.has("pcl", "vnd.hp-pcl", "langpcl"):
		// Prefer native PCL-XL for any PCL-capable printer: compact and modern.
		// (Generic "PCL"/vnd.hp-PCL is treated as PCL-XL — PCL5-only devices that
		// advertise IPP are essentially extinct; override with --device if needed.)
		if wantColor {
			return "pxlcolor", fmt.Sprintf("PCL-XL, color (%s)", colorSrc), true
		}
		return "pxlmono", fmt.Sprintf("PCL-XL, mono (%s)", colorSrc), true
	case c.has("postscript", "application/postscript", "langps"):
		return "ps2write", "PostScript", true
	}
	return "", "no targetable page-description language advertised", false
}

// pwgDimRe pulls the dimensions out of a PWG media name's trailing segment,
// e.g. "na_legal_8.5x14in" -> 8.5,14,in ; "iso_a4_210x297mm" -> 210,297,mm.
var pwgDimRe = regexp.MustCompile(`(?i)_([0-9.]+)x([0-9.]+)(in|mm)$`)

// parsePWGSize converts a PWG media keyword to points. ok=false for names
// without concrete dimensions (e.g. custom_min/max ranges parse, but bare names
// don't).
func parsePWGSize(name string) (w, h int, ok bool) {
	m := pwgDimRe.FindStringSubmatch(name)
	if m == nil {
		return 0, 0, false
	}
	wf, _ := strconv.ParseFloat(m[1], 64)
	hf, _ := strconv.ParseFloat(m[2], 64)
	scale := 72.0 // inches
	if strings.EqualFold(m[3], "mm") {
		scale = 72.0 / 25.4
	}
	return int(wf*scale + 0.5), int(hf*scale + 0.5), true
}

// MediaLoaded reports whether a page of w×h points matches a size loaded in the
// trays (within ~5pt, orientation-independent). Returns true when the loaded
// media is unknown, so callers never warn on missing information.
func (c *Caps) MediaLoaded(w, h int) bool {
	if len(c.MediaReady) == 0 {
		return true
	}
	near := func(a, b int) bool { d := a - b; return d <= 5 && d >= -5 }
	for _, m := range c.MediaReady {
		if mw, mh, ok := parsePWGSize(m); ok {
			if (near(w, mw) && near(h, mh)) || (near(w, mh) && near(h, mw)) {
				return true
			}
		}
	}
	return false
}

// has reports whether any advertised format/language contains one of needles.
func (c *Caps) has(needles ...string) bool {
	hay := strings.ToLower(strings.Join(c.Formats, " ") + " " + strings.Join(c.Languages, " "))
	for _, n := range needles {
		if strings.Contains(hay, n) {
			return true
		}
	}
	return false
}

// Summary is a one-line human description for logs ("what we detected").
func (c *Caps) Summary() string {
	var parts []string
	if c.Model != "" {
		parts = append(parts, c.Model)
	}
	langs := c.Languages
	if len(langs) == 0 {
		langs = c.Formats
	}
	if len(langs) > 0 {
		parts = append(parts, "languages: "+strings.Join(langs, ", "))
	}
	if c.Color != nil {
		if *c.Color {
			parts = append(parts, "color")
		} else {
			parts = append(parts, "mono")
		}
	}
	if c.Duplex != nil && *c.Duplex {
		parts = append(parts, "duplex")
	}
	if len(c.MediaReady) > 0 {
		parts = append(parts, "loaded: "+strings.Join(c.MediaReady, ", "))
	}
	return fmt.Sprintf("%s (via %s)", strings.Join(parts, "; "), c.Source)
}

// prettySource turns a PWG media-source keyword into a readable label:
// "tray-1" -> "Tray 1", "main" -> "Main". SNMP descriptions ("Tray 1") pass
// through unchanged. An empty source becomes "(tray)".
func prettySource(s string) string {
	if s == "" {
		return "(tray)"
	}
	words := strings.Fields(strings.ReplaceAll(s, "-", " "))
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// dimsToLabel maps an IPP media-size (x,y in hundredths of a millimetre) to a
// friendly size name, or falls back to "WxHmm" when it matches nothing known.
func dimsToLabel(xHmm, yHmm int) string {
	known := []struct {
		name string
		w, h int
	}{
		{"Letter", 21590, 27940}, {"Legal", 21590, 35560},
		{"Tabloid", 27940, 43180}, {"Executive", 18415, 26670},
		{"Statement", 13970, 21590},
		{"A5", 14800, 21000}, {"A4", 21000, 29700}, {"A3", 29700, 42000},
	}
	lo, hi := xHmm, yHmm
	if lo > hi {
		lo, hi = hi, lo
	}
	near := func(a, b int) bool { d := a - b; return d <= 40 && d >= -40 } // ±0.4 mm
	for _, k := range known {
		if near(lo, k.w) && near(hi, k.h) {
			return k.name
		}
	}
	return fmt.Sprintf("%gx%gmm", float64(xHmm)/100, float64(yHmm)/100)
}

// normalizeMediaName maps a free-form SNMP prtInputMediaName ("na-letter",
// "iso A4", "Letter") to the same friendly labels dimsToLabel uses; otherwise
// it returns the trimmed original.
func normalizeMediaName(s string) string {
	s = strings.TrimSpace(s)
	switch l := strings.ToLower(s); {
	case strings.Contains(l, "letter"):
		return "Letter"
	case strings.Contains(l, "legal"):
		return "Legal"
	case strings.Contains(l, "tabloid"), strings.Contains(l, "ledger"):
		return "Tabloid"
	case strings.Contains(l, "executive"):
		return "Executive"
	case strings.Contains(l, "statement"):
		return "Statement"
	case strings.Contains(l, "a4"):
		return "A4"
	case strings.Contains(l, "a3"):
		return "A3"
	case strings.Contains(l, "a5"):
		return "A5"
	}
	return s
}
