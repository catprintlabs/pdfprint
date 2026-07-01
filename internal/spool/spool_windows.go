//go:build windows

package spool

import (
	"fmt"
	"syscall"
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
)

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
