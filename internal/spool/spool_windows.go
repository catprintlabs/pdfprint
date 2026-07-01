//go:build windows

package spool

import (
	"fmt"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	winspool             = syscall.NewLazyDLL("winspool.drv")
	procOpenPrinter      = winspool.NewProc("OpenPrinterW")
	procStartDocPrinter  = winspool.NewProc("StartDocPrinterW")
	procStartPagePrinter = winspool.NewProc("StartPagePrinter")
	procWritePrinter     = winspool.NewProc("WritePrinter")
	procEndPagePrinter   = winspool.NewProc("EndPagePrinter")
	procEndDocPrinter    = winspool.NewProc("EndDocPrinter")
	procClosePrinter     = winspool.NewProc("ClosePrinter")
	procEnumPrinters     = winspool.NewProc("EnumPrintersW")
)

const (
	printerEnumLocal       = 0x00000002
	printerEnumConnections = 0x00000004
)

// printerInfo4 mirrors Win32 PRINTER_INFO_4W — the fast enumeration level.
type printerInfo4 struct {
	pPrinterName *uint16
	pServerName  *uint16
	attributes   uint32
}

// ListPrinters returns the names of installed/connected printers, exactly as
// they must be passed to --printer.
func ListPrinters() ([]string, error) {
	flags := uintptr(printerEnumLocal | printerEnumConnections)

	// First call: discover the required buffer size.
	var needed, returned uint32
	procEnumPrinters.Call(
		flags, 0, 4, // Level 4
		0, 0,
		uintptr(unsafe.Pointer(&needed)),
		uintptr(unsafe.Pointer(&returned)),
	)
	if needed == 0 {
		return nil, nil // no printers
	}

	buf := make([]byte, needed)
	r, _, e := procEnumPrinters.Call(
		flags, 0, 4,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(needed),
		uintptr(unsafe.Pointer(&needed)),
		uintptr(unsafe.Pointer(&returned)),
	)
	if r == 0 {
		return nil, fmt.Errorf("EnumPrinters: %w", e)
	}

	names := make([]string, 0, returned)
	stride := unsafe.Sizeof(printerInfo4{})
	for i := uintptr(0); i < uintptr(returned); i++ {
		info := (*printerInfo4)(unsafe.Pointer(&buf[i*stride]))
		names = append(names, utf16PtrToString(info.pPrinterName))
	}
	return names, nil
}

// utf16PtrToString reads a NUL-terminated UTF-16 string from a raw pointer.
func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	var u []uint16
	for ptr := unsafe.Pointer(p); ; ptr = unsafe.Add(ptr, 2) {
		c := *(*uint16)(ptr)
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}

// docInfo1 mirrors Win32 DOC_INFO_1.
type docInfo1 struct {
	pDocName    *uint16
	pOutputFile *uint16
	pDatatype   *uint16
}

// printerWriter streams bytes to an open spooler job with the RAW datatype.
type printerWriter struct {
	handle syscall.Handle
}

// Open acquires a spooler handle and starts a RAW document + page.
func Open(job Job) (Writer, error) {
	name, err := syscall.UTF16PtrFromString(job.Printer)
	if err != nil {
		return nil, fmt.Errorf("printer name %q: %w", job.Printer, err)
	}

	var h syscall.Handle
	r, _, e := procOpenPrinter.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&h)),
		0, // default PRINTER_DEFAULTS
	)
	if r == 0 {
		return nil, fmt.Errorf("OpenPrinter(%q): %w", job.Printer, e)
	}

	datatype := job.Datatype
	if datatype == "" {
		datatype = "RAW"
	}
	docName := job.DocName
	if docName == "" {
		docName = "pdfprint job"
	}
	pDoc, _ := syscall.UTF16PtrFromString(docName)
	pType, _ := syscall.UTF16PtrFromString(datatype)
	di := docInfo1{pDocName: pDoc, pOutputFile: nil, pDatatype: pType}

	jobID, _, e := procStartDocPrinter.Call(
		uintptr(h), 1, uintptr(unsafe.Pointer(&di)),
	)
	if jobID == 0 {
		procClosePrinter.Call(uintptr(h))
		return nil, fmt.Errorf("StartDocPrinter: %w", e)
	}

	if r, _, e := procStartPagePrinter.Call(uintptr(h)); r == 0 {
		procEndDocPrinter.Call(uintptr(h))
		procClosePrinter.Call(uintptr(h))
		return nil, fmt.Errorf("StartPagePrinter: %w", e)
	}

	return &printerWriter{handle: h}, nil
}

func (w *printerWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	var written uint32
	r, _, e := procWritePrinter.Call(
		uintptr(w.handle),
		uintptr(unsafe.Pointer(&p[0])),
		uintptr(len(p)),
		uintptr(unsafe.Pointer(&written)),
	)
	if r == 0 {
		return int(written), fmt.Errorf("WritePrinter: %w", e)
	}
	if int(written) != len(p) {
		return int(written), fmt.Errorf("WritePrinter: short write %d of %d", written, len(p))
	}
	return int(written), nil
}

func (w *printerWriter) Close() error {
	// Best-effort teardown in order; report the first failure.
	var firstErr error
	fail := func(name string, r uintptr, e error) {
		if r == 0 && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", name, e)
		}
	}
	r, _, e := procEndPagePrinter.Call(uintptr(w.handle))
	fail("EndPagePrinter", r, e)
	r, _, e = procEndDocPrinter.Call(uintptr(w.handle))
	fail("EndDocPrinter", r, e)
	r, _, e = procClosePrinter.Call(uintptr(w.handle))
	fail("ClosePrinter", r, e)
	return firstErr
}
