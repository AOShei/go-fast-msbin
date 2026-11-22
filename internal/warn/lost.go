package warn

import (
	"strconv"
	"strings"
	"sync"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/clean"
)

// Cap is a fixed bound of this library that can leave content out. Each
// is said once per part it left something out of, as limit.exceeded with
// severity error, with how much it left out; a bound that only bounds
// work loses nothing and is not here.
type Cap uint8

const (
	// Shared by the three formats.
	CapProjects Cap = iota
	CapProjectSize
	CapModules
	CapPropertyName
	CapPropertyValue
	CapCustomProps
	CapProperties
	CapObjects
	CapObjectDepth
	CapAddress
	CapName
	// Excel.
	CapSheets
	CapSheetCols
	CapComments
	CapLinks
	CapMerges
	CapCellStyles
	CapNumberFormats
	CapFormula
	CapNames
	CapExternalLinks
	CapExternalNames
	CapExternalSheets
	CapSheetRefs
	CapSharedFormulas
	CapChoiceLists
	CapChoiceRanges
	CapChartSeries
	CapChartPoints
	CapChartTitle
	CapChartTotal
	CapCharts
	CapShapes
	CapPictures
	CapChartLookup
	CapPivotTables
	numCaps
)

type capInfo struct {
	what      string // what was left out and how, a clause
	one, many string // the unit counted
}

func itoa(n int) string { return strconv.Itoa(n) }

var caps = [numCaps]capInfo{
	CapProjects:      {"VBA projects past " + itoa(MaxProjects) + " were not read", "project", "projects"},
	CapProjectSize:   {"VBA projects with a stream larger than " + itoa(MaxProjectSize>>20) + " MiB were not read", "project", "projects"},
	CapModules:       {"modules of a VBA project past " + itoa(MaxModules) + " were not read", "module", "modules"},
	CapPropertyName:  {"names of custom document properties longer than " + itoa(MaxPropertyName) + " bytes were cut there", "name", "names"},
	CapPropertyValue: {"document property values longer than " + itoa(MaxPropertyValue) + " bytes were cut there", "value", "values"},
	CapCustomProps:   {"custom document properties past " + itoa(MaxCustomProps) + " were not read", "property", "properties"},
	CapProperties:    {"properties of a property set past " + itoa(MaxProperties) + " were not read", "property", "properties"},
	CapObjects:       {"embedded objects past " + itoa(MaxObjects) + " were not listed", "object", "objects"},
	CapObjectDepth:   {"embedded objects whose storages nest more than " + itoa(MaxObjectDepth) + " deep were listed and not handed on", "object", "objects"},
	CapAddress:       {"addresses longer than " + itoa(MaxAddress) + " bytes were cut there", "address", "addresses"},
	CapName:          {"names longer than " + itoa(MaxName) + " bytes were cut there", "name", "names"},

	CapSheets:         {"sheets past " + itoa(MaxSheets) + " were not read", "sheet", "sheets"},
	CapSheetCols:      {"cells past column " + itoa(MaxCols) + " of the sheet were not read", "cell", "cells"},
	CapComments:       {"comments of a sheet past " + itoa(MaxComments) + " were not read", "comment", "comments"},
	CapLinks:          {"hyperlinks of a sheet past " + itoa(MaxLinks) + " were not read", "hyperlink", "hyperlinks"},
	CapMerges:         {"merged ranges of a sheet past " + itoa(MaxMerges) + " were not read; their cells are given unmerged", "range", "ranges"},
	CapCellStyles:     {"cell formats and fonts of the workbook past " + itoa(MaxCellStyles) + " were not read; cells that use them show their values unformatted", "format", "formats"},
	CapNumberFormats:  {"number formats of the workbook past " + itoa(MaxNumberFormats) + " were not read; cells that use them show their values unformatted", "format", "formats"},
	CapFormula:        {"formulas longer than " + itoa(MaxFormula) + " bytes were not read back", "formula", "formulas"},
	CapNames:          {"defined names past " + itoa(MaxNames) + " were not read", "name", "names"},
	CapExternalLinks:  {"links to other workbooks past " + itoa(MaxExternalLinks) + " were not read; formulas that use them are not read back", "link", "links"},
	CapExternalNames:  {"names of a linked workbook past " + itoa(MaxExternalNames) + " were not read; formulas that use them are not read back", "name", "names"},
	CapExternalSheets: {"sheets of a linked workbook past " + itoa(MaxExternalNames) + " were not read; formulas that use them are not read back", "sheet", "sheets"},
	CapSheetRefs:      {"references of the workbook's formulas to other sheets past " + itoa(MaxSheetRefs) + " were not read; formulas that use them are not read back", "reference", "references"},
	CapChoiceLists:    {"drop-down lists of a sheet past " + itoa(MaxChoiceLists) + " were not read", "list", "lists"},
	CapChoiceRanges:   {"ranges of a sheet's drop-down lists past " + itoa(MaxChoiceRanges) + " were not read", "range", "ranges"},
	CapChartSeries:    {"chart series past " + itoa(MaxChartSeries) + " were not read", "series", "series"},
	CapChartPoints:    {"chart points at an index past " + itoa(MaxChartPoints) + " were not read", "point", "points"},
	CapChartTitle:     {"chart and series titles longer than " + itoa(MaxChartTitle) + " bytes were cut there", "title", "titles"},
	CapChartTotal:     {"charts holding more than " + itoa(MaxChartTotal) + " points were cut there", "chart", "charts"},
	CapCharts:         {"charts of a sheet past " + itoa(MaxCharts) + " were not read", "chart", "charts"},
	CapShapes:         {"shapes of a sheet with text past " + itoa(MaxShapes) + " were not read", "shape", "shapes"},
	CapPictures:       {"pictures of the document past " + itoa(MaxPictures) + " were not read; shapes that show them are given with no picture", "picture", "pictures"},
	CapChartLookup:    {"charts whose data is in sheets of more than " + itoa(MaxChartLookup>>20) + " MiB in all were given with what the charts keep of it, not with what the cells show", "chart", "charts"},
	CapPivotTables:    {"pivot tables of a sheet past " + itoa(MaxPivotTables) + " were not read as tables; their cells are given as cells of the sheet", "table", "tables"},
	CapSharedFormulas: {"shared formulas of a sheet past " + itoa(MaxSharedFormulas) + " were not kept; cells that use them give no formula", "formula", "formulas"},
}

// The bounds, where the code that keeps to them and the messages above
// both need them.
const (
	MaxProjects       = 16
	MaxProjectSize    = 1 << 28
	MaxModules        = 1 << 12
	MaxPropertyName   = clean.MaxLabel
	MaxPropertyValue  = 64 << 10
	MaxCustomProps    = 256
	MaxProperties     = 1 << 12
	MaxObjects        = 1 << 12
	MaxObjectDepth    = 64
	MaxAddress        = clean.MaxAddress
	MaxName           = clean.MaxLabel
	MaxSheets         = 1 << 16
	MaxCols           = 1 << 14
	MaxComments       = 1 << 18
	MaxLinks          = 1 << 18
	MaxMerges         = 1 << 18
	MaxCellStyles     = 1 << 17
	MaxNumberFormats  = 1 << 13
	MaxFormula        = 32 << 10
	MaxNames          = 1 << 14
	MaxExternalLinks  = 1 << 16
	MaxExternalNames  = 1 << 16
	MaxSheetRefs      = 1 << 16
	MaxSharedFormulas = 1 << 20
	MaxChoiceLists    = 1 << 14
	MaxChoiceRanges   = 1 << 18
	MaxChartSeries    = 1 << 10
	MaxChartPoints    = 1 << 20
	MaxChartTitle     = 4096
	MaxChartTotal     = 1 << 21
	MaxCharts         = 1 << 12
	MaxPivotTables    = 1 << 10
	MaxChartLookup    = 256 << 20
	MaxShapes         = 1 << 16
	MaxPictures       = 1 << 16
)

// Message is the text of the warning for n items a bound left out.
func (c Cap) Message(n int64) string {
	info := caps[c]
	unit := info.many
	if n == 1 {
		unit = info.one
	}
	return info.what + " (" + strconv.FormatInt(n, 10) + " " + unit + ")"
}

// Lost counts what the fixed bounds left out of one part, or of the
// document, bound by bound in the order met, so that the warnings come
// in an order that does not vary. The zero value is ready to use, and a
// nil *Lost counts nothing. Several goroutines may use it at once: what parts
// share (a chart, a master) may be read by any of them.
type Lost struct {
	mu    sync.Mutex
	n     [numCaps]int64
	bytes [numCaps]int64 // of a bound that cuts text: the bytes cut off
	order []Cap
}

// Add records that the bound c left n more items out.
func (l *Lost) Add(c Cap, n int64) {
	if l == nil || n <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n[c] == 0 {
		l.order = append(l.order, c)
	}
	l.n[c] = satAdd(l.n[c], n)
}

// AddCut records that the bound c cut n more texts short, leaving bytes
// bytes out.
func (l *Lost) AddCut(c Cap, n, bytes int64) {
	if l == nil || n <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n[c] == 0 {
		l.order = append(l.order, c)
	}
	l.n[c] = satAdd(l.n[c], n)
	l.bytes[c] = satAdd(l.bytes[c], max(bytes, 0))
}

// Merge adds what o counted to l, and leaves o as it is.
func (l *Lost) Merge(o *Lost) {
	if l == nil || o == nil || l == o {
		return
	}
	o.mu.Lock()
	order := append([]Cap(nil), o.order...)
	n, bytes := o.n, o.bytes
	o.mu.Unlock()
	for _, c := range order {
		l.AddCut(c, n[c], bytes[c])
	}
}

// Name is clean.Name, counting a name it cuts.
func (l *Lost) Name(s string) string {
	l.cut(s, MaxName, CapName)
	return clean.Name(s)
}

// Over returns how many bytes a name is longer than Name keeps; 0 if it
// is not.
func Over(s string) int64 {
	return max(int64(len(strings.TrimSpace(s))-MaxName), 0)
}

// NameOver counts a name over the bound by over bytes (Over), where it is
// put in the output.
func (l *Lost) NameOver(over int64) {
	if over > 0 {
		l.AddCut(CapName, 1, over)
	}
}

// Label is clean.Label, counting a label it cuts.
func (l *Lost) Label(s string) string {
	l.cut(s, MaxName, CapName)
	return clean.Label(s)
}

// Address is clean.Address, counting an address it cuts.
func (l *Lost) Address(s string) string {
	l.cut(s, MaxAddress, CapAddress)
	return clean.Address(s)
}

func (l *Lost) cut(s string, limit int, c Cap) {
	if n := len(strings.TrimSpace(s)); n > limit {
		l.AddCut(c, 1, int64(n-limit))
	}
}

// Any reports whether anything was counted.
func (l *Lost) Any() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.order) > 0
}

// Count returns what the bound c left out.
func (l *Lost) Count(c Cap) int64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n[c]
}

// Warnings returns the warnings, one per bound, in the order met.
func (l *Lost) Warnings() []gofastcore.Warning {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []gofastcore.Warning
	for _, c := range l.order {
		msg := c.Message(l.n[c])
		if b := l.bytes[c]; b > 0 {
			msg = strings.TrimSuffix(msg, ")") + ", " + strconv.FormatInt(b, 10) + " bytes left out)"
		}
		out = append(out, gofastcore.Warning{Code: gofastcore.WarnLimitExceeded, Severity: gofastcore.SeverityError, Message: msg})
	}
	return out
}

// Take returns what was counted, and forgets it.
func (l *Lost) Take() *Lost {
	out := &Lost{}
	if l == nil {
		return out
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out.n, out.bytes, out.order = l.n, l.bytes, l.order
	l.n, l.bytes, l.order = [numCaps]int64{}, [numCaps]int64{}, nil
	return out
}

// Say gives the warnings to p, one per bound, forgets what was counted,
// and reports whether there was any.
func (l *Lost) Say(p *gofastcore.Part) bool {
	ws := l.Take().Warnings()
	for _, w := range ws {
		p.Warn(w.Severity, w.Code, w.Message)
	}
	return len(ws) > 0
}

func satAdd(a, b int64) int64 {
	if a > 1<<62 || b > 1<<62 {
		return 1 << 62
	}
	return min(a+b, 1<<62)
}

// The nouns of the warning that parts could not be read to their end.
const (
	DamagedOne  = "stream could not be read to its end"
	DamagedMany = "streams could not be read to their end"
)

// Names gathers what a warning that could repeat is about (parts that
// could not be read, references that lead nowhere), so that it is said
// once, with how many there are and the first few. The zero value is
// ready to use. Several goroutines may use it at once.
type Names struct {
	mu    sync.Mutex
	n     int64
	first []string
	seen  map[string]bool
}

// shown is how many names a warning lists.
const shown = 8

// Add counts one more, named name.
func (l *Names) Add(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n = satAdd(l.n, 1)
	l.keep(name)
}

// AddOnce counts name if it has not been counted before.
func (l *Names) AddOnce(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string]bool{}
	}
	if l.seen[name] {
		return
	}
	l.seen[name] = true
	l.n = satAdd(l.n, 1)
	l.keep(name)
}

func (l *Names) keep(name string) {
	if len(l.first) >= shown {
		return
	}
	for _, f := range l.first {
		if f == name {
			return
		}
	}
	l.first = append(l.first, name)
}

// Len returns how many were counted.
func (l *Names) Len() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

// Message returns "N <noun> (a, b, ..., and K more)", with one or many as
// the noun, and forgets what was counted; "" if nothing was.
func (l *Names) Message(one, many string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n == 0 {
		return ""
	}
	noun := many
	if l.n == 1 {
		noun = one
	}
	msg := strconv.FormatInt(l.n, 10) + " " + noun + " (" + strings.Join(l.first, ", ")
	if more := l.n - int64(len(l.first)); more > 0 {
		if len(l.first) > 0 {
			msg += ", "
		}
		msg += "and " + strconv.FormatInt(more, 10) + " more"
	}
	l.n, l.first, l.seen = 0, nil, nil
	return msg + ")"
}
