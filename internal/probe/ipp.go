package probe

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// IPP is a compact hand-rolled client for a single Get-Printer-Attributes call
// (RFC 8010/8011). We only encode what that operation needs and parse the few
// attributes that drive device selection.

const (
	ippVersion10 = 0x0100 // 1.0 — widest compatibility for a read-only query
	opGetPrinter = 0x000B // Get-Printer-Attributes

	tagOperationAttrs = 0x01
	tagPrinterAttrs   = 0x04
	tagEndAttrs       = 0x03

	tagCharset     = 0x47
	tagNaturalLang = 0x48
	tagURI         = 0x45
	tagKeyword     = 0x44
	tagBoolean     = 0x22
	tagInteger     = 0x21
	tagMimeType    = 0x49
	tagTextNoLang  = 0x41
	tagNameNoLang  = 0x42

	// Collection (media-col-ready) encoding, RFC 8010 §3.1.6.
	tagBegCollection = 0x34
	tagEndCollection = 0x37
	tagMemberName    = 0x4A
)

// probeIPP asks the printer over IPP (TCP 631). It tries the common resource
// paths and returns the first that yields a usable answer.
func probeIPP(host string, timeout time.Duration) (*Caps, error) {
	client := &http.Client{Timeout: timeout}
	var lastErr error
	for _, path := range []string{"/ipp/print", "/ipp/printer", "/"} {
		uri := fmt.Sprintf("ipp://%s%s", host, path)
		body := buildGetPrinterAttrs(uri)
		url := fmt.Sprintf("http://%s:631%s", host, path)

		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/ipp")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		caps, err := parseIPP(raw)
		if err != nil {
			lastErr = err
			continue
		}
		if len(caps.Formats) == 0 && caps.Model == "" {
			lastErr = fmt.Errorf("IPP response had no useful attributes")
			continue
		}
		return caps, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no IPP response")
	}
	return nil, lastErr
}

// buildGetPrinterAttrs encodes a Get-Printer-Attributes request for uri.
func buildGetPrinterAttrs(uri string) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint16(ippVersion10))
	binary.Write(&b, binary.BigEndian, uint16(opGetPrinter))
	binary.Write(&b, binary.BigEndian, uint32(1)) // request-id

	b.WriteByte(tagOperationAttrs)
	writeAttr(&b, tagCharset, "attributes-charset", "utf-8")
	writeAttr(&b, tagNaturalLang, "attributes-natural-language", "en")
	writeAttr(&b, tagURI, "printer-uri", uri)
	// Ask for the specific attributes we use (first value named, rest unnamed).
	writeAttr(&b, tagKeyword, "requested-attributes", "printer-make-and-model")
	writeAttr(&b, tagKeyword, "", "document-format-supported")
	writeAttr(&b, tagKeyword, "", "color-supported")
	writeAttr(&b, tagKeyword, "", "sides-supported")
	writeAttr(&b, tagKeyword, "", "printer-name")
	writeAttr(&b, tagKeyword, "", "media-ready")
	writeAttr(&b, tagKeyword, "", "media-default")
	writeAttr(&b, tagKeyword, "", "media-supported")
	writeAttr(&b, tagKeyword, "", "media-col-ready")        // per-tray loaded media (source + size)
	writeAttr(&b, tagKeyword, "", "media-source-supported") // the full tray list

	b.WriteByte(tagEndAttrs)
	return b.Bytes()
}

func writeAttr(b *bytes.Buffer, tag byte, name, value string) {
	b.WriteByte(tag)
	binary.Write(b, binary.BigEndian, uint16(len(name)))
	b.WriteString(name)
	binary.Write(b, binary.BigEndian, uint16(len(value)))
	b.WriteString(value)
}

// parseIPP walks the response and extracts the attributes we care about.
func parseIPP(raw []byte) (*Caps, error) {
	if len(raw) < 8 {
		return nil, fmt.Errorf("IPP response too short")
	}
	p := raw[8:] // skip version(2) + status(2) + request-id(4)

	caps := &Caps{Source: "IPP"}
	var curName string
	var sourceSupported []string
	for len(p) > 0 {
		tag := p[0]
		p = p[1:]
		if tag <= 0x05 { // delimiter tag (group boundary or end)
			if tag == tagEndAttrs {
				break
			}
			curName = ""
			continue
		}
		name, value, rest, ok := readValue(p)
		if !ok {
			break
		}
		p = rest
		if len(name) > 0 {
			curName = string(name)
		}

		// media-col-ready is a 1setOf collection: each value describes one tray.
		if tag == tagBegCollection {
			members, r, cok := parseCollection(p)
			if !cok {
				break
			}
			p = r
			if curName == "media-col-ready" {
				if t, ok := trayFromMembers(members); ok {
					caps.Trays = append(caps.Trays, t)
				}
			}
			continue
		}
		if curName == "media-source-supported" && tag == tagKeyword {
			sourceSupported = append(sourceSupported, string(value))
			continue
		}
		applyIPPAttr(caps, curName, tag, value)
	}

	mergeSources(caps, sourceSupported)
	deriveLanguages(caps)
	return caps, nil
}

// ippMember is one member of an IPP collection: a scalar (tag+val) or, when it
// is itself a collection (media-size), a nested member map in sub.
type ippMember struct {
	tag byte
	val []byte
	sub map[string]ippMember
}

// parseCollection reads member-attributes until the matching endCollection,
// starting just after a begCollection value. Nested collections recurse. It
// returns the members, the remaining bytes, and ok=false on malformed input.
func parseCollection(p []byte) (members map[string]ippMember, rest []byte, ok bool) {
	members = map[string]ippMember{}
	for len(p) > 0 {
		tag := p[0]
		p = p[1:]
		if tag == tagEndCollection {
			_, _, r, k := readValue(p) // consumes the empty name+value
			if !k {
				return members, p, false
			}
			return members, r, true
		}
		if tag != tagMemberName {
			return members, p, false
		}
		_, nameVal, r, k := readValue(p)
		if !k {
			return members, p, false
		}
		p = r
		member := string(nameVal)
		if len(p) < 1 {
			return members, p, false
		}
		vtag := p[0]
		p = p[1:]
		if vtag == tagBegCollection {
			_, _, r2, k2 := readValue(p) // empty begCollection value
			if !k2 {
				return members, p, false
			}
			sub, r3, k3 := parseCollection(r2)
			if !k3 {
				return members, r3, false
			}
			members[member] = ippMember{tag: vtag, sub: sub}
			p = r3
			continue
		}
		_, val, r2, k2 := readValue(p)
		if !k2 {
			return members, p, false
		}
		members[member] = ippMember{tag: vtag, val: val}
		p = r2
	}
	return members, p, false
}

// trayFromMembers builds a Tray from one media-col-ready collection. ok is false
// when it carries neither a source nor a size worth reporting.
func trayFromMembers(m map[string]ippMember) (Tray, bool) {
	var t Tray
	if src, ok := m["media-source"]; ok {
		t.Source = string(src.val)
	}
	if typ, ok := m["media-type"]; ok {
		t.Type = string(typ.val)
	}
	if sz, ok := m["media-size"]; ok && sz.sub != nil {
		x := ippInt(sz.sub["x-dimension"])
		y := ippInt(sz.sub["y-dimension"])
		if x > 0 && y > 0 {
			t.Size = dimsToLabel(x, y)
		}
	}
	if t.Source == "" && t.Size == "" {
		return t, false
	}
	return t, true
}

// ippInt decodes a big-endian IPP integer value.
func ippInt(m ippMember) int {
	n := 0
	for _, b := range m.val {
		n = n<<8 | int(b)
	}
	return n
}

// mergeSources appends any advertised tray (media-source-supported) that had no
// ready-media collection, so empty trays still appear in the list.
func mergeSources(caps *Caps, sources []string) {
	have := map[string]bool{}
	for _, t := range caps.Trays {
		have[strings.ToLower(t.Source)] = true
	}
	for _, s := range sources {
		if s == "" || strings.EqualFold(s, "auto") {
			continue
		}
		if !have[strings.ToLower(s)] {
			caps.Trays = append(caps.Trays, Tray{Source: s})
			have[strings.ToLower(s)] = true
		}
	}
}

// readValue reads one name-length/name/value-length/value triple.
func readValue(p []byte) (name, value, rest []byte, ok bool) {
	if len(p) < 2 {
		return nil, nil, nil, false
	}
	nl := int(binary.BigEndian.Uint16(p))
	p = p[2:]
	if len(p) < nl+2 {
		return nil, nil, nil, false
	}
	name = p[:nl]
	p = p[nl:]
	vl := int(binary.BigEndian.Uint16(p))
	p = p[2:]
	if len(p) < vl {
		return nil, nil, nil, false
	}
	value = p[:vl]
	return name, value, p[vl:], true
}

func applyIPPAttr(caps *Caps, name string, tag byte, value []byte) {
	switch name {
	case "document-format-supported":
		if tag == tagMimeType {
			caps.Formats = append(caps.Formats, string(value))
		}
	case "color-supported":
		if tag == tagBoolean && len(value) == 1 {
			v := value[0] != 0
			caps.Color = &v
		}
	case "sides-supported":
		if tag == tagKeyword && !strings.EqualFold(string(value), "one-sided") {
			d := true
			caps.Duplex = &d
		}
	case "printer-make-and-model", "printer-name":
		if caps.Model == "" && (tag == tagTextNoLang || tag == tagNameNoLang) {
			caps.Model = string(value)
		}
	case "media-ready":
		if tag == tagKeyword {
			caps.MediaReady = append(caps.MediaReady, string(value))
		}
	case "media-default":
		if tag == tagKeyword && caps.MediaDefault == "" {
			caps.MediaDefault = string(value)
		}
	case "media-supported":
		if tag == tagKeyword {
			caps.MediaSupported = append(caps.MediaSupported, string(value))
		}
	}
}

// deriveLanguages turns MIME formats into human PDL names for matching/summary.
func deriveLanguages(caps *Caps) {
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			caps.Languages = append(caps.Languages, s)
		}
	}
	for _, f := range caps.Formats {
		switch lf := strings.ToLower(f); {
		case strings.Contains(lf, "postscript"):
			add("PostScript")
		case strings.Contains(lf, "pclxl"), strings.Contains(lf, "pcl-xl"):
			add("PCL-XL")
		case strings.Contains(lf, "pclm"):
			add("PCLm")
		case strings.Contains(lf, "pcl"):
			add("PCL")
		case strings.Contains(lf, "pdf"):
			add("PDF")
		case strings.Contains(lf, "pwg-raster"):
			add("PWG Raster")
		case strings.Contains(lf, "urf"):
			add("Apple Raster")
		}
	}
}
