// Package stamp overlays a small text label (timestamp, host, the print command,
// arbitrary notes) onto every page of a PDF, using Ghostscript. It's meant for
// smoke tests: the printed page then documents exactly what command produced it
// and when — which also disambiguates otherwise-identical test pages.
//
// The overlay is drawn in an EndPage procedure so it composits on top of each
// page's existing content, and the input page geometry is preserved (no scaling)
// unless an explicit Width/Height is given.
package stamp

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/catprintlabs/pdfprint/internal/gs"
)

// Options configures a stamping run.
type Options struct {
	Input    string   // input PDF path
	Output   string   // output PDF path
	GSBinary string   // Ghostscript binary; auto-detected when empty
	Title    string   // bold header line (e.g. "pdfprint smoke test")
	Lines    []string // body lines drawn under the title
	Width    int      // page width in points; 0 = preserve input
	Height   int      // page height in points; 0 = preserve input
}

// Apply writes Input to Output with the stamp overlaid on every page.
func Apply(opt Options) error {
	if opt.Input == "" || opt.Output == "" {
		return fmt.Errorf("stamp: Input and Output are required")
	}
	gsBin, found := gs.FindBinary(opt.GSBinary)
	if !found {
		return fmt.Errorf("stamp: Ghostscript not found (looked for %q plus standard install dirs); install it or set GSBinary", opt.GSBinary)
	}

	prolog := BuildProlog(opt.Title, opt.Lines)
	f, err := os.CreateTemp("", "pdfprint-stamp-*.ps")
	if err != nil {
		return fmt.Errorf("stamp: temp prolog: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(prolog); err != nil {
		f.Close()
		return fmt.Errorf("stamp: write prolog: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("stamp: close prolog: %w", err)
	}

	args := []string{"-q", "-dBATCH", "-dNOPAUSE", "-dSAFER", "-sDEVICE=pdfwrite"}
	if opt.Width > 0 && opt.Height > 0 {
		args = append(args,
			fmt.Sprintf("-dDEVICEWIDTHPOINTS=%d", opt.Width),
			fmt.Sprintf("-dDEVICEHEIGHTPOINTS=%d", opt.Height),
			"-dFIXEDMEDIA")
	}
	// Prolog first (installs EndPage), then the input PDF whose pages trigger it.
	args = append(args, "-o", opt.Output, f.Name(), "-f", opt.Input)

	cmd := exec.Command(gsBin, args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("stamp: ghostscript failed: %w", err)
	}
	return nil
}

// BuildProlog returns the PostScript that installs an EndPage overlay drawing the
// title (bold) and lines (monospace) in the top-left of every page. Exposed for
// testing.
func BuildProlog(title string, lines []string) string {
	var b strings.Builder
	// EndPage operands are (count reason) with reason on top; draw on real pages
	// (reason 0 showpage / 1 copypage) and return that boolean to transmit them.
	b.WriteString("<< /EndPage {\n")
	b.WriteString("  exch pop 2 ne dup {\n")
	b.WriteString("    gsave 0 setgray\n")

	const x = 40
	y := 966
	if title != "" {
		b.WriteString("    /Courier-Bold findfont 9 scalefont setfont\n")
		fmt.Fprintf(&b, "    %d %d moveto (%s) show\n", x, y, escapePS(title))
		y -= 13
	}
	b.WriteString("    /Courier findfont 8 scalefont setfont\n")
	for _, ln := range lines {
		fmt.Fprintf(&b, "    %d %d moveto (%s) show\n", x, y, escapePS(ln))
		y -= 11
	}

	b.WriteString("    grestore\n")
	b.WriteString("  } if\n")
	b.WriteString("} bind >> setpagedevice\n")
	return b.String()
}

// escapePS escapes a string for a PostScript literal ( ... ) — backslash and
// parentheses must be escaped; non-printable/non-ASCII bytes are dropped so the
// prolog stays valid ASCII PostScript.
func escapePS(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '(':
			b.WriteString(`\(`)
		case ')':
			b.WriteString(`\)`)
		default:
			if r >= 0x20 && r < 0x7f {
				b.WriteRune(r)
			} else {
				b.WriteByte(' ')
			}
		}
	}
	return b.String()
}
