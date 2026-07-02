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
