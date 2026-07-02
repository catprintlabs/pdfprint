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

func TestHasPrefix(t *testing.T) {
	base := []int{1, 3, 6, 1, 2, 1, 43, 15, 1, 1, 5}
	if !hasPrefix(append(append([]int{}, base...), 1, 2), base) {
		t.Error("child OID should match prefix")
	}
	if hasPrefix([]int{1, 3, 6, 1, 2, 1, 43, 15, 1, 1, 6}, base) {
		t.Error("sibling column should not match prefix")
	}
}
