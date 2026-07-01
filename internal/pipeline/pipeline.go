// Package pipeline wires the stages together: parse the PPD, build the
// Ghostscript command, run it, and stream its output (the printer's native
// PCL/PS) to the Windows spooler or, for local testing, a file.
package pipeline

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/catprintlabs/pdfprint/internal/gs"
	"github.com/catprintlabs/pdfprint/internal/ppd"
	"github.com/catprintlabs/pdfprint/internal/spool"
)

// Config is the resolved run configuration from the CLI.
type Config struct {
	InputPath  string // PDF path, or "-" for stdin
	PPDPath    string // optional PPD path
	Printer    string // Windows printer name (spooler output)
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
	Verbose bool
	Logf    func(format string, args ...any) // progress sink; defaults to stderr
}

// Run executes the full pipeline.
func Run(cfg Config) error {
	logf := cfg.Logf
	if logf == nil {
		logf = func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	}

	// 0. Resolve the Ghostscript binary (auto-detect on Windows).
	gsBin, found := gs.FindBinary(cfg.GSBinary)
	cfg.GSBinary = gsBin
	if !found && !cfg.DryRun {
		return fmt.Errorf("Ghostscript not found (looked for %q plus standard install dirs); install it or pass --gs <path to gswin64c.exe>", cfg.GSBinary)
	}
	if cfg.Verbose {
		logf("ghostscript: %s", gsBin)
	}

	// 1. Parse the PPD (optional but strongly recommended).
	var p *ppd.PPD
	if cfg.PPDPath != "" {
		var err error
		p, err = ppd.ParseFile(cfg.PPDPath)
		if err != nil {
			return fmt.Errorf("parse PPD: %w", err)
		}
		if cfg.Verbose {
			logf("PPD: %s (%s)", p.NickName, p.DefaultResolution)
		}
	}

	// 2. Build the Ghostscript command.
	cmd, err := gs.Build(p, gs.Options{
		Device:     cfg.Device,
		Resolution: cfg.Resolution,
		PageSize:   cfg.PageSize,
		Duplex:     cfg.Duplex,
		Copies:     cfg.Copies,
		Fit:        cfg.Fit,
		Color:      cfg.Color,
		InputPath:  cfg.InputPath,
		GSBinary:   cfg.GSBinary,
		Extra:      cfg.Extra,
	})
	if err != nil {
		return err
	}
	if cfg.Verbose || cfg.DryRun {
		logf("device: %s", cmd.Device)
		logf("gs command: %s", cmd.String())
	}
	if cfg.DryRun {
		return nil
	}

	// 3. Open the output sink: spooler (RAW) or file.
	out, sink, err := openOutput(cfg)
	if err != nil {
		return err
	}
	logf("output: %s", sink)

	// 4. Run gs, streaming stdout -> sink, stderr -> log.
	if err := runGS(cmd, cfg, out); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("finalize output: %w", err)
	}
	logf("done")
	return nil
}

// openOutput chooses the spooler, a file, or stdout, and returns a description.
func openOutput(cfg Config) (spool.Writer, string, error) {
	if cfg.OutputFile == "-" {
		// Stream to stdout so it can be piped, e.g. into `lp -o raw` on macOS —
		// the local analog of the Windows RAW spooler path.
		return nopCloser{os.Stdout}, "stdout", nil
	}
	if cfg.OutputFile != "" {
		w, err := spool.OpenFile(cfg.OutputFile)
		return w, "file " + cfg.OutputFile, err
	}
	if cfg.Printer == "" {
		return nil, "", fmt.Errorf("no output: pass --printer <name> (Windows) or --output <file>")
	}
	docName := cfg.DocName
	if docName == "" {
		docName = cfg.InputPath
	}
	w, err := spool.Open(spool.Job{Printer: cfg.Printer, DocName: docName, Datatype: "RAW"})
	return w, "printer " + cfg.Printer, err
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
