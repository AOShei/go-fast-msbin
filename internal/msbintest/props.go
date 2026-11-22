package msbintest

import (
	"encoding/binary"
	"math"
	"time"
	"unicode/utf16"
)

// This file writes property sets ([MS-OLEPS]) for the tests, on its own
// from the published description: the library's reading is checked
// against it, and against what olefile reads of real files.

// The format identifiers of the three sets, as they stand in a file.
var (
	FmtSummary    = [16]byte{0xE0, 0x85, 0x9F, 0xF2, 0xF9, 0x4F, 0x68, 0x10, 0xAB, 0x91, 0x08, 0x00, 0x2B, 0x27, 0xB3, 0xD9}
	FmtDocSummary = [16]byte{0x02, 0xD5, 0xCD, 0xD5, 0x9C, 0x2E, 0x1B, 0x10, 0x93, 0x97, 0x08, 0x00, 0x2B, 0x2C, 0xF9, 0xAE}
	FmtUser       = [16]byte{0x05, 0xD5, 0xCD, 0xD5, 0x9C, 0x2E, 0x1B, 0x10, 0x93, 0x97, 0x08, 0x00, 0x2B, 0x2C, 0xF9, 0xAE}
)

// Wide is a string written in UTF-16 (VT_LPWSTR); a plain string is
// written as its bytes, in the set's code page (VT_LPSTR).
type Wide string

// Strings is a vector of strings in the set's code page.
type Strings []string

// Raw is a value written as it is: a type and what follows it.
type Raw []byte

// Prop is a property: an identifier, a name for the set's dictionary (a
// property the author defined has one) and a value: string, Wide,
// Strings, int32, int16, bool, float64, time.Time or Raw.
type Prop struct {
	ID    uint32
	Name  string
	Value any
}

// Section is one property set.
type Section struct {
	Format   [16]byte
	CodePage int
	Props    []Prop
}

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func lpstr(s string) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(s)+1))
	return pad4(append(append(b, s...), 0))
}

func propValue(v any) []byte {
	le := binary.LittleEndian
	typed := func(t uint16, body []byte) []byte {
		return append(le.AppendUint32(nil, uint32(t)), body...)
	}
	switch v := v.(type) {
	case string:
		return typed(0x1E, lpstr(v))
	case Wide:
		units := append(utf16.Encode([]rune(string(v))), 0)
		b := le.AppendUint32(nil, uint32(len(units)))
		for _, u := range units {
			b = le.AppendUint16(b, u)
		}
		return typed(0x1F, pad4(b))
	case Strings:
		// The strings of a vector are not padded, as Office writes them.
		b := le.AppendUint32(nil, uint32(len(v)))
		for _, s := range v {
			b = le.AppendUint32(b, uint32(len(s)+1))
			b = append(append(b, s...), 0)
		}
		return typed(0x101E, pad4(b))
	case int32:
		return typed(0x03, le.AppendUint32(nil, uint32(v)))
	case int:
		return typed(0x03, le.AppendUint32(nil, uint32(int32(v))))
	case int16:
		return typed(0x02, pad4(le.AppendUint16(nil, uint16(v))))
	case bool:
		b := uint16(0)
		if v {
			b = 0xFFFF
		}
		return typed(0x0B, pad4(le.AppendUint16(nil, b)))
	case float64:
		return typed(0x05, le.AppendUint64(nil, math.Float64bits(v)))
	case time.Time:
		ft := uint64(0)
		if !v.IsZero() {
			ft = uint64(v.UnixNano()/100 + 116444736000000000)
		}
		return typed(0x40, le.AppendUint64(nil, ft))
	case Raw:
		return v
	}
	panic("msbintest: a property value of a type that is not written")
}

func (s Section) bytes() []byte {
	le := binary.LittleEndian
	type entry struct {
		id   uint32
		body []byte
	}
	entries := []entry{{1, append(le.AppendUint32(nil, 2), le.AppendUint32(nil, uint32(uint16(s.CodePage)))...)}}
	// The dictionary, if any property is named.
	var dict []byte
	named := 0
	for _, p := range s.Props {
		if p.Name == "" {
			continue
		}
		named++
		dict = le.AppendUint32(dict, p.ID)
		if s.CodePage == 1200 {
			units := append(utf16.Encode([]rune(p.Name)), 0)
			dict = le.AppendUint32(dict, uint32(len(units)))
			for _, u := range units {
				dict = le.AppendUint16(dict, u)
			}
			dict = pad4(dict)
		} else {
			dict = le.AppendUint32(dict, uint32(len(p.Name)+1))
			dict = append(append(dict, p.Name...), 0)
		}
	}
	if named > 0 {
		entries = append(entries, entry{0, pad4(append(le.AppendUint32(nil, uint32(named)), dict...))})
	}
	for _, p := range s.Props {
		entries = append(entries, entry{p.ID, propValue(p.Value)})
	}
	head := 8 + 8*len(entries)
	var index, body []byte
	for _, e := range entries {
		index = le.AppendUint32(index, e.id)
		index = le.AppendUint32(index, uint32(head+len(body)))
		body = append(body, e.body...)
	}
	out := le.AppendUint32(nil, uint32(head+len(body)))
	out = le.AppendUint32(out, uint32(len(entries)))
	return append(append(out, index...), body...)
}

// PropertyStream returns a property set stream of the sections: one, or
// the two of the document summary.
func PropertyStream(sections ...Section) []byte {
	le := binary.LittleEndian
	out := []byte{0xFE, 0xFF, 0, 0, 0x06, 0x00, 0x02, 0x00} // byte order, version, system
	out = append(out, make([]byte, 16)...)                  // class
	out = le.AppendUint32(out, uint32(len(sections)))
	at := len(out) + 20*len(sections)
	var bodies []byte
	for _, s := range sections {
		b := s.bytes()
		out = append(out, s.Format[:]...)
		out = le.AppendUint32(out, uint32(at+len(bodies)))
		bodies = append(bodies, b...)
	}
	return append(out, bodies...)
}
