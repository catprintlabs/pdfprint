// Command pdfprint prints a PDF to a Windows printer by translating it to the
// printer's native language (PCL/PostScript) with Ghostscript, driven by a PPD
// — a Windows port of the CUPS/foomatic-rip approach.
//
// Examples:
//
//	# Print to a Windows printer, device inferred from the PPD:
//	pdfprint --ppd hp.ppd --printer "HP LaserJet" job.pdf
//
//	# Force a device and resolution, two copies, duplex:
//	pdfprint --device pxlcolor --resolution 600 --copies 2 --duplex long \
//	         --printer "HP LaserJet" job.pdf
//
//	# Local test on macOS/Linux: capture the raw PCL/PS to a file:
//	pdfprint --ppd hp.ppd --device pxlcolor --output out.pcl job.pdf
//
//	# See the exact Ghostscript command without running it:
//	pdfprint --ppd hp.ppd --dry-run job.pdf
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/catprintlabs/pdfprint/internal/gs"
	"github.com/catprintlabs/pdfprint/internal/pipeline"
	"github.com/catprintlabs/pdfprint/internal/spool"
)

func main() {
	var (
		ppdPath    = flag.String("ppd", "", "path to the printer's PPD file")
		printer    = flag.String("printer", "", "Windows printer name (RAW spooler output)")
		output     = flag.String("output", "", "write the raw PCL/PS stream to this file instead of a printer")
		docName    = flag.String("doc-name", "", "document title shown in the spooler (default: input path)")
		device     = flag.String("device", "", "Ghostscript output device override (e.g. pxlcolor, pxlmono, ljet4, ps2write)")
		resolution = flag.String("resolution", "", "output resolution in dpi (default: PPD DefaultResolution)")
		pageSize   = flag.String("page-size", "", "PPD PageSize keyword (default: PPD default)")
		duplexFlag = flag.String("duplex", "none", "duplex mode: none | long | short")
		copies     = flag.Int("copies", 1, "number of copies")
		scaleFlag  = flag.String("scale", "none", "scaling: none (1:1, no scaling) | fit (scale to page)")
		colorFlag  = flag.String("color", "auto", "color mode: auto | color | mono")
		gsBinary   = flag.String("gs", "", "path to the Ghostscript binary (auto-detected if empty)")
		listPrn    = flag.Bool("list-printers", false, "list installed Windows printers and exit")
		dryRun     = flag.Bool("dry-run", false, "print the resolved Ghostscript command and exit")
		verbose    = flag.Bool("v", false, "verbose logging")
	)
	flag.Usage = usage
	flag.Parse()

	if *listPrn {
		listPrinters()
		return
	}

	input := flag.Arg(0)
	if input == "" {
		fmt.Fprintln(os.Stderr, "error: no input PDF (pass a path or '-' for stdin)")
		usage()
		os.Exit(2)
	}

	duplex, err := parseDuplex(*duplexFlag)
	if err != nil {
		fatal(err)
	}
	color, err := parseColor(*colorFlag)
	if err != nil {
		fatal(err)
	}
	fit, err := parseScale(*scaleFlag)
	if err != nil {
		fatal(err)
	}

	cfg := pipeline.Config{
		InputPath:  input,
		PPDPath:    *ppdPath,
		Printer:    *printer,
		OutputFile: *output,
		DocName:    *docName,
		Device:     *device,
		Resolution: *resolution,
		PageSize:   *pageSize,
		Duplex:     duplex,
		Copies:     *copies,
		Fit:        fit,
		Color:      color,
		GSBinary:   *gsBinary,
		DryRun:     *dryRun,
		Verbose:    *verbose,
	}
	if err := pipeline.Run(cfg); err != nil {
		fatal(err)
	}
}

func parseDuplex(s string) (gs.Duplex, error) {
	switch s {
	case "none", "":
		return gs.DuplexNone, nil
	case "long":
		return gs.DuplexLong, nil
	case "short":
		return gs.DuplexShort, nil
	default:
		return "", fmt.Errorf("invalid --duplex %q (want none|long|short)", s)
	}
}

func parseColor(s string) (*bool, error) {
	switch s {
	case "auto", "":
		return nil, nil
	case "color":
		v := true
		return &v, nil
	case "mono", "gray", "grey":
		v := false
		return &v, nil
	default:
		return nil, fmt.Errorf("invalid --color %q (want auto|color|mono)", s)
	}
}

func parseScale(s string) (bool, error) {
	switch s {
	case "none", "", "1:1":
		return false, nil // no scaling
	case "fit":
		return true, nil
	default:
		return false, fmt.Errorf("invalid --scale %q (want none|fit)", s)
	}
}

// listPrinters prints installed printer names (Windows only).
func listPrinters() {
	names, err := spool.ListPrinters()
	if err != nil {
		fatal(err)
	}
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "no printers found")
		return
	}
	for _, n := range names {
		fmt.Println(n)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "pdfprint — print a PDF via Ghostscript+PPD (PCL/PS) to a Windows printer\n\n")
	fmt.Fprintf(os.Stderr, "usage: pdfprint [flags] <input.pdf|->\n\n")
	flag.PrintDefaults()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
