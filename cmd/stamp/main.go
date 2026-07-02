// Command stamp overlays a timestamp, host, print command, and arbitrary notes
// onto every page of a PDF (via Ghostscript), producing a self-documenting test
// page for smoke tests. Feed its output straight into pdfprint:
//
//	stamp --cmd "pdfprint --device pxlmono --printer P legal_ruler.pdf" \
//	      -o job.pdf testdata/legal_ruler.pdf
//	pdfprint --device pxlmono --page-size Legal --printer P job.pdf
//
// The stamp records what produced the page and when, and disambiguates otherwise
// identical test pages (e.g. the PS vs PCL paths of the same fixture).
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/catprintlabs/pdfprint/internal/stamp"
)

// multiFlag collects repeated --line values.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, "; ") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func main() {
	var extra multiFlag
	var (
		out      = flag.String("o", "", "output PDF path (required)")
		title    = flag.String("title", "pdfprint smoke test", "bold header line")
		cmdStr   = flag.String("cmd", "", "print command to record on the page")
		host     = flag.String("host", "", "host label (default: OS hostname)")
		noTime   = flag.Bool("no-time", false, "omit the timestamp line")
		gsBinary = flag.String("gs", "", "path to the Ghostscript binary (auto-detected if empty)")
		width    = flag.Int("width", 0, "force page width in points (default: preserve input)")
		height   = flag.Int("height", 0, "force page height in points (default: preserve input)")
	)
	flag.Var(&extra, "line", "extra note line to add (repeatable)")
	flag.Usage = usage
	flag.Parse()

	input := flag.Arg(0)
	if input == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "error: need an input PDF and -o <output>")
		usage()
		os.Exit(2)
	}

	lines := buildLines(*cmdStr, *host, *noTime, extra)

	if err := stamp.Apply(stamp.Options{
		Input:    input,
		Output:   *out,
		GSBinary: *gsBinary,
		Title:    *title,
		Lines:    lines,
		Width:    *width,
		Height:   *height,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// buildLines assembles the stamp body: timestamp+host, the command, then notes.
func buildLines(cmdStr, host string, noTime bool, extra []string) []string {
	var lines []string
	if !noTime {
		if host == "" {
			host, _ = os.Hostname()
		}
		lines = append(lines, fmt.Sprintf("when: %s  host: %s", time.Now().UTC().Format(time.RFC3339), host))
	}
	if cmdStr != "" {
		lines = append(lines, "cmd:  "+cmdStr)
	}
	lines = append(lines, extra...)
	return lines
}

func usage() {
	fmt.Fprintf(os.Stderr, "stamp — overlay a smoke-test label (time, host, command) onto a PDF\n\n")
	fmt.Fprintf(os.Stderr, "usage: stamp [flags] <input.pdf> -o <output.pdf>\n\n")
	flag.PrintDefaults()
}
