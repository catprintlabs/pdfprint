// Package ppd parses PostScript Printer Description (PPD) files well enough to
// drive a Ghostscript-based print pipeline, the same way CUPS/foomatic-rip do.
//
// It is deliberately a pragmatic subset of the full PPD spec (Adobe 4.3). We
// capture:
//   - identity fields (NickName, ModelName, Manufacturer)
//   - device hints (DefaultResolution, ColorDevice, cupsFilter lines)
//   - UI options with their choices and defaults (PageSize, Duplex, ...)
//   - the Foomatic fields (*FoomaticRIPCommandLine, *FoomaticRIPOptionSetting)
//     that encode the Ghostscript command line for non-PostScript printers
//
// Anything we do not understand is ignored rather than treated as an error, so
// real-world vendor PPDs load cleanly.
package ppd

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// Choice is one selectable value of an Option (e.g. PageSize=A4).
type Choice struct {
	Keyword     string // machine keyword, e.g. "A4"
	Translation string // human label, e.g. "A4"
	Code        string // invocation value / PostScript snippet
}

// Option is a UI-selectable option group (from *OpenUI ... *CloseUI).
type Option struct {
	Keyword     string // e.g. "PageSize"
	Translation string // human label
	Type        string // PickOne | Boolean | PickMany
	Default     string // default choice keyword (from *Default<Keyword>)
	Choices     []Choice
	Order       int // order of first appearance, for stable iteration
}

// Choice looks up a choice by keyword (case-insensitive). Returns nil if absent.
func (o *Option) Choice(keyword string) *Choice {
	for i := range o.Choices {
		if strings.EqualFold(o.Choices[i].Keyword, keyword) {
			return &o.Choices[i]
		}
	}
	return nil
}

// DefaultChoice returns the default choice, or nil if none is defined.
func (o *Option) DefaultChoice() *Choice {
	if o.Default == "" {
		return nil
	}
	return o.Choice(o.Default)
}

// PPD is the parsed model of a printer description.
type PPD struct {
	NickName          string
	ModelName         string
	Manufacturer      string
	DefaultResolution string // e.g. "600dpi"
	LanguageLevel     string
	ColorDevice       bool
	CUPSFilters       []string // raw *cupsFilter / *cupsFilter2 lines

	// Foomatic encoding of the Ghostscript command line.
	FoomaticRIPCommandLine string
	// FoomaticSettings maps optionKeyword -> choiceKeyword -> code snippet.
	FoomaticSettings map[string]map[string]string

	Options     map[string]*Option // keyed by option keyword
	optionOrder []string
}

// Option returns the named option (case-insensitive) or nil.
func (p *PPD) Option(keyword string) *Option {
	if o, ok := p.Options[keyword]; ok {
		return o
	}
	for k, o := range p.Options {
		if strings.EqualFold(k, keyword) {
			return o
		}
	}
	return nil
}

// OrderedOptions returns options in the order they first appeared in the file.
func (p *PPD) OrderedOptions() []*Option {
	out := make([]*Option, 0, len(p.optionOrder))
	for _, k := range p.optionOrder {
		if o := p.Options[k]; o != nil {
			out = append(out, o)
		}
	}
	return out
}

// ParseFile reads and parses a PPD from disk.
func ParseFile(path string) (*PPD, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Parse reads and parses a PPD from r.
func Parse(r io.Reader) (*PPD, error) {
	p := &PPD{
		FoomaticSettings: map[string]map[string]string{},
		Options:          map[string]*Option{},
	}

	stmts, err := scanStatements(r)
	if err != nil {
		return nil, err
	}

	var openOption string // keyword of the option we are currently inside (OpenUI)

	for _, s := range stmts {
		switch {
		case s.main == "NickName":
			p.NickName = unquote(s.value)
		case s.main == "ModelName":
			p.ModelName = unquote(s.value)
		case s.main == "Manufacturer":
			p.Manufacturer = unquote(s.value)
		case s.main == "DefaultResolution":
			p.DefaultResolution = strings.TrimSpace(s.value)
		case s.main == "LanguageLevel":
			p.LanguageLevel = unquote(s.value)
		case s.main == "ColorDevice":
			p.ColorDevice = strings.EqualFold(strings.TrimSpace(s.value), "true")
		case s.main == "cupsFilter" || s.main == "cupsFilter2":
			p.CUPSFilters = append(p.CUPSFilters, unquote(s.value))
		case s.main == "FoomaticRIPCommandLine":
			p.FoomaticRIPCommandLine = unquote(s.value)
		case s.main == "FoomaticRIPOptionSetting":
			// Form: *FoomaticRIPOptionSetting Option=Choice: "code"
			opt, choice := splitEq(s.option)
			if opt != "" && choice != "" {
				if p.FoomaticSettings[opt] == nil {
					p.FoomaticSettings[opt] = map[string]string{}
				}
				p.FoomaticSettings[opt][choice] = unquote(s.value)
			}
		case s.main == "OpenUI":
			// *OpenUI *PageSize/Media Size: PickOne
			kw := strings.TrimPrefix(s.option, "*")
			o := p.ensureOption(kw)
			o.Translation = s.translation
			o.Type = strings.TrimSpace(s.value)
			openOption = kw
		case s.main == "CloseUI":
			openOption = ""
		case strings.HasPrefix(s.main, "Default"):
			// *DefaultPageSize: Letter  -> sets default for option "PageSize"
			kw := strings.TrimPrefix(s.main, "Default")
			if o := p.Options[kw]; o != nil {
				o.Default = strings.TrimSpace(s.value)
			} else {
				// Default may appear before OpenUI; stash it.
				o := p.ensureOption(kw)
				o.Default = strings.TrimSpace(s.value)
			}
		default:
			// A choice line for the currently-open option looks like:
			//   *PageSize A4/A4: "<PS code>"
			// main == option keyword, option == choice keyword.
			if openOption != "" && s.main == openOption && s.option != "" {
				o := p.ensureOption(openOption)
				o.Choices = append(o.Choices, Choice{
					Keyword:     s.option,
					Translation: s.translation,
					Code:        unquote(s.value),
				})
			}
		}
	}

	return p, nil
}

func (p *PPD) ensureOption(keyword string) *Option {
	if o, ok := p.Options[keyword]; ok {
		return o
	}
	o := &Option{Keyword: keyword, Order: len(p.optionOrder)}
	p.Options[keyword] = o
	p.optionOrder = append(p.optionOrder, keyword)
	return o
}

// statement is one logical PPD line: *main option/translation: value
type statement struct {
	main        string // keyword after leading '*', without '*'
	option      string // second token (choice keyword or *Option for OpenUI)
	translation string // text after '/' in the option token
	value       string // text after ':' (may be quoted, may span lines)
}

// scanStatements tokenizes a PPD into logical statements, joining the
// multi-line quoted values that PPD uses for embedded PostScript.
func scanStatements(r io.Reader) ([]statement, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var stmts []statement
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "*%") || trimmed == "*End" {
			continue // blank or comment
		}
		if !strings.HasPrefix(trimmed, "*") {
			continue
		}

		// If the value opens a quote that does not close on this line, keep
		// reading until we find the closing quote (or a line ending in *End).
		if openQuoteUnclosed(line) {
			var b strings.Builder
			b.WriteString(line)
			for sc.Scan() {
				next := sc.Text()
				b.WriteByte('\n')
				// A lone "*End" terminates the value; drop it.
				if strings.TrimSpace(next) == "*End" {
					break
				}
				b.WriteString(next)
				if strings.Contains(next, `"`) {
					break
				}
			}
			line = b.String()
		}

		if st, ok := parseStatement(line); ok {
			stmts = append(stmts, st)
		}
	}
	return stmts, sc.Err()
}

// openQuoteUnclosed reports whether line contains a value-opening '"' with no
// matching close on the same line.
func openQuoteUnclosed(line string) bool {
	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		return false
	}
	rest := line[colon+1:]
	first := strings.IndexByte(rest, '"')
	if first < 0 {
		return false
	}
	return strings.IndexByte(rest[first+1:], '"') < 0
}

// parseStatement parses a single (possibly multi-line-joined) PPD statement.
func parseStatement(line string) (statement, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "*") {
		return statement{}, false
	}
	line = line[1:] // drop leading '*'

	colon := strings.IndexByte(line, ':')
	var head, value string
	if colon >= 0 {
		head = strings.TrimSpace(line[:colon])
		value = strings.TrimSpace(line[colon+1:])
	} else {
		head = strings.TrimSpace(line)
	}

	// head is "MainKeyword" or "MainKeyword OptionToken"
	var st statement
	if sp := strings.IndexAny(head, " \t"); sp >= 0 {
		st.main = head[:sp]
		optTok := strings.TrimSpace(head[sp+1:])
		// Split translation off the option token at '/'.
		if slash := strings.IndexByte(optTok, '/'); slash >= 0 {
			st.option = optTok[:slash]
			st.translation = optTok[slash+1:]
		} else {
			st.option = optTok
		}
	} else {
		st.main = head
	}
	st.value = value
	return st, st.main != ""
}

// unquote strips a single layer of surrounding double quotes and trims space.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' {
		if end := strings.LastIndexByte(s, '"'); end > 0 {
			return s[1:end]
		}
	}
	return s
}

// splitEq splits "Option=Choice" into its parts.
func splitEq(s string) (string, string) {
	if i := strings.IndexByte(s, '='); i >= 0 {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
	}
	return strings.TrimSpace(s), ""
}
