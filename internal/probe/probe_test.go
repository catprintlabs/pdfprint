package probe

import "testing"

func bp(v bool) *bool { return &v }

func TestSuggestDevice(t *testing.T) {
	cases := []struct {
		name   string
		caps   Caps
		color  *bool
		want   string
		wantOK bool
	}{
		{"pcl+color -> pxlcolor", Caps{Formats: []string{"application/vnd.hp-PCL"}, Color: bp(true)}, nil, "pxlcolor", true},
		{"pcl+mono printer -> pxlmono", Caps{Languages: []string{"PCL"}, Color: bp(false)}, nil, "pxlmono", true},
		{"pcl, --color mono overrides", Caps{Languages: []string{"PCL"}, Color: bp(true)}, bp(false), "pxlmono", true},
		{"postscript only -> ps2write", Caps{Formats: []string{"application/postscript"}}, nil, "ps2write", true},
		{"explicit pcl5 only -> ljet4", Caps{Languages: []string{"PCL5"}}, nil, "ljet4", true},
		{"pcl-xl + pcl5 -> pxl (not ljet4)", Caps{Languages: []string{"PCL5", "PCLXL"}, Color: bp(true)}, nil, "pxlcolor", true},
		{"nothing targetable", Caps{Formats: []string{"image/jpeg"}}, nil, "", false},
	}
	for _, c := range cases {
		got, _, ok := c.caps.SuggestDevice(c.color)
		if ok != c.wantOK || got != c.want {
			t.Errorf("%s: got (%q, ok=%v), want (%q, ok=%v)", c.name, got, ok, c.want, c.wantOK)
		}
	}
}

func TestBEREncoding(t *testing.T) {
	// berInt round-trips through readTLV.
	for _, v := range []int{0, 1, 127, 128, 255, 300, 65535} {
		tag, content, _, ok := readTLV(berInt(v))
		if !ok || tag != 0x02 {
			t.Fatalf("berInt(%d): bad TLV", v)
		}
		got := 0
		for _, b := range content {
			got = got<<8 | int(b)
		}
		if got != v {
			t.Errorf("berInt(%d) decoded to %d", v, got)
		}
	}

	// berOID encodes/decodes the interpreter-description OID.
	oid := []int{1, 3, 6, 1, 2, 1, 43, 15, 1, 1, 5}
	tag, content, _, ok := readTLV(berOID(oid))
	if !ok || tag != 0x06 {
		t.Fatal("berOID: bad TLV")
	}
	dec := decodeOID(content)
	if len(dec) != len(oid) {
		t.Fatalf("decodeOID len = %d, want %d", len(dec), len(oid))
	}
	for i := range oid {
		if dec[i] != oid[i] {
			t.Errorf("decodeOID[%d] = %d, want %d", i, dec[i], oid[i])
		}
	}
}

func TestDimsToLabel(t *testing.T) {
	cases := []struct {
		x, y int
		want string
	}{
		{21590, 27940, "Letter"},
		{27940, 21590, "Letter"}, // landscape orientation still matches
		{21590, 35560, "Legal"},
		{21000, 29700, "A4"},
		{10000, 20000, "100x200mm"}, // unknown -> mm fallback
	}
	for _, c := range cases {
		if got := dimsToLabel(c.x, c.y); got != c.want {
			t.Errorf("dimsToLabel(%d,%d) = %q, want %q", c.x, c.y, got, c.want)
		}
	}
}

func TestPrettySource(t *testing.T) {
	for in, want := range map[string]string{
		"tray-1": "Tray 1",
		"main":   "Main",
		"Tray 2": "Tray 2",
		"":       "(tray)",
	} {
		if got := prettySource(in); got != want {
			t.Errorf("prettySource(%q) = %q, want %q", in, got, want)
		}
	}
}

// attr encodes one IPP attribute (tag, name, value) the way readValue expects.
func attr(tag byte, name string, val []byte) []byte {
	b := []byte{tag, byte(len(name) >> 8), byte(len(name))}
	b = append(b, name...)
	b = append(b, byte(len(val)>>8), byte(len(val)))
	return append(b, val...)
}

func TestParseIPPTrays(t *testing.T) {
	i32 := func(n int) []byte { return []byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)} }

	body := []byte{0x01, 0x00, 0x00, 0x00, 0, 0, 0, 1} // version, status, request-id
	body = append(body, tagPrinterAttrs)
	body = append(body, attr(tagTextNoLang, "printer-make-and-model", []byte("Test Printer"))...)

	// One media-col-ready value: tray-2 loaded with Letter (215.90 x 279.40 mm).
	mc := attr(tagBegCollection, "media-col-ready", nil)
	mc = append(mc, attr(tagMemberName, "", []byte("media-size"))...)
	mc = append(mc, attr(tagBegCollection, "", nil)...)
	mc = append(mc, attr(tagMemberName, "", []byte("x-dimension"))...)
	mc = append(mc, attr(tagInteger, "", i32(21590))...)
	mc = append(mc, attr(tagMemberName, "", []byte("y-dimension"))...)
	mc = append(mc, attr(tagInteger, "", i32(27940))...)
	mc = append(mc, attr(tagEndCollection, "", nil)...)
	mc = append(mc, attr(tagMemberName, "", []byte("media-source"))...)
	mc = append(mc, attr(tagKeyword, "", []byte("tray-2"))...)
	mc = append(mc, attr(tagMemberName, "", []byte("media-type"))...)
	mc = append(mc, attr(tagKeyword, "", []byte("stationery"))...)
	mc = append(mc, attr(tagEndCollection, "", nil)...)
	body = append(body, mc...)

	// An advertised-but-empty tray via media-source-supported.
	body = append(body, attr(tagKeyword, "media-source-supported", []byte("manual"))...)
	body = append(body, tagEndAttrs)

	caps, err := parseIPP(body)
	if err != nil {
		t.Fatalf("parseIPP: %v", err)
	}
	if len(caps.Trays) != 2 {
		t.Fatalf("got %d trays, want 2: %+v", len(caps.Trays), caps.Trays)
	}
	if got := caps.Trays[0]; got.Source != "tray-2" || got.Size != "Letter" || got.Type != "stationery" {
		t.Errorf("tray[0] = %+v, want {tray-2 Letter stationery}", got)
	}
	if got := caps.Trays[1]; got.Source != "manual" || got.Size != "" {
		t.Errorf("tray[1] = %+v, want empty 'manual' tray", got)
	}
	if got := caps.Trays[0].Desc(); got != "Tray 2: Letter (stationery)" {
		t.Errorf("Desc() = %q", got)
	}
}

func TestHasPrefix(t *testing.T) {
	base := []int{1, 3, 6, 1, 2, 1, 43, 15, 1, 1, 5}
	if !hasPrefix(append(append([]int{}, base...), 1, 2), base) {
		t.Error("child OID should match prefix")
	}
	if hasPrefix([]int{1, 3, 6, 1, 2, 1, 43, 15, 1, 1, 6}, base) {
		t.Error("sibling column should not match prefix")
	}
}
