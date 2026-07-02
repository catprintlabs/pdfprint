// Package spool sends a raw byte stream to a printer.
//
// On Windows it writes to the print spooler with the RAW datatype, which hands
// the bytes straight to the device and bypasses any host print driver — exactly
// what we want, since Ghostscript has already produced the printer's native
// language (PCL/PS). On other platforms Open returns an error and callers should
// use OpenFile instead (development/testing on macOS/Linux).
package spool

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// Job describes a print job to open.
type Job struct {
	Printer  string // Windows printer name as shown in "Printers & scanners"
	DocName  string // document title in the spooler UI
	Datatype string // spooler datatype; defaults to "RAW"
}

// PrinterRoute is how a named printer should be reached, as resolved by
// ResolvePrinter. Network printers whose queues cannot carry the RAW datatype
// (WSD ports; V4 drivers) are routed to a direct raw-TCP socket; genuinely
// local/USB queues stay on the spooler.
type PrinterRoute struct {
	Kind string // "socket" | "spooler"
	Addr string // dial address (host:port) when Kind == "socket"
	Why  string // short human explanation, for -v and error messages
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

// OpenSocket dials a network printer's raw print port (AppSocket/JetDirect,
// conventionally TCP 9100) and returns a Writer that streams the native PCL/PS
// straight to the device. This is the network analog of the Windows RAW spooler
// and of CUPS's socket:// backend (what `lp -o raw` uses): it needs no OS-side
// port, queue, or driver, so it sidesteps queues that silently drop the RAW
// datatype (WSD ports and V4 drivers). addr is host:port; if the port is absent,
// pass it via the caller (see HostPort). Works on every platform.
func OpenSocket(addr string) (Writer, error) {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect %s (raw print port): %w", addr, err)
	}
	return conn, nil // net.Conn is an io.WriteCloser
}

// HostPort normalizes a user-supplied host (optionally "host:port") and a
// default port into a dial address. If host already carries a port, that port
// wins; otherwise defaultPort is applied.
func HostPort(host string, defaultPort int) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host // already host:port
	}
	return net.JoinHostPort(host, fmt.Sprintf("%d", defaultPort))
}

// MatchPrinter expands a possibly-partial printer name to the unique installed
// printer it identifies, so `--printer "(FC:82:A2)"` picks the one printer whose
// name contains that substring. If the printer list can't be enumerated (e.g.
// off Windows), the input is returned unchanged for the caller to handle.
func MatchPrinter(input string) (string, error) {
	names, err := ListPrinters()
	if err != nil || len(names) == 0 {
		return input, nil
	}
	return matchName(input, names)
}

// matchName resolves input against names: an exact match wins (so a name that is
// a substring of another still selects itself); otherwise a case-insensitive
// substring must match exactly one name. Zero or multiple matches are an error.
func matchName(input string, names []string) (string, error) {
	for _, n := range names {
		if n == input {
			return n, nil
		}
	}
	lc := strings.ToLower(input)
	var m []string
	for _, n := range names {
		if strings.Contains(strings.ToLower(n), lc) {
			m = append(m, n)
		}
	}
	switch len(m) {
	case 1:
		return m[0], nil
	case 0:
		return "", fmt.Errorf("no printer matches %q (run --list-printers)", input)
	default:
		return "", fmt.Errorf("%q is ambiguous — matches %d printers: %s (be more specific)", input, len(m), strings.Join(m, ", "))
	}
}
