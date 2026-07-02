package spool

import "testing"

func TestMatchName(t *testing.T) {
	names := []string{
		"Xerox VersaLink C620 (FC:90:A7)",
		"Xerox VersaLink C620 (FC:90:88)",
		"Xerox VersaLink C620 (FC:89:BF)",
		"Xerox VersaLink C620 (FC:84:9B)",
		"Xerox VersaLink C620 (FC:82:A2)",
		"Win2PDF",
		"Win2Image",
		"OneNote (Desktop)",
		"Microsoft Print to PDF",
	}

	ok := []struct{ in, want string }{
		{"(FC:82:A2)", "Xerox VersaLink C620 (FC:82:A2)"}, // unique substring
		{"FC:90:A7", "Xerox VersaLink C620 (FC:90:A7)"},   // without parens
		{"Win2PDF", "Win2PDF"},                            // exact wins over Win2Image substring
		{"win2pdf", "Win2PDF"},                            // case-insensitive
		{"OneNote", "OneNote (Desktop)"},
		{"Xerox VersaLink C620 (FC:89:BF)", "Xerox VersaLink C620 (FC:89:BF)"}, // full name
	}
	for _, c := range ok {
		got, err := matchName(c.in, names)
		if err != nil || got != c.want {
			t.Errorf("matchName(%q) = (%q, %v), want %q", c.in, got, err, c.want)
		}
	}

	// Ambiguous and absent inputs must error.
	for _, in := range []string{"Xerox", "Win2", "C620", "FC:90", "nope"} {
		if got, err := matchName(in, names); err == nil {
			t.Errorf("matchName(%q) = %q, want error", in, got)
		}
	}
}
