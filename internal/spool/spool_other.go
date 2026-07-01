//go:build !windows

package spool

import "fmt"

// Open is unavailable off Windows; callers should use OpenFile for local
// development/testing on macOS/Linux. This keeps the package cross-compilable
// so the whole tool builds and tests on a dev Mac.
func Open(job Job) (Writer, error) {
	return nil, fmt.Errorf("spooler output is only available on Windows; use --output <file> to capture the raw stream on this platform")
}

// ListPrinters is Windows-only; off Windows there is no spooler to query.
func ListPrinters() ([]string, error) {
	return nil, fmt.Errorf("--list-printers is only available on Windows")
}
