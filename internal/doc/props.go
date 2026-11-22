package doc

import "sort"

// Properties are kept apart from the text, as lists of changes to a
// default (sprms, [MS-DOC] 2.6): for a paragraph and for a run of
// characters, in pages of the WordDocument stream that a table of the
// table stream points to (bin tables, [MS-DOC] 2.8.5); for a style, in
// the style sheet.

// eachSprm calls fn for each change of a list: its code and its operand.
// It stops where fn returns false, or the list breaks off.
func eachSprm(grpprl []byte, fn func(op uint16, arg []byte) bool) {
	for len(grpprl) >= 2 {
		op := le.Uint16(grpprl)
		grpprl = grpprl[2:]
		// Three bits of the code say how long the operand is.
		n := 0
		switch op >> 13 {
		case 0, 1:
			n = 1
		case 2, 4, 5:
			n = 2
		case 3:
			n = 4
		case 7:
			n = 3
		case 6:
			// A length comes first: in a byte, or for the definition of a
			// table's cells in two, counting itself less one.
			switch {
			case op == sprmTDefTable || op == 0xD609:
				if len(grpprl) < 2 {
					return
				}
				n = int(le.Uint16(grpprl)) + 1
			case len(grpprl) < 1:
				return
			case op == 0xC615 && grpprl[0] == 255:
				// A change of tab stops too long for its length byte: so
				// many deleted, four bytes each, and so many added, three.
				if len(grpprl) < 2 {
					return
				}
				del := int(grpprl[1])
				if 2+4*del >= len(grpprl) {
					return
				}
				n = 2 + 4*del + 1 + 3*int(grpprl[2+4*del])
			default:
				n = int(grpprl[0]) + 1
			}
		}
		if n > len(grpprl) {
			return
		}
		if !fn(op, grpprl[:n]) {
			return
		}
		grpprl = grpprl[n:]
	}
}

// The changes that are read.
const (
	sprmPIlvl           = 0x260A
	sprmPIlfo           = 0x460B
	sprmPFInTable       = 0x2416
	sprmPFTtp           = 0x2417
	sprmPOutLvl         = 0x2640
	sprmPItap           = 0x6649
	sprmPDtap           = 0x664A
	sprmPFInnerTableCel = 0x244B
	sprmPFInnerTtp      = 0x244C
	sprmPIstd           = 0x4600
	sprmPHugePapx       = 0x6646
	sprmPHugePapx2      = 0x6645

	sprmCFRMarkDel   = 0x0800
	sprmCFRMarkIns   = 0x0801
	sprmCFFldVanish  = 0x0802
	sprmCPicLocation = 0x6A03
	sprmCFData       = 0x0806
	sprmCFOle2       = 0x080A
	sprmCSymbol      = 0x6A09
	sprmCIstd        = 0x4A30
	sprmCFBold       = 0x0835
	sprmCFItalic     = 0x0836
	sprmCFStrike     = 0x0837
	sprmCFVanish     = 0x083C
	sprmCKul         = 0x2A3E
	sprmCIss         = 0x2A48
	sprmCRgFtc0      = 0x4A4F
	sprmCFDStrike    = 0x2A53
	sprmCFSpec       = 0x0855
	sprmCFObj        = 0x0856
	sprmCIbstRMark   = 0x4804
	sprmCDttmRMark   = 0x6805
	sprmCIbstRMarkDe = 0x4863
	sprmCDttmRMarkDe = 0x6864

	sprmTDefTable    = 0xD608
	sprmTTableHeader = 0x3404
)

// bins is a bin table: for runs of the stream, the page that holds their
// properties.
type bins struct {
	fcs []uint32 // one more than pages
	pns []uint32
	// The page and the run found last: the text is read in order, and
	// the next is most often the same or the one after.
	page, run int
}

// readBins reads a bin table (a PlcBteChpx or PlcBtePapx).
func readBins(b []byte) bins {
	n := (len(b) - 4) / 8
	if n <= 0 {
		return bins{}
	}
	t := bins{fcs: make([]uint32, n+1), pns: make([]uint32, n)}
	for i := range t.fcs {
		t.fcs[i] = le.Uint32(b[4*i:])
	}
	for i := range t.pns {
		t.pns[i] = le.Uint32(b[4*(n+1)+4*i:]) & 0x3FFFFF
	}
	return t
}

// fkp returns the page of properties that covers the stream offset fc,
// and the index of its run that does: nil where there is none.
func (t *bins) fkp(stream []byte, fc int64) (page []byte, run int) {
	if len(t.pns) == 0 {
		return nil, 0
	}
	i := t.page
	if !(i < len(t.pns) && int64(t.fcs[i]) <= fc && fc < int64(t.fcs[i+1])) {
		i = sort.Search(len(t.pns), func(i int) bool { return int64(t.fcs[i+1]) > fc })
		if i >= len(t.pns) || int64(t.fcs[i]) > fc {
			return nil, 0
		}
		t.page, t.run = i, 0
	}
	at := int64(t.pns[i]) * 512
	if at+512 > int64(len(stream)) {
		return nil, 0
	}
	page = stream[at : at+512]
	n := int(page[511])
	if n == 0 || 4*(n+1) > 511 {
		return nil, 0
	}
	j := t.run
	if !(j < n && int64(le.Uint32(page[4*j:])) <= fc && fc < int64(le.Uint32(page[4*j+4:]))) {
		j = sort.Search(n, func(j int) bool { return int64(le.Uint32(page[4*j+4:])) > fc })
		if j >= n || int64(le.Uint32(page[4*j:])) > fc {
			return nil, 0
		}
		t.run = j
	}
	return page, j
}

// papx returns the properties of the paragraph whose last character is at
// the stream offset fc: its style and its changes to it.
func (t *bins) papx(stream []byte, fc int64) (istd uint16, grpprl []byte) {
	page, j := t.fkp(stream, fc)
	if page == nil {
		return 0, nil
	}
	n := int(page[511])
	bx := 4*(n+1) + 13*j
	if bx >= 511 {
		return 0, nil
	}
	at := 2 * int(page[bx])
	if at == 0 || at+2 > 511 {
		return 0, nil
	}
	// A length in a byte counts pairs of bytes less one; where it is
	// zero, the length is in the next byte, counting pairs.
	size := 2*int(page[at]) - 1
	at++
	if size < 0 {
		size = 2 * int(page[at])
		at++
	}
	if size < 2 || at+size > 511 {
		return 0, nil
	}
	return le.Uint16(page[at:]), page[at+2 : at+size]
}

// chpx returns the changes of the run of characters that holds the
// stream offset fc, and the offset where the run ends; end is fc+1 where
// the offset is in no run.
func (t *bins) chpx(stream []byte, fc int64) (grpprl []byte, end int64) {
	page, j := t.fkp(stream, fc)
	if page == nil {
		return nil, fc + 1
	}
	n := int(page[511])
	end = int64(le.Uint32(page[4*j+4:]))
	if end <= fc {
		end = fc + 1
	}
	b := 4*(n+1) + j
	if b >= 511 {
		return nil, end
	}
	at := 2 * int(page[b])
	if at == 0 || at >= 511 {
		return nil, end
	}
	size := int(page[at])
	if at+1+size > 511 {
		return nil, end
	}
	return page[at+1 : at+1+size], end
}
