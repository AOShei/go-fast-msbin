// Package officeart reads the drawing layer that Word, Excel and
// PowerPoint files of the binary formats share ([MS-ODRAW]): shapes with
// their properties and where they are anchored, and the pictures kept
// for them.
//
// A drawing is a tree of records. Each begins with eight bytes: a version
// and an instance in sixteen bits, a type, and the length of what
// follows. A record whose version is 0xF is a container: what follows is
// more records.
package officeart

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"io"
	"unicode/utf16"
)

var le = binary.LittleEndian

// Record types that are read.
const (
	DggContainer    = 0xF000
	BStoreContainer = 0xF001
	DgContainer     = 0xF002
	SpgrContainer   = 0xF003
	SpContainer     = 0xF004
	BSE             = 0xF007 // an entry of the store of pictures
	Sp              = 0xF00A // a shape: its number and flags
	OPT             = 0xF00B // a shape's properties
	ClientTextbox   = 0xF00D
	ChildAnchor     = 0xF00F
	ClientAnchor    = 0xF010
	ClientData      = 0xF011
	SecondaryOPT    = 0xF121
	TertiaryOPT     = 0xF122

	blipFirst = 0xF018
	blipLast  = 0xF117
	blipEMF   = 0xF01A
	blipWMF   = 0xF01B
	blipPICT  = 0xF01C
	blipJPEG  = 0xF01D
	blipPNG   = 0xF01E
	blipDIB   = 0xF01F
	blipTIFF  = 0xF029
	blipJPEG2 = 0xF02A // JPEG in the CMYK colour space
)

// MaxDepth bounds how deep containers are followed. Office nests groups
// of shapes a few deep.
const MaxDepth = 64

// Rec is one record.
type Rec struct {
	Ver  uint8
	Inst uint16
	Type uint16
	// Body is what follows the header. Where the record says it is longer
	// than the bytes there are, it is what there is: a drawing is often
	// cut into pieces between records, in the middle of a container.
	Body []byte
	// Short says the record says it is longer than the bytes there are.
	Short bool
}

// Container reports whether what follows the header is more records.
func (r Rec) Container() bool { return r.Ver == 0xF }

// Next reads the record at the start of b, and returns what follows it.
// ok is false where b holds no record.
func Next(b []byte) (r Rec, rest []byte, ok bool) {
	if len(b) < 8 {
		return Rec{}, nil, false
	}
	vi := le.Uint16(b)
	r = Rec{Ver: uint8(vi & 0xF), Inst: vi >> 4, Type: le.Uint16(b[2:])}
	n := uint64(le.Uint32(b[4:]))
	if n > uint64(len(b)-8) {
		n, r.Short = uint64(len(b)-8), true
	}
	r.Body = b[8 : 8+n]
	return r, b[8+n:], true
}

// Walk calls fn for every record of b, going into containers; depth is
// how many containers the record is inside. Where fn returns false for a
// container, what it holds is passed over.
func Walk(b []byte, fn func(r Rec, depth int) bool) {
	walk(b, 0, fn)
}

func walk(b []byte, depth int, fn func(r Rec, depth int) bool) {
	for {
		r, rest, ok := Next(b)
		if !ok {
			return
		}
		if fn(r, depth) && r.Container() && depth < MaxDepth {
			walk(r.Body, depth+1, fn)
		}
		b = rest
	}
}

// Property identifiers ([MS-ODRAW] 2.3).
const (
	PropTextID       = 0x0080 // lTxid: which text the shape holds
	PropBlip         = 0x0104 // pib: the picture a shape shows, by its number from 1
	PropBlipName     = 0x0105 // pibName: the name of the picture's file
	PropShapeName    = 0x0380 // wzName
	PropDescription  = 0x0381 // wzDescription: the alternative text
	PropHyperlink    = 0x0382 // pihlShape
	PropGroupBools   = 0x03BF // the group's booleans, fHidden among them
	PropTextBools    = 0x00BF
	PropUnicodeText  = 0x00C0 // gtextUNICODE: the text of a WordArt shape
	PropGeoTextBools = 0x00FF // fGtext among them
)

// Props are the properties of a shape: numbers, and for some a value of
// bytes besides.
type Props struct {
	values  map[uint16]uint32
	complex map[uint16][]byte
}

// ReadProps reads a table of properties (the body of an OPT record with
// the given instance, which is the number of properties) into p, adding
// to what p holds.
func (p *Props) ReadProps(inst uint16, body []byte) {
	n := int(inst)
	if n*6 > len(body) {
		n = len(body) / 6
	}
	if p.values == nil {
		p.values = map[uint16]uint32{}
	}
	data := body[n*6:]
	for i := 0; i < n; i++ {
		id, v := le.Uint16(body[i*6:]), le.Uint32(body[i*6+2:])
		pid := id & 0x3FFF
		p.values[pid] = v
		if id&0x8000 == 0 {
			continue
		}
		// A property with a value of bytes: the number is their length,
		// and they follow the table in the order of the properties.
		size := int(min(uint64(v), uint64(len(data))))
		if p.complex == nil {
			p.complex = map[uint16][]byte{}
		}
		p.complex[pid] = data[:size]
		data = data[size:]
	}
}

// Value returns the number of a property.
func (p *Props) Value(id uint16) (uint32, bool) {
	v, ok := p.values[id]
	return v, ok
}

// String returns a property that is text: UTF-16, up to its terminator.
func (p *Props) String(id uint16) string {
	b := p.complex[id]
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		c := le.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}

// Hidden reports whether the properties mark the shape hidden: the bit
// fHidden of the group's booleans, where the bit that says it is set is
// set too ([MS-ODRAW] 2.3.4.44).
func (p *Props) Hidden() bool {
	v := p.values[PropGroupBools]
	return v&0x00020000 != 0 && v&0x00000002 != 0
}

// Reset empties p for another shape.
func (p *Props) Reset() {
	clear(p.values)
	clear(p.complex)
}

// Picture is a picture of the store.
type Picture struct {
	// MIME is its type: image/png, image/jpeg, image/x-emf, image/x-wmf,
	// image/x-pict, image/bmp, image/tiff.
	MIME string
	// Ext is the extension of a file of that type, without a dot.
	Ext string
	// Data is the picture as a file of its type would hold it, or nil
	// where it could not be read.
	Data []byte
	// Compressed says the file keeps the picture compressed: Data is what
	// it expands to, which is to be counted against the limit on that.
	Compressed bool
}

// ErrBudget is Blip's error for a picture that expands to more than the
// limit.
var ErrBudget = errors.New("officeart: a picture expands past the limit")

// Blip reads a picture record (a BLIP, [MS-ODRAW] 2.2.23): a bitmap as
// the file of its type, a metafile expanded if the record keeps it
// compressed. limit bounds what a compressed picture may expand to. With
// data false only the type is given.
func Blip(r Rec, data bool, limit int64) (Picture, error) {
	var p Picture
	// Before the picture: one or two identifiers of sixteen bytes, which
	// the instance tells apart, and for a bitmap a byte, for a metafile a
	// header of 34 bytes.
	uids := 16
	metafile := false
	switch r.Type {
	case blipEMF:
		p.MIME, p.Ext, metafile = "image/x-emf", "emf", true
		if r.Inst == 0x3D5 {
			uids = 32
		}
	case blipWMF:
		p.MIME, p.Ext, metafile = "image/x-wmf", "wmf", true
		if r.Inst == 0x217 {
			uids = 32
		}
	case blipPICT:
		p.MIME, p.Ext, metafile = "image/x-pict", "pict", true
		if r.Inst == 0x543 {
			uids = 32
		}
	case blipJPEG, blipJPEG2:
		p.MIME, p.Ext = "image/jpeg", "jpg"
		if r.Inst == 0x46B || r.Inst == 0x6E3 {
			uids = 32
		}
	case blipPNG:
		p.MIME, p.Ext = "image/png", "png"
		if r.Inst == 0x6E1 {
			uids = 32
		}
	case blipDIB:
		p.MIME, p.Ext = "image/bmp", "bmp"
		if r.Inst == 0x7A9 {
			uids = 32
		}
	case blipTIFF:
		p.MIME, p.Ext = "image/tiff", "tiff"
		if r.Inst == 0x6E5 {
			uids = 32
		}
	default:
		return p, errors.New("officeart: a picture of a type that is not read")
	}
	if !data {
		return p, nil
	}
	b := r.Body
	if !metafile {
		if len(b) < uids+1 {
			return p, errors.New("officeart: a picture record too short")
		}
		b = b[uids+1:]
		if r.Type == blipDIB {
			p.Data = dibFile(b)
		} else {
			p.Data = b
		}
		return p, nil
	}
	if len(b) < uids+34 {
		return p, errors.New("officeart: a picture record too short")
	}
	head := b[uids : uids+34]
	b = b[uids+34:]
	compression := head[32]
	if compression != 0 {
		// Not compressed.
		p.Data = b
		return p, nil
	}
	p.Compressed = true
	zr, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return p, err
	}
	defer zr.Close()
	// What it expands to is read as far as the limit, whatever the record
	// says its size is.
	out, err := io.ReadAll(io.LimitReader(zr, limit+1))
	if int64(len(out)) > limit {
		return p, ErrBudget
	}
	if err != nil && len(out) == 0 {
		return p, err
	}
	p.Data = out
	return p, nil
}

// dibFile makes a bitmap file of a device-independent bitmap, which is
// such a file without its first fourteen bytes.
func dibFile(dib []byte) []byte {
	if len(dib) < 40 {
		return dib
	}
	// The pixels follow the header and the colour table.
	header := int64(le.Uint32(dib))
	bits := le.Uint16(dib[14:])
	colors := int64(le.Uint32(dib[32:]))
	if colors == 0 && bits <= 8 {
		colors = 1 << bits
	}
	offset := 14 + header + 4*colors
	if header == 40 && le.Uint32(dib[16:]) == 3 {
		offset += 12 // the masks of a bitmap with bit fields
	}
	out := make([]byte, 14, 14+len(dib))
	out[0], out[1] = 'B', 'M'
	le.PutUint32(out[2:], uint32(min(int64(14+len(dib)), 1<<32-1)))
	le.PutUint32(out[10:], uint32(min(offset, 1<<32-1)))
	return append(out, dib...)
}

// StoreEntry is one entry of the store of pictures (a BSE record): the
// picture it holds, if it holds one, and where the picture is otherwise.
type StoreEntry struct {
	// Blip is the picture's record, or a record of type 0 where the
	// entry does not hold it.
	Blip Rec
	// Offset is where the picture's record is in the stream that holds
	// the pictures apart from the drawing (a presentation's Pictures
	// stream, a Word file's WordDocument stream), where the entry does
	// not hold it; Size is that record's length.
	Offset uint32
	Size   uint32
	// Refs is how many shapes use the picture.
	Refs uint32
}

// Store reads the store of pictures in a drawing group's records (the
// body of a DggContainer, or the records in one): its entries in order,
// which shapes name by number from 1. At most limit entries are read;
// past counts the rest.
func Store(b []byte, limit int) (entries []StoreEntry, past int) {
	Walk(b, func(r Rec, depth int) bool {
		switch r.Type {
		case DggContainer, BStoreContainer:
			return true
		case BSE:
			if len(entries) >= limit {
				past++
				return false
			}
			var e StoreEntry
			if len(r.Body) >= 36 {
				e.Size, e.Refs, e.Offset = le.Uint32(r.Body[20:]), le.Uint32(r.Body[24:]), le.Uint32(r.Body[28:])
				name := int(r.Body[33])
				if rest := r.Body[36:]; name <= len(rest) {
					if blip, _, ok := Next(rest[name:]); ok && blip.Type >= blipFirst && blip.Type <= blipLast {
						e.Blip = blip
					}
				}
			}
			entries = append(entries, e)
			return false
		}
		// A picture that is in the store with no entry around it.
		if depth > 0 && r.Type >= blipFirst && r.Type <= blipLast {
			if len(entries) >= limit {
				past++
				return false
			}
			entries = append(entries, StoreEntry{Blip: r})
		}
		return false
	})
	return entries, past
}
