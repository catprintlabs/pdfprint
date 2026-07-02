// Package gs builds a Ghostscript command line from a parsed PPD plus the
// caller's render options, mirroring what foomatic-rip does: pick the output
// device, resolution, page geometry and duplex, then let Ghostscript translate
// the PDF into the printer's native language (PCL / PostScript / raster).
package gs

import (
	"fmt"
	"regexp"
	"strconv"
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
	Fit        bool   // false (default) = print 1:1, NO scaling; true = scale to page
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
	MediaW int      // resolved media width in points (0 = using the PDF's own size)
	MediaH int      // resolved media height in points
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
		// No device given, none in a PPD, and the caller (pipeline) couldn't
		// detect one from the printer. We do NOT guess — a wrong PDL prints
		// garbage or nothing. The caller should have produced a clearer message.
		return Command{}, fmt.Errorf("no output device: pass --device (pxlcolor, pxlmono, ljet4, ps2write) or use a printer/PPD that advertises one")
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

	// Page geometry. Resolve the target media size in points (from the PPD's
	// PaperDimension / PageSize, or a built-in table for a bare --device run).
	// We set it via -dDEVICEWIDTHPOINTS/-dDEVICEHEIGHTPOINTS at device init and
	// lock it with -dFIXEDMEDIA. This is the only reliable way to force exact
	// media: a `-c setpagedevice` after -dFIXEDMEDIA is ignored, and without
	// fixing the size gs would default to Letter.
	var mediaW, mediaH int
	if w, h, ok := resolvePageDims(p, o.PageSize); ok {
		args = append(args,
			fmt.Sprintf("-dDEVICEWIDTHPOINTS=%d", w),
			fmt.Sprintf("-dDEVICEHEIGHTPOINTS=%d", h),
			"-dFIXEDMEDIA",
		)
		mediaW, mediaH = w, h
	}
	// Scaling policy. Default (Fit=false) is strict 1:1, no scaling — oversized
	// content clips rather than scales. With Fit, gs scales pages to the media.
	if o.Fit {
		args = append(args, "-dPDFFitPage")
	} else {
		args = append(args, "-dPDFFitPage=false")
	}
	// Duplex / copies go through setpagedevice (not media, so safe after init).
	if snippet := optionsSnippet(o); snippet != "" {
		args = append(args, "-c", snippet, "-f")
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

	return Command{Binary: bin, Args: args, Device: device, MediaW: mediaW, MediaH: mediaH}, nil
}

// knownSizes maps common media keywords to their PostScript point dimensions,
// so --page-size works even without a PPD (a bare --device run against a real
// PCL printer). Points = 1/72 inch. Legal (8.5x14") = 612 x 1008.
var knownSizes = map[string][2]int{
	"letter":    {612, 792},
	"legal":     {612, 1008},
	"a4":        {595, 842},
	"a3":        {842, 1191},
	"tabloid":   {792, 1224},
	"ledger":    {1224, 792},
	"executive": {522, 756},
	"statement": {396, 612},
}

// dimsRe pulls the first two numbers out of a PageSize choice's PostScript code,
// e.g. "<</PageSize[612 1008]...>>" -> 612, 1008.
var dimsRe = regexp.MustCompile(`\[\s*([0-9.]+)\s+([0-9.]+)`)

// customSizeRe matches an explicit "WxH" media size with an optional unit,
// e.g. "612x792", "8.5x11in", "216x279mm". Default unit is points.
var customSizeRe = regexp.MustCompile(`(?i)^\s*([0-9]*\.?[0-9]+)\s*[x×]\s*([0-9]*\.?[0-9]+)\s*(pt|in|mm|cm)?\s*$`)

// parseCustomSize parses an explicit media size into points. ok=false if the
// string isn't a WxH size (so a keyword like "Legal" falls through to the table).
func parseCustomSize(s string) (w, h int, ok bool) {
	m := customSizeRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	wf, _ := strconv.ParseFloat(m[1], 64)
	hf, _ := strconv.ParseFloat(m[2], 64)
	scale := 1.0 // points
	switch strings.ToLower(m[3]) {
	case "in":
		scale = 72
	case "mm":
		scale = 72.0 / 25.4
	case "cm":
		scale = 72.0 / 2.54
	}
	w = int(wf*scale + 0.5)
	h = int(hf*scale + 0.5)
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// resolvePageDims resolves the target media size in points. It prefers, in
// order: an explicit "WxH" size, the PPD's *PaperDimension for the keyword, the
// numbers embedded in the PPD PageSize choice code, then the built-in size table.
// When key is empty it uses the PPD's default PageSize. Returns ok=false if
// nothing matches (in which case gs falls back to the PDF's own MediaBox — still
// no scaling).
func resolvePageDims(p *ppd.PPD, key string) (w, h int, ok bool) {
	// An explicit "WxH[unit]" size wins over everything.
	if w, h, ok := parseCustomSize(key); ok {
		return w, h, true
	}
	if p != nil {
		if key == "" {
			if opt := p.Option("PageSize"); opt != nil {
				key = opt.Default
			}
		}
		if key != "" {
			if wh, found := p.PaperDimension(key); found {
				if w, h, ok := parseTwoNums(wh); ok {
					return w, h, true
				}
			}
			if opt := p.Option("PageSize"); opt != nil {
				if ch := opt.Choice(key); ch != nil {
					if m := dimsRe.FindStringSubmatch(ch.Code); m != nil {
						if w, h, ok := parseTwoNums(m[1] + " " + m[2]); ok {
							return w, h, true
						}
					}
				}
			}
		}
	}
	if d, found := knownSizes[strings.ToLower(key)]; found {
		return d[0], d[1], true
	}
	return 0, 0, false
}

// optionsSnippet builds the setpagedevice prologue for duplex and copies (not
// media — those are set at device init). Returns "" when nothing to set.
func optionsSnippet(o Options) string {
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
	if len(kv) == 0 {
		return ""
	}
	return "<< " + strings.Join(kv, " ") + " >> setpagedevice"
}

// parseTwoNums parses "W H" (possibly fractional) into rounded integer points.
func parseTwoNums(s string) (int, int, bool) {
	f := strings.Fields(s)
	if len(f) < 2 {
		return 0, 0, false
	}
	w, err1 := strconv.ParseFloat(f[0], 64)
	h, err2 := strconv.ParseFloat(f[1], 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return int(w + 0.5), int(h + 0.5), true
}
