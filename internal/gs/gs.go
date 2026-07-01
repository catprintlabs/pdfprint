// Package gs builds a Ghostscript command line from a parsed PPD plus the
// caller's render options, mirroring what foomatic-rip does: pick the output
// device, resolution, page geometry and duplex, then let Ghostscript translate
// the PDF into the printer's native language (PCL / PostScript / raster).
package gs

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/catprintlabs/pdfprint/internal/ppd"
)

// Duplex mode.
type Duplex string

const (
	DuplexNone  Duplex = "none"
	DuplexLong  Duplex = "long"  // long-edge binding (portrait book)
	DuplexShort Duplex = "short" // short-edge binding (calendar)
)

// Options controls how the PDF is rasterized/translated.
type Options struct {
	Device     string // gs -sDEVICE override; if empty, inferred from PPD
	Resolution string // e.g. "600"; if empty, taken from PPD DefaultResolution
	PageSize   string // PPD PageSize keyword (e.g. "Letter"); default from PPD
	Duplex     Duplex // none/long/short
	Copies     int    // number of copies; <=1 means single
	Color      *bool  // nil = follow PPD/device default; else force color/mono
	InputPath  string // path to input PDF, or "-" for stdin
	GSBinary   string // path to gs / gswin64c.exe; default "gs"
	Extra      []string
}

// Command is a fully-resolved Ghostscript invocation.
type Command struct {
	Binary string
	Args   []string // arguments (not including Binary)
	Device string   // the device that was selected
}

// String renders the command for logging / --dry-run.
func (c Command) String() string {
	return c.Binary + " " + strings.Join(c.Args, " ")
}

// deviceFromFoomatic pulls -sDEVICE=<name> out of a Foomatic command line.
var deviceRe = regexp.MustCompile(`-sDEVICE=([A-Za-z0-9_.-]+)`)

// InferDevice picks a Ghostscript output device for a PPD, or "" if it cannot.
// Order: Foomatic command line -> cupsFilter hints -> nickname heuristics.
func InferDevice(p *ppd.PPD) string {
	if m := deviceRe.FindStringSubmatch(p.FoomaticRIPCommandLine); m != nil {
		return m[1]
	}
	// cupsFilter lines look like: "application/vnd.cups-raster 0 rastertopclx"
	joined := strings.ToLower(strings.Join(p.CUPSFilters, " "))
	nick := strings.ToLower(p.NickName + " " + p.ModelName)
	hay := joined + " " + nick
	switch {
	case strings.Contains(hay, "postscript"), strings.Contains(joined, "pdftops"):
		return "ps2write"
	case strings.Contains(hay, "pcl-xl"), strings.Contains(hay, "pclxl"), strings.Contains(hay, "pcl6"), strings.Contains(hay, "pclm"):
		if p.ColorDevice {
			return "pxlcolor"
		}
		return "pxlmono"
	case strings.Contains(hay, "pcl"):
		return "ljet4"
	}
	return ""
}

// Build resolves Options + PPD into a concrete Ghostscript command.
func Build(p *ppd.PPD, o Options) (Command, error) {
	bin := o.GSBinary
	if bin == "" {
		bin = "gs"
	}

	device := o.Device
	if device == "" && p != nil {
		device = InferDevice(p)
	}
	if device == "" {
		return Command{}, fmt.Errorf("could not determine Ghostscript device from PPD; pass --device (e.g. pxlcolor, pxlmono, ljet4, ps2write)")
	}

	res := o.Resolution
	if res == "" && p != nil {
		res = strings.TrimSuffix(strings.ToLower(p.DefaultResolution), "dpi")
		res = strings.TrimSpace(res)
	}

	args := []string{
		"-q",               // quiet: keep stdout clean for the raw stream
		"-dBATCH",          // exit after processing
		"-dNOPAUSE",        // no per-page pause
		"-dSAFER",          // restricted file access (default in modern gs)
		"-dNOINTERPOLATE",  // match device pixels; sharper text on lasers
		"-sstdout=%stderr", // route gs messages to stderr, never stdout
		"-sOutputFile=%stdout",
		"-sDEVICE=" + device,
	}
	if res != "" {
		args = append(args, "-r"+res)
	}

	// Color / mono coercion where the device supports it.
	if o.Color != nil {
		if *o.Color {
			// pxlmono has no color; caller should choose pxlcolor via --device.
			if device == "pxlmono" {
				return Command{}, fmt.Errorf("device %q is monochrome; use --device pxlcolor for color", device)
			}
		} else {
			// Force grayscale rendering regardless of device.
			args = append(args,
				"-dProcessColorModel=/DeviceGray",
				"-sColorConversionStrategy=Gray",
			)
		}
	}

	// Page geometry via a setpagedevice snippet built from the PPD choice.
	if pd := pageDeviceSnippet(p, o); pd != "" {
		args = append(args, "-c", pd, "-f")
	}

	args = append(args, o.Extra...)

	input := o.InputPath
	if input == "" {
		input = "-"
	}
	if input == "-" {
		args = append(args, "-_") // read PDF/PS from stdin
	} else {
		args = append(args, input)
	}

	return Command{Binary: bin, Args: args, Device: device}, nil
}

// pageDeviceSnippet builds the PostScript prologue that sets page size, duplex
// and copy count. Page size prefers the PPD PageSize choice's own embedded code
// (so we honor the exact media geometry the vendor defined); duplex and copies
// are appended as a second setpagedevice call. Result is a single PostScript
// string suitable for `gs -c <snippet> -f <input>`.
func pageDeviceSnippet(p *ppd.PPD, o Options) string {
	var snippets []string

	// Page size: use the PPD PageSize choice's embedded setpagedevice fragment.
	if p != nil {
		if opt := p.Option("PageSize"); opt != nil {
			key := o.PageSize
			if key == "" {
				key = opt.Default
			}
			if ch := opt.Choice(key); ch != nil && strings.Contains(ch.Code, "PageSize") {
				snippets = append(snippets, strings.TrimSpace(ch.Code))
			}
		}
	}

	// Duplex + copies via standard PostScript keys.
	var kv []string
	switch o.Duplex {
	case DuplexLong:
		kv = append(kv, "/Duplex true /Tumble false")
	case DuplexShort:
		kv = append(kv, "/Duplex true /Tumble true")
	case DuplexNone:
		kv = append(kv, "/Duplex false")
	}
	if o.Copies > 1 {
		kv = append(kv, fmt.Sprintf("/NumCopies %d", o.Copies))
	}
	if len(kv) > 0 {
		snippets = append(snippets, "<< "+strings.Join(kv, " ")+" >> setpagedevice")
	}

	return strings.Join(snippets, " ")
}
