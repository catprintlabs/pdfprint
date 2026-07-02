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

// ResolvePrinter is Windows-only (it inspects the Windows spooler/registry).
// Off Windows there are no named queues to discover; callers use --host/--output.
func ResolvePrinter(name string) (PrinterRoute, error) {
	return PrinterRoute{}, fmt.Errorf("resolving a printer by name is only available on Windows; use --host <ip> or --output")
}
