// Package doc reads Word documents in the binary format of Word 97 to
// 2003 ([MS-DOC]): the WordDocument stream, which holds the text and the
// pages of properties, and the table stream beside it, which holds
// everything that says what the text is. It also reads the documents of
// Word 6 and 95, which are one stream (word6.go).
package doc

import (
	"encoding/binary"
	"errors"
)

var le = binary.LittleEndian

// fib is the file information block: what the file holds and where
// ([MS-DOC] 2.5.1).
type fib struct {
	nFib    uint16
	flags   uint16
	complex bool // the file was saved fast: properties are also on the pieces of its text
	table   string
	// The lengths of the stories, in characters: the main text, the
	// footnotes, the headers and footers, the comments, the endnotes, the
	// text boxes and the text boxes of headers.
	ccpText, ccpFtn, ccpHdd, ccpAtn, ccpEdn, ccpTxbx, ccpHdrTxbx int64
	// Where each structure of the table stream is, and how long.
	fc, lcb []uint32
}

// Flags of the FIB.
const (
	fibComplex   = 0x0004
	fibEncrypted = 0x0100
	fibWhichTbl  = 0x0200
)

// The structures of the table stream, by their place among
// the pairs of an offset and a length ([MS-DOC] 2.5.6, FibRgFcLcb97).
const (
	fcStshf          = 1
	fcPlcffndRef     = 2
	fcPlcffndTxt     = 3
	fcPlcfandRef     = 4
	fcPlcfandTxt     = 5
	fcPlcfSed        = 6
	fcPlcfHdd        = 11
	fcPlcfBteChpx    = 12
	fcPlcfBtePapx    = 13
	fcSttbfFfn       = 15
	fcPlcfFldMom     = 16
	fcPlcfFldHdr     = 17
	fcPlcfFldFtn     = 18
	fcPlcfFldAtn     = 19
	fcSttbfBkmk      = 21
	fcPlcfBkf        = 22
	fcPlcfBkl        = 23
	fcDop            = 31
	fcSttbfAssoc     = 32
	fcClx            = 33
	fcGrpXstAtnOwner = 36
	fcPlcSpaMom      = 40
	fcPlcSpaHdr      = 41
	fcPlcfendRef     = 46
	fcPlcfendTxt     = 47
	fcPlcfFldEdn     = 48
	fcDggInfo        = 50
	fcSttbfRMark     = 51
	fcPlcftxbxTxt    = 56
	fcPlcfFldTxbx    = 57
	fcPlcfHdrtxbxTxt = 58
	fcPlcfFldHdrTxbx = 59
	fcStwUser        = 60
	fcPlfLst         = 73
	fcPlfLfo         = 74
)

var errFIB = errors.New("the file information block is cut short")

// readFIB reads the file information block at the start of the
// WordDocument stream.
func readFIB(b []byte) (*fib, error) {
	if len(b) < 34 {
		return nil, errFIB
	}
	f := &fib{nFib: le.Uint16(b[2:]), flags: le.Uint16(b[10:]), table: "0Table"}
	f.complex = f.flags&fibComplex != 0
	if f.flags&fibWhichTbl != 0 {
		f.table = "1Table"
	}
	// After the 32 bytes of the base: a count and that many 16-bit
	// values, a count and that many 32-bit values, a count and that many
	// pairs of an offset and a length.
	at := 32
	csw := int(le.Uint16(b[at:]))
	at += 2 + 2*csw
	if at+2 > len(b) {
		return nil, errFIB
	}
	cslw := int(le.Uint16(b[at:]))
	lw := b[min(at+2, len(b)):]
	at += 2 + 4*cslw
	if at+2 > len(b) || len(lw) < 4*cslw {
		return nil, errFIB
	}
	long := func(i int) int64 {
		if i >= cslw {
			return 0
		}
		// A count of characters is not negative, and not more than the
		// file can hold.
		v := int64(int32(le.Uint32(lw[4*i:])))
		return max(v, 0)
	}
	f.ccpText, f.ccpFtn, f.ccpHdd, f.ccpAtn, f.ccpEdn, f.ccpTxbx, f.ccpHdrTxbx = long(3), long(4), long(5), long(7), long(8), long(9), long(10)
	pairs := int(le.Uint16(b[at:]))
	at += 2
	if pairs > (len(b)-at)/8 {
		pairs = (len(b) - at) / 8
	}
	f.fc, f.lcb = make([]uint32, pairs), make([]uint32, pairs)
	for i := 0; i < pairs; i++ {
		f.fc[i], f.lcb[i] = le.Uint32(b[at+8*i:]), le.Uint32(b[at+8*i+4:])
	}
	return f, nil
}

// part returns the bytes of a structure of the table stream, or nil where
// the file has none or says it lies outside the stream.
func (f *fib) part(table []byte, index int) []byte {
	if index >= len(f.fc) || f.lcb[index] == 0 {
		return nil
	}
	start, n := int64(f.fc[index]), int64(f.lcb[index])
	if start+n > int64(len(table)) {
		return nil
	}
	return table[start : start+n]
}
