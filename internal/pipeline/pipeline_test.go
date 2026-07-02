package pipeline

import "testing"

func TestParseMediaBox(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		w, h   int
		wantOK bool
	}{
		{"legal", "... /Type /Page /MediaBox [0 0 612 1008] /Contents ...", 612, 1008, true},
		{"letter tight", "/MediaBox[0 0 612 792]", 612, 792, true},
		{"floats + offset origin", "/MediaBox [ 12.0 12.0 624.0 804.0 ]", 612, 792, true},
		{"none", "no mediabox here", 0, 0, false},
	}
	for _, c := range cases {
		w, h, ok := parseMediaBox([]byte(c.in))
		if ok != c.wantOK || w != c.w || h != c.h {
			t.Errorf("%s: parseMediaBox = %d,%d,%v; want %d,%d,%v", c.name, w, h, ok, c.w, c.h, c.wantOK)
		}
	}
}
