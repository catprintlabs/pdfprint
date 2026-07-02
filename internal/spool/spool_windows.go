//go:build windows

package spool

import (
	"fmt"
	"net/url"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	winspool             = syscall.NewLazyDLL("winspool.drv")
	procOpenPrinter      = winspool.NewProc("OpenPrinterW")
	procGetPrinter       = winspool.NewProc("GetPrinterW")
	procStartDocPrinter  = winspool.NewProc("StartDocPrinterW")
	procStartPagePrinter = winspool.NewProc("StartPagePrinter")
	procWritePrinter     = winspool.NewProc("WritePrinter")
	procEndPagePrinter   = winspool.NewProc("EndPagePrinter")
	procEndDocPrinter    = winspool.NewProc("EndDocPrinter")
	procClosePrinter     = winspool.NewProc("ClosePrinter")
	procEnumPrinters     = winspool.NewProc("EnumPrintersW")

	advapi32            = syscall.NewLazyDLL("advapi32.dll")
	procRegOpenKeyEx    = advapi32.NewProc("RegOpenKeyExW")
	procRegQueryValueEx = advapi32.NewProc("RegQueryValueExW")
	procRegEnumKeyEx    = advapi32.NewProc("RegEnumKeyExW")
	procRegCloseKey     = advapi32.NewProc("RegCloseKey")
)

const (
	printerEnumLocal       = 0x00000002
	printerEnumConnections = 0x00000004

	hkeyLocalMachine = 0x80000002
	keyRead          = 0x20019
	regSZ            = 1
	regExpandSZ      = 2
	regDWORD         = 4
	errorSuccess     = 0
	errorNoMoreItems = 259

	defaultRawPort = 9100 // AppSocket/JetDirect
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

// printerInfo2 mirrors the leading fields of Win32 PRINTER_INFO_2W. We only read
// pPortName; later fields are declared so the struct size/offsets are correct on
// 64-bit (all leading fields are pointers, so offsets stay 8-byte aligned).
type printerInfo2 struct {
	pServerName         *uint16
	pPrinterName        *uint16
	pShareName          *uint16
	pPortName           *uint16
	pDriverName         *uint16
	pComment            *uint16
	pLocation           *uint16
	pDevMode            uintptr
	pSepFile            *uint16
	pPrintProcessor     *uint16
	pDatatype           *uint16
	pParameters         *uint16
	pSecurityDescriptor uintptr
	attributes          uint32
	priority            uint32
	defaultPriority     uint32
	startTime           uint32
	untilTime           uint32
	status              uint32
	cJobs               uint32
	averagePPM          uint32
}

// ResolvePrinter decides how to reach a named printer. A WSD port or a Standard
// TCP/IP port means a network device: we resolve its IP and route to a direct
// raw-TCP socket (port 9100), which sidesteps the RAW-datatype drop that WSD
// ports and V4 drivers cause. Anything else (USB/local) stays on the spooler,
// where RAW passes through fine.
func ResolvePrinter(name string) (PrinterRoute, error) {
	port, err := printerPort(name)
	if err != nil {
		return PrinterRoute{}, err
	}

	// WSD: cannot carry RAW. Find the device IP via its PnP LocationInformation.
	if strings.HasPrefix(strings.ToUpper(port), "WSD") {
		ip, err := wsdDeviceIP(name)
		if err != nil {
			return PrinterRoute{}, fmt.Errorf(
				"printer %q is on a WSD port, which cannot receive RAW print data; "+
					"could not auto-discover its IP (%v) — pass --host <ip> to print over raw TCP",
				name, err)
		}
		return PrinterRoute{
			Kind: "socket",
			Addr: HostPort(ip, defaultRawPort),
			Why:  fmt.Sprintf("WSD port %q → raw TCP %s:%d", port, ip, defaultRawPort),
		}, nil
	}

	// Standard TCP/IP port: read host + raw port from the port monitor registry.
	if host, rawPort, ok := tcpipPortTarget(port); ok {
		return PrinterRoute{
			Kind: "socket",
			Addr: HostPort(host, rawPort),
			Why:  fmt.Sprintf("TCP/IP port %q → raw TCP %s:%d", port, host, rawPort),
		}, nil
	}

	// Local/USB or unrecognized port: the spooler handles RAW correctly here.
	return PrinterRoute{Kind: "spooler", Why: fmt.Sprintf("port %q → spooler RAW", port)}, nil
}

// printerPort returns the port name of a named printer via GetPrinter level 2.
func printerPort(name string) (string, error) {
	pName, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", fmt.Errorf("printer name %q: %w", name, err)
	}
	var h syscall.Handle
	r, _, e := procOpenPrinter.Call(uintptr(unsafe.Pointer(pName)), uintptr(unsafe.Pointer(&h)), 0)
	if r == 0 {
		return "", fmt.Errorf("OpenPrinter(%q): %w", name, e)
	}
	defer procClosePrinter.Call(uintptr(h))

	var needed uint32
	procGetPrinter.Call(uintptr(h), 2, 0, 0, uintptr(unsafe.Pointer(&needed)))
	if needed == 0 {
		return "", fmt.Errorf("GetPrinter(%q): zero size", name)
	}
	buf := make([]byte, needed)
	r, _, e = procGetPrinter.Call(uintptr(h), 2, uintptr(unsafe.Pointer(&buf[0])), uintptr(needed), uintptr(unsafe.Pointer(&needed)))
	if r == 0 {
		return "", fmt.Errorf("GetPrinter(%q): %w", name, e)
	}
	pi := (*printerInfo2)(unsafe.Pointer(&buf[0]))
	return utf16PtrToString(pi.pPortName), nil
}

// tcpipPortTarget reads a Standard TCP/IP port's host and raw port number from
// the port monitor's registry key. rawPort is the configured PortNumber only when
// the protocol is RAW (1); otherwise it defaults to 9100.
func tcpipPortTarget(port string) (host string, rawPort int, ok bool) {
	key := `SYSTEM\CurrentControlSet\Control\Print\Monitors\Standard TCP/IP Port\Ports\` + port
	h, err := regOpen(hkeyLocalMachine, key)
	if err != nil {
		return "", 0, false
	}
	defer procRegCloseKey.Call(uintptr(h))

	host, _ = regString(h, "HostName")
	if host == "" {
		host, _ = regString(h, "IPAddress")
	}
	if host == "" {
		return "", 0, false
	}
	rawPort = defaultRawPort
	if proto, err := regDword(h, "Protocol"); err == nil && proto == 1 { // 1 = RAW
		if pn, err := regDword(h, "PortNumber"); err == nil && pn > 0 {
			rawPort = int(pn)
		}
	}
	return host, rawPort, true
}

// wsdDeviceIP finds the IP of a WSD-connected printer by matching the PnP device
// (under SWD\PRINTENUM) whose FriendlyName equals the printer name and parsing
// the host out of its LocationInformation URL (e.g. http://10.0.1.151:53202/...).
func wsdDeviceIP(printerName string) (string, error) {
	const base = `SYSTEM\CurrentControlSet\Enum\SWD\PRINTENUM`
	root, err := regOpen(hkeyLocalMachine, base)
	if err != nil {
		return "", err
	}
	defer procRegCloseKey.Call(uintptr(root))

	subs, err := regSubkeys(root)
	if err != nil {
		return "", err
	}
	for _, s := range subs {
		h, err := regOpen(hkeyLocalMachine, base+`\`+s)
		if err != nil {
			continue
		}
		fn, _ := regString(h, "FriendlyName")
		if fn != printerName {
			procRegCloseKey.Call(uintptr(h))
			continue
		}
		loc, err := regString(h, "LocationInformation")
		procRegCloseKey.Call(uintptr(h))
		if err != nil {
			return "", err
		}
		if u, err := url.Parse(strings.TrimSpace(loc)); err == nil && u.Hostname() != "" {
			return u.Hostname(), nil
		}
		return "", fmt.Errorf("no host in LocationInformation %q", loc)
	}
	return "", fmt.Errorf("no PnP device found with FriendlyName %q", printerName)
}

// --- minimal advapi32 registry read helpers (avoids a golang.org/x/sys dep) ---

func regOpen(root uintptr, path string) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var h syscall.Handle
	r, _, _ := procRegOpenKeyEx.Call(root, uintptr(unsafe.Pointer(p)), 0, keyRead, uintptr(unsafe.Pointer(&h)))
	if r != errorSuccess {
		return 0, fmt.Errorf("RegOpenKeyEx(%s): error %d", path, r)
	}
	return h, nil
}

func regString(h syscall.Handle, name string) (string, error) {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", err
	}
	var typ, size uint32
	r, _, _ := procRegQueryValueEx.Call(uintptr(h), uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&typ)), 0, uintptr(unsafe.Pointer(&size)))
	if r != errorSuccess {
		return "", fmt.Errorf("query %q: error %d", name, r)
	}
	if typ != regSZ && typ != regExpandSZ {
		return "", fmt.Errorf("value %q is not a string (type %d)", name, typ)
	}
	buf := make([]uint16, size/2+1)
	r, _, _ = procRegQueryValueEx.Call(uintptr(h), uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r != errorSuccess {
		return "", fmt.Errorf("query %q: error %d", name, r)
	}
	return syscall.UTF16ToString(buf), nil
}

func regDword(h syscall.Handle, name string) (uint32, error) {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	var typ, size, val uint32
	size = 4
	r, _, _ := procRegQueryValueEx.Call(uintptr(h), uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&val)), uintptr(unsafe.Pointer(&size)))
	if r != errorSuccess {
		return 0, fmt.Errorf("query %q: error %d", name, r)
	}
	if typ != regDWORD {
		return 0, fmt.Errorf("value %q is not a DWORD (type %d)", name, typ)
	}
	return val, nil
}

func regSubkeys(h syscall.Handle) ([]string, error) {
	var names []string
	for i := uint32(0); ; i++ {
		buf := make([]uint16, 256)
		n := uint32(len(buf))
		r, _, _ := procRegEnumKeyEx.Call(uintptr(h), uintptr(i),
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)), 0, 0, 0, 0)
		if r == errorNoMoreItems {
			break
		}
		if r != errorSuccess {
			return names, fmt.Errorf("RegEnumKeyEx: error %d", r)
		}
		names = append(names, syscall.UTF16ToString(buf[:n]))
	}
	return names, nil
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
