package xls

import (
	"github.com/AOShei/go-fast-msbin/internal/codepage"
	"math"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/chart"
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/grid"
	"github.com/AOShei/go-fast-msbin/internal/numfmt"
	"github.com/AOShei/go-fast-msbin/internal/ptg"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// A chart is a substream of records of its own ([MS-XLS] 2.1.7.20.1): a
// chart sheet's, or inside a worksheet's after the object that places it.
// What is read is what go-fast-ooxml reads of a chart: its type, the
// titles of the chart and its axes, the names of its series, and the
// categories and values the chart keeps from when it was last drawn.

// Records of a chart that are read.
const (
	recChSeries     = 0x1003
	recChSeriesText = 0x100D
	recChFormat     = 0x1014 // a group of series drawn as one type
	recChBar        = 0x1017
	recChLine       = 0x1018
	recChPie        = 0x1019
	recChArea       = 0x101A
	recChScatter    = 0x101B
	recChChartLine  = 0x101C
	recChText       = 0x1025
	recChObjectLink = 0x1027
	recChBegin      = 0x1033
	recChEnd        = 0x1034
	recCh3D         = 0x103A
	recChRadar      = 0x103E
	recChSurface    = 0x103F
	recChRadarArea  = 0x1040
	recChAuxTrend   = 0x104B // the series is a trend line of another
	recChAI         = 0x1051 // what a series takes its name, values or categories from
	recChAuxErrBar  = 0x105B // the series is the error bars of another
	recChBopPop     = 0x1061 // a pie with a second plot of some of its slices
	recChSIIndex    = 0x1065 // which of a series' data the values after it are
)

// The records of the datasheet of a Microsoft Graph chart, and the
// bounds on it.
const (
	recGraphNumber = 0x0003
	recGraphLabel  = 0x0004
	maxGraphRows   = 4000
	maxGraphCols   = 4000
)

// cached is a value a chart keeps of the cells it was drawn from.
type cached struct {
	num   float64
	text  string
	isNum bool
	set   bool
}

// chartSeries is a series as its records give it.
type chartSeries struct {
	name                 string
	valFormat, catFormat uint16 // number formats, by number
	aux                  bool   // a trend line or error bars: not a series of data
	// The cells its values, its categories and its bubble sizes are
	// taken from, where the series says so with one range.
	refs [4]cellRange
}

// cellRange is a range of cells of one sheet; ok says there is one.
type cellRange struct {
	sheet int // the sheet's place among the BoundSheet records
	at    grid.Rect
	ok    bool
}

// rangeOf reads a formula that is one reference to a cell or a range, on
// this sheet or another of the workbook ([MS-XLS] 2.5.198: PtgRef,
// PtgArea, PtgRef3d, PtgArea3d). A formula of any other kind gives no
// range.
func (sh *sheet) rangeOf(rgce []byte) cellRange {
	if len(rgce) == 0 {
		return cellRange{}
	}
	r := cellRange{sheet: sh.index}
	body := rgce[1:]
	switch rgce[0] & 0x1F {
	case 0x1A, 0x1B: // on the sheets an entry of the table of sheet references names
		if len(body) < 2 {
			return cellRange{}
		}
		x := int(le.Uint16(body))
		f := &sh.b.formulas
		if x >= len(f.XTI) {
			return cellRange{}
		}
		xti := f.XTI[x]
		if xti.Link < 0 || int(xti.Link) >= len(f.Links) || f.Links[xti.Link].Kind != ptg.LinkSelf || xti.First != xti.Last || xti.First < 0 {
			return cellRange{}
		}
		r.sheet = int(xti.First)
		body = body[2:]
	case 0x04, 0x05:
	default:
		return cellRange{}
	}
	switch rgce[0] & 0x1F {
	case 0x04, 0x1A:
		if len(body) != 4 {
			return cellRange{}
		}
		row, col := int32(le.Uint16(body)), int32(le.Uint16(body[2:])&0xFF)
		r.at = grid.Rect{R0: row, R1: row, C0: col, C1: col}
	default:
		if len(body) != 8 {
			return cellRange{}
		}
		r.at = grid.Rect{R0: int32(le.Uint16(body)), R1: int32(le.Uint16(body[2:])), C0: int32(le.Uint16(body[4:]) & 0xFF), C1: int32(le.Uint16(body[6:]) & 0xFF)}
	}
	r.ok = r.at.R0 <= r.at.R1 && r.at.C0 <= r.at.C1
	return r
}

// readChart reads the chart whose BOF record the cursor is on, to its EOF
// record, and returns it; nil where the records break off.
func (sh *sheet) readChart() *chart.Chart {
	c := &sh.c
	var out chart.Chart
	var series []chartSeries
	// The values kept, by kind (1 values, 2 categories, 3 bubble sizes),
	// then by series, then by point.
	var cache [4]map[uint16][]cached
	var datasheet map[[2]uint16]cached // the cells of a Microsoft Graph chart's datasheet
	total := 0
	index := 0 // the kind the values being read are of
	// The records that the groups being read belong to.
	var owners []uint16
	last := uint16(0)
	inSeries := -1 // the series whose group is being read
	// A text being read: its string, and what it is the title of.
	var text string
	link := -1
	textDepth := -1
	kind, threeD := "", false
	formatDepth := -1
	var scratch []byte
	depth := 0
	for n := 0; ; n++ {
		typ, ok := c.record(c.next)
		if !ok {
			sh.cutOff = true
			return nil
		}
		if n&4095 == 4095 && sh.ctx.Err() != nil {
			sh.cancelled = true
			return nil
		}
		b := c.b
		switch typ {
		case recGraphNumber, recGraphLabel:
			// A cell of the datasheet of a Microsoft Graph chart, which
			// keeps its data in a sheet of its own ([MS-OGRAPH]): its row
			// and column, three bytes of format, and a number, or a text
			// with its length and whether it is in 16 bits.
			if len(b) < 8 || len(datasheet) >= warn.MaxChartTotal {
				break
			}
			at := [2]uint16{le.Uint16(b), le.Uint16(b[2:])}
			if at[0] > maxGraphRows || at[1] > maxGraphCols {
				break
			}
			if datasheet == nil {
				datasheet = map[[2]uint16]cached{}
			}
			if typ == recGraphNumber {
				if len(b) >= 15 {
					datasheet[at] = cached{num: math.Float64frombits(le.Uint64(b[7:])), isNum: true, set: true}
				}
				break
			}
			if len(b) < 9 {
				break
			}
			raw := b[9:]
			var text string
			if b[8]&1 != 0 {
				raw = raw[:min(2*int(b[7]), len(raw)&^1)]
				u := make([]uint16, len(raw)/2)
				for i := range u {
					u[i] = le.Uint16(raw[2*i:])
				}
				text = string(utf16.Decode(u))
			} else {
				text, _ = codepage.Decode(sh.b.cp, raw[:min(int(b[7]), len(raw))])
			}
			text, removed := clean.String([]byte(text), clean.Prose)
			sh.removed += removed
			datasheet[at] = cached{text: text, set: true}
		case recBOF:
			depth++
			continue
		case recEOF:
			if depth > 0 {
				depth--
				continue
			}
		}
		if depth > 0 {
			continue
		}
		if typ == recEOF {
			break
		}
		switch typ {
		case recChBegin:
			owners = append(owners, last)
			switch last {
			case recChText:
				if textDepth < 0 {
					textDepth, text, link = len(owners), "", -1
				}
			case recChFormat:
				formatDepth, kind, threeD = len(owners), "", false
			}
		case recChEnd:
			if len(owners) == textDepth {
				title := sh.lost.Label(text)
				switch link {
				case 1:
					out.Title = title
				case 2:
					out.AxisY = title
				case 3:
					out.AxisX = title
				}
				textDepth = -1
			}
			if len(owners) == formatDepth {
				if kind != "" {
					if threeD {
						kind += "3d"
					}
					out.Kind(kind)
				}
				formatDepth = -1
			}
			if len(owners) > 0 {
				if owners[len(owners)-1] == recChSeries {
					inSeries = -1
				}
				owners = owners[:len(owners)-1]
			}
		case recChSeries:
			if len(series) >= chart.MaxSeries {
				sh.lost.Add(warn.CapChartSeries, 1)
				inSeries = -1
			} else {
				series = append(series, chartSeries{})
				inSeries = len(series) - 1
			}
		case recChAuxTrend, recChAuxErrBar:
			if inSeries >= 0 {
				series[inSeries].aux = true
			}
		case recChAI:
			// Which of the series' data it is for, and the number format
			// of the values ([MS-XLS] 2.4.29).
			if inSeries >= 0 && len(b) >= 8 && len(owners) > 0 && owners[len(owners)-1] == recChSeries {
				switch b[0] {
				case 1:
					series[inSeries].valFormat = le.Uint16(b[4:])
				case 2:
					series[inSeries].catFormat = le.Uint16(b[4:])
				}
				// A reference to cells of the workbook: the formula
				// follows its length.
				if cce := int(le.Uint16(b[6:])); b[0] >= 1 && b[0] <= 3 && b[1] == 2 && cce <= len(b)-8 {
					series[inSeries].refs[b[0]] = sh.rangeOf(b[8 : 8+cce])
				}
			}
		case recChSeriesText:
			// Two bytes not used, then a string with its length in a byte.
			if len(b) < 4 {
				break
			}
			c.b = b[2:]
			raw, _ := c.shortStr(scratch[:0])
			scratch = raw
			s, removed := clean.String(raw, clean.Prose)
			sh.removed += removed
			switch {
			case textDepth >= 0:
				text = s
			case inSeries >= 0 && len(owners) > 0 && owners[len(owners)-1] == recChSeries:
				series[inSeries].name = sh.lost.Label(s)
			}
		case recChObjectLink:
			if textDepth >= 0 && len(b) >= 2 {
				link = int(le.Uint16(b))
			}
		case recChBar:
			kind = "bar"
		case recChLine:
			kind = "line"
		case recChArea:
			kind = "area"
		case recChRadar, recChRadarArea:
			kind = "radar"
		case recChSurface:
			kind = "surface"
		case recChBopPop:
			kind = "ofpie"
		case recChPie:
			// A pie with a hole is a doughnut.
			kind = "pie"
			if len(b) >= 4 && le.Uint16(b[2:]) != 0 {
				kind = "doughnut"
			}
		case recChScatter:
			kind = "scatter"
			if len(b) >= 6 && le.Uint16(b[4:])&1 != 0 {
				kind = "bubble"
			}
		case recCh3D:
			threeD = formatDepth >= 0
		case recChChartLine:
			// Lines from the highest to the lowest value of each category
			// make a line chart a stock chart.
			if formatDepth >= 0 && kind == "line" && len(b) >= 2 && le.Uint16(b) == 1 {
				kind = "stock"
			}
		case recChSIIndex:
			index = 0
			if len(b) >= 2 && le.Uint16(b) >= 1 && le.Uint16(b) <= 3 {
				index = int(le.Uint16(b))
			}
		case recNumber, recLabel, recBoolErr, recRK:
			// A value the chart keeps: its row is the point, its column
			// the series.
			if index == 0 || len(b) < 6 {
				break
			}
			point, col := int(le.Uint16(b)), le.Uint16(b[2:])
			if int(col) >= chart.MaxSeries || point >= chart.MaxPoints {
				sh.lost.Add(warn.CapChartPoints, 1)
				break
			}
			var v cached
			switch {
			case typ == recNumber && len(b) >= 14:
				v = cached{num: math.Float64frombits(le.Uint64(b[6:])), isNum: true, set: true}
			case typ == recRK && len(b) >= 10:
				v = cached{num: rk(le.Uint32(b[6:])), isNum: true, set: true}
			case typ == recLabel:
				c.b = b[6:]
				raw, _ := c.str(scratch[:0])
				scratch = raw
				s, removed := clean.String(raw, clean.Prose)
				sh.removed += removed
				v = cached{text: s, set: true}
			case typ == recBoolErr && len(b) >= 8:
				v = cached{text: textFalse, set: true}
				switch {
				case b[7] != 0:
					v.text = errorText(b[6])
				case b[6] != 0:
					v.text = textTrue
				}
			}
			if !v.set {
				break
			}
			if cache[index] == nil {
				cache[index] = map[uint16][]cached{}
			}
			points := cache[index][col]
			if grow := point + 1 - len(points); grow > 0 {
				if total+grow > warn.MaxChartTotal {
					sh.lost.Add(warn.CapChartTotal, 1)
					break
				}
				total += grow
				points = append(points, make([]cached, grow)...)
			}
			points[point] = v
			cache[index][col] = points
		}
		if typ != recChBegin {
			last = typ
		}
	}

	// The series, with what the chart keeps of each. The column of a
	// series among the values kept is its place among the series.
	show := func(points []cached, format uint16) []string {
		if len(points) == 0 {
			return nil
		}
		f := numfmt.General()
		if code, ok := sh.b.codes[int(format)]; ok {
			f = numfmt.Parse(code)
		} else if format != 0 {
			f = numfmt.Parse(numfmt.Builtin(int(format)))
		}
		out := make([]string, len(points))
		for i, p := range points {
			switch {
			case !p.set:
			case p.isNum:
				sh.out, _ = f.Number(sh.out[:0], p.num, sh.b.date1904)
				out[i] = strings.TrimSpace(string(sh.out))
			default:
				out[i] = p.text
			}
		}
		return out
	}
	// Where a series says which cells its data is, the cells are what the
	// chart shows, as they show it; what the chart keeps of them is used
	// where it does not say, or the cells are not this workbook's.
	found := sh.lookup(series)
	data := func(i, kind int, format uint16) []string {
		s := &series[i]
		if r := s.refs[kind]; r.ok && found != nil {
			// The cells in order: down a column, or along a row; of a
			// range of several columns and rows the last column, which
			// holds the innermost of several levels of categories.
			at := r.at
			if at.R1 > at.R0 && at.C1 > at.C0 {
				at.C0 = at.C1
			}
			n := int(at.R1-at.R0+1) * int(at.C1-at.C0+1)
			if n > chart.MaxPoints {
				sh.lost.Add(warn.CapChartPoints, int64(n-chart.MaxPoints))
				n = chart.MaxPoints
			}
			vals := make([]string, 0, n)
			for row := at.R0; row <= at.R1 && len(vals) < n; row++ {
				for col := at.C0; col <= at.C1 && len(vals) < n; col++ {
					vals = append(vals, found[cellKey{r.sheet, pos{row, col}}])
				}
			}
			return vals
		}
		points := cache[kind][uint16(i)]
		if points == nil && kind == 2 {
			points = cache[2][0] // the series share the categories of the first
		}
		return show(points, format)
	}
	if len(datasheet) > 0 {
		// A Microsoft Graph chart: its datasheet is its data. The first
		// row names what runs across, the first column what runs down;
		// each row below the first is given as a series.
		rows, cols := map[uint16]bool{}, map[uint16]bool{}
		for at := range datasheet {
			if at[0] > 0 {
				rows[at[0]] = true
			}
			if at[1] > 0 {
				cols[at[1]] = true
			}
		}
		order := func(set map[uint16]bool) []uint16 {
			out := make([]uint16, 0, len(set))
			for v := range set {
				out = append(out, v)
			}
			sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
			return out
		}
		across := order(cols)
		if len(across) > chart.MaxPoints {
			sh.lost.Add(warn.CapChartPoints, int64(len(across)-chart.MaxPoints))
			across = across[:chart.MaxPoints]
		}
		cell := func(r, c uint16) string {
			if v := show([]cached{datasheet[[2]uint16{r, c}]}, 0); len(v) == 1 {
				return v[0]
			}
			return ""
		}
		cats := make([]string, len(across))
		for i, c := range across {
			cats[i] = cell(0, c)
		}
		// No more rows than keep the table within what a chart may hold.
		down := order(rows)
		if most := warn.MaxChartTotal / max(len(across), 1); len(down) > most {
			sh.lost.Add(warn.CapChartTotal, 1)
			down = down[:most]
		}
		for _, r := range down {
			vals := make([]string, len(across))
			for i, c := range across {
				vals[i] = cell(r, c)
			}
			out.Series = append(out.Series, chart.Series{Name: cell(r, 0), Cats: cats, Vals: [][]string{vals}})
		}
		return &out
	}
	for i, s := range series {
		if s.aux {
			continue
		}
		ser := chart.Series{Name: s.name}
		ser.Cats = data(i, 2, s.catFormat)
		if vals := data(i, 1, s.valFormat); vals != nil {
			ser.Vals = append(ser.Vals, vals)
		} else {
			ser.Vals = append(ser.Vals, []string{})
		}
		if sizes := data(i, 3, s.valFormat); sizes != nil {
			ser.Vals = append(ser.Vals, sizes)
		}
		out.Series = append(out.Series, ser)
	}
	return &out
}

// chartBlocks adds a chart's blocks at the given cell, or with no place
// for a chart that is a sheet of its own.
func (sh *sheet) chartBlocks(ch *chart.Chart, row, col int32, placed, hidden bool) {
	if ch == nil || sh.shapesFull {
		return
	}
	if sh.charts >= warn.MaxCharts {
		sh.lost.Add(warn.CapCharts, 1)
		return
	}
	sh.charts++
	blocks, _, cut := ch.Blocks(sh.room, "")
	for _, b := range blocks {
		if hidden {
			b.Display = gofastcore.DisplayHidden
		}
		if placed {
			b.Loc.Range = grid.CellName(row, col)
		}
		sh.floating = append(sh.floating, grid.Placed{Row: row, Col: col, Block: b})
	}
	if cut {
		sh.shapesFull = true
		if sh.g.Cut == "" {
			sh.g.Cut = "the limit on the amount of text was reached"
		}
	}
}

// cellKey names a cell of the workbook: its sheet's place among the
// BoundSheet records, and its place on the sheet.
type cellKey struct {
	sheet int
	at    pos
}

// maxLookup bounds the bytes of sheets' records read over again for the
// charts of one sheet: a chart's data is looked up in the sheets it
// names, each read once for each chart.
const maxLookup = warn.MaxChartLookup

// lookup reads what the cells that the series of a chart take their data
// from show. It returns nil where no series names cells, or the bound on
// reading sheets over again was reached.
func (sh *sheet) lookup(series []chartSeries) map[cellKey]string {
	// The ranges wanted of each sheet.
	want := map[int][]grid.Rect{}
	for i := range series {
		for k := 1; k <= 3; k++ {
			if r := series[i].refs[k]; r.ok && !series[i].aux {
				want[r.sheet] = append(want[r.sheet], r.at)
			}
		}
	}
	if len(want) == 0 {
		return nil
	}
	found := map[cellKey]string{}
	for index, rects := range want {
		if index < 0 || index >= len(sh.b.places) {
			continue
		}
		place := sh.b.places[index]
		if sh.lookedUp += int64(place.size); sh.lookedUp > maxLookup {
			sh.lost.Add(warn.CapChartLookup, 1)
			return nil
		}
		// The sheet is read as a sheet is, and only the cells of the
		// ranges are kept.
		other := &sheet{b: sh.b, ctx: sh.ctx, index: index, unread: map[string]int{}, rowAt: -1, last: pos{-1, -1}}
		other.c.data, other.c.cp = sh.b.data, sh.b.cp
		other.only = rects
		other.found = map[pos]string{}
		other.read(place.pos)
		for at, text := range other.found {
			found[cellKey{index, at}] = text
		}
	}
	return found
}
