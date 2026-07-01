// Package spool sends a raw byte stream to a printer.
//
// On Windows it writes to the print spooler with the RAW datatype, which hands
// the bytes straight to the device and bypasses any host print driver — exactly
// what we want, since Ghostscript has already produced the printer's native
// language (PCL/PS). On other platforms Open returns an error and callers should
// use OpenFile instead (development/testing on macOS/Linux).
package spool

import (
	"io"
	"os"
)

// Job describes a print job to open.
type Job struct {
	Printer  string // Windows printer name as shown in "Printers & scanners"
	DocName  string // document title in the spooler UI
	Datatype string // spooler datatype; defaults to "RAW"
}

// Writer is a write-closer that flushes the job on Close.
type Writer interface {
	io.WriteCloser
}

// fileWriter adapts *os.File to Writer for the file-output path.
type fileWriter struct{ *os.File }

// OpenFile routes the raw stream to a file instead of the spooler. This is the
// default on non-Windows and available everywhere via --output for inspection.
func OpenFile(path string) (Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return fileWriter{f}, nil
}
