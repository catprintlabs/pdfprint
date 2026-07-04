package probe

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Minimal SNMPv1 client: enough to GET sysDescr and walk the Printer MIB's
// prtInterpreterLangDescription table. Hand-rolled BER, no dependency.

var (
	oidSysDescr        = []int{1, 3, 6, 1, 2, 1, 1, 1, 0}         // sysDescr.0
	oidInterpreterDesc = []int{1, 3, 6, 1, 2, 1, 43, 15, 1, 1, 5} // prtInterpreterLangDescription
	oidPrtInputDesc    = []int{1, 3, 6, 1, 2, 1, 43, 8, 2, 1, 18} // prtInputDescription (tray name)
	oidPrtInputMedia   = []int{1, 3, 6, 1, 2, 1, 43, 8, 2, 1, 12} // prtInputMediaName (loaded media)
)

var snmpReqID = 0

// probeSNMP queries the printer over SNMP (UDP 161, community "public").
func probeSNMP(host string, timeout time.Duration) (*Caps, error) {
	conn, err := net.DialTimeout("udp", net.JoinHostPort(host, "161"), timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))

	caps := &Caps{Source: "SNMP"}

	if _, val, tag, ok := snmpGetNext(conn, oidSysDescr[:len(oidSysDescr)-1]); ok && tag == 0x04 {
		caps.Model = string(val)
	}

	// Walk the interpreter-description column; each row names a supported PDL.
	cur := oidInterpreterDesc
	for i := 0; i < 32; i++ {
		oid, val, tag, ok := snmpGetNext(conn, cur)
		if !ok || !hasPrefix(oid, oidInterpreterDesc) {
			break
		}
		if tag == 0x04 && len(val) > 0 {
			caps.Languages = append(caps.Languages, string(val))
		}
		cur = oid
	}

	caps.Trays = snmpTrays(conn)

	if caps.Model == "" && len(caps.Languages) == 0 {
		return nil, fmt.Errorf("no SNMP data")
	}
	return caps, nil
}

// snmpTrays walks the Printer-MIB prtInputTable, correlating each tray's
// description and loaded-media columns by their shared row index.
func snmpTrays(conn net.Conn) []Tray {
	byRow := map[string]*Tray{}
	var order []string

	walk := func(base []int, set func(t *Tray, val string)) {
		cur := base
		for i := 0; i < 32; i++ {
			oid, val, tag, ok := snmpGetNext(conn, cur)
			if !ok || !hasPrefix(oid, base) {
				break
			}
			cur = oid
			if tag != 0x04 { // OCTET STRING
				continue
			}
			row := oidTail(oid, len(base))
			t := byRow[row]
			if t == nil {
				t = &Tray{}
				byRow[row] = t
				order = append(order, row)
			}
			set(t, string(val))
		}
	}

	walk(oidPrtInputDesc, func(t *Tray, v string) { t.Source = strings.TrimSpace(v) })
	walk(oidPrtInputMedia, func(t *Tray, v string) { t.Size = normalizeMediaName(v) })

	var out []Tray
	for _, row := range order {
		t := byRow[row]
		if t.Source == "" && t.Size == "" {
			continue
		}
		out = append(out, *t)
	}
	return out
}

// oidTail joins the index sub-identifiers that follow a table column's base OID
// into a stable row key (e.g. "1.2").
func oidTail(oid []int, prefixLen int) string {
	parts := make([]string, 0, len(oid)-prefixLen)
	for _, n := range oid[prefixLen:] {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, ".")
}

// snmpGetNext sends a GetNextRequest for oid and returns the next (oid, value).
func snmpGetNext(conn net.Conn, oid []int) (nextOID []int, value []byte, tag byte, ok bool) {
	snmpReqID++
	varbind := berTLV(0x30, append(berOID(oid), berTLV(0x05, nil)...))
	pdu := berTLV(0xA1, concat(berInt(snmpReqID), berInt(0), berInt(0), berTLV(0x30, varbind)))
	msg := berTLV(0x30, concat(berInt(0), berTLV(0x04, []byte("public")), pdu))

	if _, err := conn.Write(msg); err != nil {
		return nil, nil, 0, false
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, nil, 0, false
	}
	return parseSNMPResp(buf[:n])
}

// parseSNMPResp drills into the response to the first varbind's OID and value.
func parseSNMPResp(b []byte) (oid []int, value []byte, tag byte, ok bool) {
	_, msg, _, ok := readTLV(b) // outer SEQUENCE
	if !ok {
		return
	}
	_, _, msg, ok = readTLV(msg) // version -> rest
	if !ok {
		return
	}
	_, _, msg, ok = readTLV(msg) // community -> rest
	if !ok {
		return
	}
	_, pdu, _, ok := readTLV(msg) // GetResponse PDU
	if !ok {
		return
	}
	_, _, pdu, ok = readTLV(pdu) // request-id
	if !ok {
		return
	}
	_, _, pdu, ok = readTLV(pdu) // error-status
	if !ok {
		return
	}
	_, _, pdu, ok = readTLV(pdu) // error-index
	if !ok {
		return
	}
	_, vbl, _, ok := readTLV(pdu) // varbind list SEQUENCE
	if !ok {
		return
	}
	_, vb, _, ok := readTLV(vbl) // first varbind SEQUENCE
	if !ok {
		return
	}
	_, oidBytes, vb, ok := readTLV(vb) // OID
	if !ok {
		return
	}
	vtag, val, _, ok := readTLV(vb) // value
	if !ok {
		return
	}
	return decodeOID(oidBytes), val, vtag, true
}

// --- BER helpers ---

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func berTLV(tag byte, content []byte) []byte {
	return append(append([]byte{tag}, berLen(len(content))...), content...)
}

func berLen(n int) []byte {
	if n < 0x80 {
		return []byte{byte(n)}
	}
	var tmp []byte
	for n > 0 {
		tmp = append([]byte{byte(n & 0xff)}, tmp...)
		n >>= 8
	}
	return append([]byte{byte(0x80 | len(tmp))}, tmp...)
}

func berInt(v int) []byte {
	if v == 0 {
		return berTLV(0x02, []byte{0x00})
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte(v & 0xff)}, b...)
		v >>= 8
	}
	if b[0]&0x80 != 0 {
		b = append([]byte{0x00}, b...) // keep it positive
	}
	return berTLV(0x02, b)
}

func berOID(oid []int) []byte {
	if len(oid) < 2 {
		return berTLV(0x06, nil)
	}
	b := []byte{byte(oid[0]*40 + oid[1])}
	for _, sub := range oid[2:] {
		b = append(b, base128(sub)...)
	}
	return berTLV(0x06, b)
}

func base128(n int) []byte {
	if n == 0 {
		return []byte{0}
	}
	var tmp []byte
	for n > 0 {
		tmp = append([]byte{byte(n & 0x7f)}, tmp...)
		n >>= 7
	}
	for i := 0; i < len(tmp)-1; i++ {
		tmp[i] |= 0x80
	}
	return tmp
}

func readTLV(b []byte) (tag byte, content, rest []byte, ok bool) {
	if len(b) < 2 {
		return 0, nil, nil, false
	}
	tag = b[0]
	i := 1
	l := int(b[i])
	i++
	if l&0x80 != 0 {
		n := l & 0x7f
		if n == 0 || len(b) < i+n {
			return 0, nil, nil, false
		}
		l = 0
		for k := 0; k < n; k++ {
			l = (l << 8) | int(b[i])
			i++
		}
	}
	if len(b) < i+l {
		return 0, nil, nil, false
	}
	return tag, b[i : i+l], b[i+l:], true
}

func decodeOID(b []byte) []int {
	if len(b) == 0 {
		return nil
	}
	out := []int{int(b[0]) / 40, int(b[0]) % 40}
	n := 0
	for _, c := range b[1:] {
		n = (n << 7) | int(c&0x7f)
		if c&0x80 == 0 {
			out = append(out, n)
			n = 0
		}
	}
	return out
}

func hasPrefix(oid, prefix []int) bool {
	if len(oid) < len(prefix) {
		return false
	}
	for i := range prefix {
		if oid[i] != prefix[i] {
			return false
		}
	}
	return true
}
