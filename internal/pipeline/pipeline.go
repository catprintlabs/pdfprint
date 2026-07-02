// Package pipeline wires the stages together: parse the PPD, build the
// Ghostscript command, run it, and stream its output (the printer's native
// PCL/PS) to the Windows spooler or, for local testing, a file.
package pipeline

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/catprintlabs/pdfprint/internal/gs"
	"github.com/catprintlabs/pdfprint/internal/ppd"
	"github.com/catprintlabs/pdfprint/internal/probe"
	"github.com/catprintlabs/pdfprint/internal/spool"
)

// Config is the resolved run configuration from the CLI.
type Config struct {
	InputPath  string // PDF path, or "-" for stdin
	PPDPath    string // optional PPD path
	Printer    string // Windows printer name (spooler output)
	Host       string // direct raw-TCP target (AppSocket/JetDirect): host or host:port
	Port       int    // TCP port for Host (default 9100)
	Transport  string // output transport: auto | socket | spooler
	OutputFile string // write raw stream here instead of spooler
	DocName    string // spooler document title

	Device     string
	Resolution string
	PageSize   string
	Duplex     gs.Duplex
	Copies     int
	Fit        bool // false = 1:1 no scaling (default)
	Color      *bool

	GSBinary string
	Extra    []string

	DryRun  bool
	Verbose bool                             // extra detail (gs command, PPD, probe)
	Quiet   bool                             // suppress normal progress; errors only
	Logf    func(format string, args ...any) // progress sink; defaults to stderr
}

// Run executes the full pipeline.
func Run(cfg Config) error {
	base := cfg.Logf
	if base == nil {
		base = func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	}
	quiet := cfg.Quiet
	verbose := cfg.Verbose && !quiet
	info := func(f string, a ...any) { // normal progress
		if !quiet {
			base(f, a...)
		}
	}
	dbg := func(f string, a ...any) { // verbose-only detail
		if verbose {
			base(f, a...)
		}
	}
	warn := func(f string, a ...any) { base("WARNING: "+f, a...) } // advisory; shown even under --quiet

	// Expand a partial --printer to the unique installed printer it identifies.
	if cfg.Printer != "" {
		full, err := spool.MatchPrinter(cfg.Printer)
		if err != nil {
			return err
		}
		if full != cfg.Printer {
			info("printer: %q -> %q", cfg.Printer, full)
		}
		cfg.Printer = full
	}

	// 0. Resolve the Ghostscript binary (auto-detect on Windows).
	gsBin, found := gs.FindBinary(cfg.GSBinary)
	cfg.GSBinary = gsBin
	if !found && !cfg.DryRun {
		return fmt.Errorf("Ghostscript not found (looked for %q plus standard install dirs); install it or pass --gs <path to gswin64c.exe>", cfg.GSBinary)
	}
	dbg("ghostscript: %s", gsBin)

	// 1. Parse the PPD (optional but strongly recommended).
	var p *ppd.PPD
	if cfg.PPDPath != "" {
		var err error
		p, err = ppd.ParseFile(cfg.PPDPath)
		if err != nil {
			return fmt.Errorf("parse PPD: %w", err)
		}
		dbg("PPD: %s (%s)", p.NickName, p.DefaultResolution)
	}

	// 2. Resolve the output transport first (side-effect-free) — it yields the
	//    host used for capability probing below.
	plan, planErr := planOutput(cfg)
	host := networkHost(plan, planErr)

	// 3. Probe the printer once (best-effort) for a network target: the
	//    capabilities drive device selection and the loaded-paper check.
	needDevice := cfg.Device == "" && (p == nil || gs.InferDevice(p) == "")
	var caps *probe.Caps
	if host != "" {
		if needDevice {
			info("probing %s for capabilities...", host)
		}
		if c, perr := probe.Probe(host, 4*time.Second); perr == nil {
			caps = c
			if needDevice {
				info("detected: %s", caps.Summary())
			}
		}
	}

	// 4. Resolve the output device: explicit --device -> PPD -> probe -> refuse.
	device, reason, devErr := resolveDevice(cfg, p, caps, host, plan, planErr)

	// Effective media size: an explicit --page-size (or PPD), else the PDF's own
	// size. We must set it — otherwise gs falls back to its Letter default for the
	// device and the printer waits for the wrong paper (even for a Legal PDF).
	pageSize, pageFromPDF := cfg.PageSize, false
	if pageSize == "" && cfg.InputPath != "-" {
		if w, h, ok := readPDFSize(cfg.InputPath); ok {
			pageSize = fmt.Sprintf("%dx%d", w, h)
			pageFromPDF = true
		}
	}

	// 5. Build the Ghostscript command (only when we actually have a device).
	var cmd gs.Command
	if devErr == nil {
		cmd, devErr = gs.Build(p, gs.Options{
			Device:     device,
			Resolution: cfg.Resolution,
			PageSize:   pageSize,
			Duplex:     cfg.Duplex,
			Copies:     cfg.Copies,
			Fit:        cfg.Fit,
			Color:      cfg.Color,
			InputPath:  cfg.InputPath,
			GSBinary:   cfg.GSBinary,
			Extra:      cfg.Extra,
		})
	}
	if devErr == nil && reason != "" {
		info("device: %s (%s)", cmd.Device, reason)
	}
	// Report the resolved size, and warn (non-fatally) if it isn't the loaded
	// paper — an operator has to go load it; we don't refuse the job.
	if devErr == nil && cmd.MediaW > 0 {
		src := ""
		if pageFromPDF {
			src = ", from PDF"
		}
		info("page size: %s%s", mediaLabel(cmd.MediaW, cmd.MediaH), src)
		if caps != nil && !caps.MediaLoaded(cmd.MediaW, cmd.MediaH) {
			warn("printer has %s loaded, but this job is %s — load matching paper or the page may clip",
				strings.Join(caps.MediaReady, ", "), mediaLabel(cmd.MediaW, cmd.MediaH))
		}
	}

	if cfg.DryRun {
		if devErr != nil {
			info("device: (unresolved) %v", devErr)
		} else {
			info("gs command: %s", cmd.String())
		}
		if planErr != nil {
			info("output: (unresolved) %v", planErr)
		} else {
			info("output: %s", plan.desc)
		}
		return nil
	}
	if devErr != nil {
		return devErr
	}
	if planErr != nil {
		return planErr
	}
	dbg("gs command: %s", cmd.String())

	// 5. Open the resolved output sink.
	out, err := openPlan(plan, cfg)
	if err != nil {
		return err
	}
	info("output: %s", plan.desc)

	// 6. Run gs, streaming stdout -> sink, stderr -> log.
	if err := runGS(cmd, cfg, out); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("finalize output: %w", err)
	}
	info("done")
	return nil
}

// resolveDevice picks the Ghostscript device without guessing. Order: explicit
// --device; then the PPD's device; then the probed capabilities (caps). If none
// of those yield a device it returns an error — callers must not fall back to an
// assumed device.
func resolveDevice(cfg Config, p *ppd.PPD, caps *probe.Caps, host string, plan outPlan, planErr error) (device, reason string, err error) {
	if cfg.Device != "" {
		return cfg.Device, "", nil // explicit: no need to explain
	}
	if p != nil {
		if d := gs.InferDevice(p); d != "" {
			return d, "from PPD", nil
		}
	}
	if host == "" {
		where := "no network printer to query"
		if planErr == nil {
			where = plan.desc
		}
		return "", "", fmt.Errorf("no --device and cannot auto-detect (%s); pass --device pxlcolor|pxlmono|ljet4|ps2write", where)
	}
	if caps == nil {
		return "", "", fmt.Errorf("could not detect %s capabilities (IPP/SNMP); pass --device pxlcolor|pxlmono|ljet4|ps2write", host)
	}
	dev, why, ok := caps.SuggestDevice(cfg.Color)
	if !ok {
		return "", "", fmt.Errorf("printer advertised no page-description language we can target (%s); pass --device", caps.Summary())
	}
	return dev, why, nil
}

// networkHost returns the printer's IP for a socket transport, else "".
func networkHost(plan outPlan, planErr error) string {
	if planErr != nil || plan.kind != "socket" {
		return ""
	}
	if h, _, err := net.SplitHostPort(plan.target); err == nil {
		return h
	}
	return ""
}

// mediaLabel formats a media size as "8.5x14 in (612x1008 pt)".
func mediaLabel(w, h int) string {
	return fmt.Sprintf("%sx%s in (%dx%d pt)", inches(w), inches(h), w, h)
}

func inches(pt int) string {
	s := strconv.FormatFloat(float64(pt)/72, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}

// mediaBoxRe matches an uncompressed /MediaBox [x1 y1 x2 y2] entry.
var mediaBoxRe = regexp.MustCompile(`/MediaBox\s*\[\s*(-?[\d.]+)\s+(-?[\d.]+)\s+(-?[\d.]+)\s+(-?[\d.]+)\s*\]`)

// readPDFSize returns the first page's size in points from the PDF's MediaBox,
// best-effort. ok=false if none is found in cleartext (e.g. the page tree is in
// a compressed object stream).
func readPDFSize(path string) (w, h int, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	return parseMediaBox(data)
}

// parseMediaBox extracts a page size (points) from the first /MediaBox in data.
func parseMediaBox(data []byte) (w, h int, ok bool) {
	m := mediaBoxRe.FindSubmatch(data)
	if m == nil {
		return 0, 0, false
	}
	x1, _ := strconv.ParseFloat(string(m[1]), 64)
	y1, _ := strconv.ParseFloat(string(m[2]), 64)
	x2, _ := strconv.ParseFloat(string(m[3]), 64)
	y2, _ := strconv.ParseFloat(string(m[4]), 64)
	wf, hf := x2-x1, y2-y1
	if wf < 0 {
		wf = -wf
	}
	if hf < 0 {
		hf = -hf
	}
	w, h = int(wf+0.5), int(hf+0.5)
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// outPlan is a resolved output decision, computed without opening anything.
type outPlan struct {
	kind   string // "stdout" | "file" | "socket" | "spooler"
	target string // file path, dial address, or printer name
	desc   string // human description for logs / --dry-run
}

// planOutput chooses the transport — file, stdout, a direct raw-TCP socket, or
// the Windows spooler — without side effects (it may query the spooler/registry
// to detect capability, but opens no file, socket, or spooler handle).
func planOutput(cfg Config) (outPlan, error) {
	// File / stdout sinks take precedence (local capture and piping).
	if cfg.OutputFile == "-" {
		// Stream to stdout so it can be piped, e.g. into `lp -o raw` on macOS —
		// the local analog of the Windows RAW spooler path.
		return outPlan{kind: "stdout", desc: "stdout"}, nil
	}
	if cfg.OutputFile != "" {
		return outPlan{kind: "file", target: cfg.OutputFile, desc: "file " + cfg.OutputFile}, nil
	}

	// Explicit direct raw-TCP target: dial the device, no OS-side setup.
	if cfg.Host != "" {
		addr := spool.HostPort(cfg.Host, cfg.Port)
		return outPlan{kind: "socket", target: addr, desc: "socket " + addr}, nil
	}

	if cfg.Printer == "" {
		return outPlan{}, fmt.Errorf("no output: pass --printer <name>, --host <ip> (raw TCP), or --output <file>")
	}

	// Named printer: choose the transport.
	switch cfg.Transport {
	case "spooler":
		return outPlan{kind: "spooler", target: cfg.Printer, desc: fmt.Sprintf("printer %q (spooler RAW)", cfg.Printer)}, nil

	case "socket":
		// Force raw TCP: discover the device IP from the named queue.
		route, err := spool.ResolvePrinter(cfg.Printer)
		if err != nil {
			return outPlan{}, err
		}
		if route.Kind != "socket" {
			return outPlan{}, fmt.Errorf("--transport socket: %q is not a network printer (%s); use --host <ip> or --transport spooler", cfg.Printer, route.Why)
		}
		return outPlan{kind: "socket", target: route.Addr, desc: fmt.Sprintf("printer %q via socket %s (%s)", cfg.Printer, route.Addr, route.Why)}, nil

	case "auto", "":
		// Detect capability: network queues (WSD/TCP-IP) that can't carry RAW get
		// routed to a direct socket; local/USB queues use the spooler.
		route, err := spool.ResolvePrinter(cfg.Printer)
		if err != nil {
			return outPlan{}, err
		}
		if route.Kind == "socket" {
			return outPlan{kind: "socket", target: route.Addr, desc: fmt.Sprintf("printer %q via socket %s (%s)", cfg.Printer, route.Addr, route.Why)}, nil
		}
		return outPlan{kind: "spooler", target: cfg.Printer, desc: fmt.Sprintf("printer %q (spooler RAW: %s)", cfg.Printer, route.Why)}, nil

	default:
		return outPlan{}, fmt.Errorf("invalid --transport %q (want auto|socket|spooler)", cfg.Transport)
	}
}

// openPlan opens the sink chosen by planOutput.
func openPlan(p outPlan, cfg Config) (spool.Writer, error) {
	switch p.kind {
	case "stdout":
		return nopCloser{os.Stdout}, nil
	case "file":
		return spool.OpenFile(p.target)
	case "socket":
		return spool.OpenSocket(p.target)
	case "spooler":
		docName := cfg.DocName
		if docName == "" {
			docName = cfg.InputPath
		}
		return spool.Open(spool.Job{Printer: p.target, DocName: docName, Datatype: "RAW"})
	default:
		return nil, fmt.Errorf("internal: unknown output plan %q", p.kind)
	}
}

// nopCloser adapts an io.Writer (os.Stdout) to spool.Writer without closing it.
type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// runGS executes the Ghostscript command, piping stdin (if input is "-"),
// stdout to the sink, and stderr to the log.
func runGS(cmd gs.Command, cfg Config, out io.Writer) error {
	c := exec.Command(cmd.Binary, cmd.Args...)
	c.Stdout = out
	c.Stderr = os.Stderr
	if cfg.InputPath == "-" {
		c.Stdin = os.Stdin
	}
	if err := c.Run(); err != nil {
		return fmt.Errorf("ghostscript failed (%s): %w", cmd.Binary, err)
	}
	return nil
}
