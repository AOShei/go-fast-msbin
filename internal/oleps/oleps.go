// Package oleps reads property sets ([MS-OLEPS]): the streams
// "\x05SummaryInformation" and "\x05DocumentSummaryInformation", in which
// a Word, Excel or PowerPoint file keeps its title, its author, its dates
// and the properties its author defined.
package oleps

import (
	"encoding/binary"
	"math"
	"strconv"
	"time"
	"unicode/utf16"

	"github.com/AOShei/go-fast-msbin/internal/codepage"
)

// The streams' names.
const (
	SummaryStream    = "\x05SummaryInformation"
	DocSummaryStream = "\x05DocumentSummaryInformation"
)

// MaxStream bounds the bytes of a property set stream that are read. A
// stream holds a thumbnail at most besides its text; one larger than
// this is read as far as this.
const MaxStream = 16 << 20

// Bounds on what one set gives.
const (
	maxProperties = 1 << 12
	maxValue      = 64 << 10 // bytes of a string value kept
	maxVector     = 256      // elements of a vector of strings read
)

// The format identifiers of the sets that are read ([MS-OLEPS] 2.25),
// as they stand in the file.
var (
	fmtSummary    = [16]byte{0xE0, 0x85, 0x9F, 0xF2, 0xF9, 0x4F, 0x68, 0x10, 0xAB, 0x91, 0x08, 0x00, 0x2B, 0x27, 0xB3, 0xD9}
	fmtDocSummary = [16]byte{0x02, 0xD5, 0xCD, 0xD5, 0x9C, 0x2E, 0x1B, 0x10, 0x93, 0x97, 0x08, 0x00, 0x2B, 0x2C, 0xF9, 0xAE}
	fmtUser       = [16]byte{0x05, 0xD5, 0xCD, 0xD5, 0x9C, 0x2E, 0x1B, 0x10, 0x93, 0x97, 0x08, 0x00, 0x2B, 0x2C, 0xF9, 0xAE}
)

// swapped gives an identifier as Word for the Macintosh writes it: its
// first three fields with their bytes in the other order.
func swapped(id [16]byte) [16]byte {
	id[0], id[1], id[2], id[3] = id[3], id[2], id[1], id[0]
	id[4], id[5] = id[5], id[4]
	id[6], id[7] = id[7], id[6]
	return id
}

// Kind says which set a Set is.
type Kind uint8

const (
	Other      Kind = iota
	Summary         // the summary information
	DocSummary      // the document summary information
	User            // the properties the author defined
)

// Property identifiers of the summary information ([MS-OLEPS] 3.1.x and
// [MS-OSHARED] 2.3.3.2.1).
const (
	PIDTitle      = 2
	PIDSubject    = 3
	PIDAuthor     = 4
	PIDKeywords   = 5
	PIDComments   = 6
	PIDTemplate   = 7
	PIDLastAuthor = 8
	PIDRevision   = 9
	PIDCreated    = 12
	PIDSaved      = 13
	PIDAppName    = 18
)

// Property identifiers of the document summary information ([MS-OSHARED]
// 2.3.3.2.2).
const (
	PIDCategory      = 2
	PIDManager       = 14
	PIDCompany       = 15
	PIDVersion       = 23
	PIDContentStatus = 27
)

// Type is the type of a value as the model needs to know it.
type Type uint8

const (
	None   Type = iota // a type that is not read
	String             // Text holds it
	Int                // Int holds it
	Float              // Float holds it
	Bool               // Int is 0 or 1
	Time               // Time holds it; the zero time for a date not set
)

// Value is the value of a property.
type Value struct {
	Type  Type
	Text  string
	Int   int64
	Float float64
	Time  time.Time
}

// String gives the value as text: a number in decimal, a date in RFC
// 3339, "true" or "false". It is empty for a value that is not read and
// for a date not set.
func (v Value) String() string {
	switch v.Type {
	case String:
		return v.Text
	case Int:
		return strconv.FormatInt(v.Int, 10)
	case Float:
		return strconv.FormatFloat(v.Float, 'g', -1, 64)
	case Bool:
		return strconv.FormatBool(v.Int != 0)
	case Time:
		if v.Time.IsZero() {
			return ""
		}
		return v.Time.Format(time.RFC3339)
	}
	return ""
}

// Property is one property of a set.
type Property struct {
	ID uint32
	// Name is the name the set's dictionary gives the property: a
	// property the author defined has one, a property of the two
	// summaries has none.
	Name  string
	Value Value
}

// Set is one property set of a stream.
type Set struct {
	Kind     Kind
	CodePage int
	// Properties are in the order of the file, without the code page,
	// the dictionary and the locale.
	Properties []Property
}

// Get returns the value of the property with the identifier.
func (s *Set) Get(id uint32) (Value, bool) {
	for i := range s.Properties {
		if s.Properties[i].ID == id {
			return s.Properties[i].Value, true
		}
	}
	return Value{}, false
}

// Result is what Read found in a stream.
type Result struct {
	Sets []Set
	// Damaged says some of the stream could not be read; what could be
	// is in Sets.
	Damaged bool
	// Undecoded counts characters of text that the set's code page does
	// not define, or bytes of a code page not known: each is U+FFFD.
	Undecoded int
	// ValuesCut counts the string values cut at the bound on their
	// length, and ValueBytes the bytes cut off them; Past counts the
	// properties past the bound on their number.
	ValuesCut, ValueBytes, Past int64

	// given is the bytes of text given so far, and room what the stream
	// can account for: a sound stream keeps each value once, so its text
	// is no longer than the stream, decoded.
	given, room int
	inVector    bool // the value being read is an element of a vector
}

// Read reads the property sets of a stream. It never fails: a stream
// that is not a property set gives no sets and is Damaged.
func Read(b []byte) Result {
	res := Result{room: 3*len(b) + 1024}
	le := binary.LittleEndian
	// The header: byte order, version, system, class, number of sets
	// ([MS-OLEPS] 2.21).
	if len(b) < 28 || le.Uint16(b) != 0xFFFE {
		res.Damaged = true
		return res
	}
	n := le.Uint32(b[24:])
	if n == 0 || n > 2 {
		// One set, or the two of the document summary.
		res.Damaged = true
		n = min(n, 2)
	}
	for i := 0; i < int(n); i++ {
		at := 28 + 20*i
		if at+20 > len(b) {
			res.Damaged = true
			break
		}
		var id [16]byte
		copy(id[:], b[at:])
		off := int64(le.Uint32(b[at+16:]))
		set := Set{CodePage: 1252}
		switch id {
		case fmtSummary, swapped(fmtSummary):
			set.Kind = Summary
		case fmtDocSummary, swapped(fmtDocSummary):
			set.Kind = DocSummary
		case fmtUser, swapped(fmtUser):
			set.Kind = User
		}
		if off < int64(at+20) || off+8 > int64(len(b)) {
			res.Damaged = true
			continue
		}
		res.set(&set, b[off:])
		res.Sets = append(res.Sets, set)
	}
	return res
}

// Reserved property identifiers.
const (
	pidDictionary = 0
	pidCodePage   = 1
	pidLocale     = 0x80000000
	pidBehavior   = 0x80000003
)

// set reads one set, which begins at the start of b ([MS-OLEPS] 2.20).
func (res *Result) set(set *Set, b []byte) {
	le := binary.LittleEndian
	size, count := int64(le.Uint32(b)), int64(le.Uint32(b[4:]))
	if size < int64(len(b)) && size >= 8 {
		b = b[:size]
	} else if size > int64(len(b)) {
		res.Damaged = true // the set says it runs past the stream's end
	}
	if 8+count*8 > int64(len(b)) {
		res.Damaged = true
		count = int64(len(b)-8) / 8
	}
	type ref struct{ id, off uint32 }
	refs := make([]ref, 0, min(count, maxProperties))
	dictionary := -1
	for i := int64(0); i < count; i++ {
		r := ref{le.Uint32(b[8+8*i:]), le.Uint32(b[12+8*i:])}
		if int64(r.off) >= int64(len(b)) {
			res.Damaged = true
			continue
		}
		switch r.id {
		case pidCodePage:
			// The code page comes before the strings it is for, whatever
			// the order of the properties.
			if v := b[r.off:]; len(v) >= 6 && le.Uint16(v) == vtI2 {
				set.CodePage = int(le.Uint16(v[4:]))
			}
		case pidDictionary:
			dictionary = int(r.off)
		case pidLocale, pidBehavior:
		default:
			if len(refs) == maxProperties {
				res.Past++
				continue
			}
			refs = append(refs, r)
		}
	}
	names := map[uint32]string{}
	if dictionary >= 0 {
		res.dictionary(set, b[dictionary:], names)
	}
	for _, r := range refs {
		v, _, ok := res.value(set, b[r.off:], 0)
		if !ok {
			res.Damaged = true
		}
		// Properties that share one value's bytes would give more text
		// than the stream holds: past what it can account for, values
		// are left out, and the stream is damaged.
		if res.given += len(v.Text); res.given > res.room {
			v, res.Damaged = Value{}, true
		}
		set.Properties = append(set.Properties, Property{ID: r.id, Name: names[r.id], Value: v})
	}
}

// dictionary reads the names of the properties ([MS-OLEPS] 2.17).
func (res *Result) dictionary(set *Set, b []byte, names map[uint32]string) {
	le := binary.LittleEndian
	if len(b) < 4 {
		res.Damaged = true
		return
	}
	count := le.Uint32(b)
	at := 4
	for i := uint32(0); i < count; i++ {
		if at+8 > len(b) {
			res.Damaged = true
			return
		}
		id, length := le.Uint32(b[at:]), int64(le.Uint32(b[at+4:]))
		at += 8
		// The length counts characters: bytes, or 16-bit units padded
		// to a multiple of four bytes.
		bytes := length
		if set.CodePage == cpUTF16 {
			bytes = length * 2
		}
		if bytes > int64(len(b)-at) {
			res.Damaged = true
			return
		}
		raw := b[at : at+int(bytes)]
		at += int(bytes)
		if set.CodePage == cpUTF16 {
			at += (4 - int(bytes)%4) % 4
		}
		if len(names) < maxProperties {
			names[id] = res.text(set.CodePage, raw)
		}
	}
}

const cpUTF16 = 1200

// text decodes a string of the set, up to its terminator, and bounds its
// length.
func (res *Result) text(cp int, raw []byte) string {
	var s string
	if cp == cpUTF16 {
		u := make([]uint16, 0, min(len(raw)/2, maxValue))
		for i := 0; i+1 < len(raw); i += 2 {
			c := binary.LittleEndian.Uint16(raw[i:])
			if c == 0 {
				break
			}
			if len(u) == cap(u) {
				res.ValuesCut++
				res.ValueBytes += int64(len(raw) - i)
				break
			}
			u = append(u, c)
		}
		return string(utf16.Decode(u))
	}
	for i, c := range raw {
		if c == 0 {
			raw = raw[:i]
			break
		}
	}
	if len(raw) > maxValue {
		res.ValuesCut++
		res.ValueBytes += int64(len(raw) - maxValue)
		raw = raw[:maxValue]
	}
	s, undecoded := codepage.Decode(cp, raw)
	res.Undecoded += undecoded
	return s
}

// Value types ([MS-OLEPS] 2.15).
const (
	vtEmpty    = 0x0000
	vtNull     = 0x0001
	vtI2       = 0x0002
	vtI4       = 0x0003
	vtR4       = 0x0004
	vtR8       = 0x0005
	vtCY       = 0x0006
	vtDate     = 0x0007
	vtBSTR     = 0x0008
	vtError    = 0x000A
	vtBool     = 0x000B
	vtVariant  = 0x000C
	vtI1       = 0x0010
	vtUI1      = 0x0011
	vtUI2      = 0x0012
	vtUI4      = 0x0013
	vtI8       = 0x0014
	vtUI8      = 0x0015
	vtInt      = 0x0016
	vtUint     = 0x0017
	vtLPSTR    = 0x001E
	vtLPWSTR   = 0x001F
	vtFiletime = 0x0040
	vtVector   = 0x1000
)

// value reads a typed value at the start of b, and returns the bytes it
// takes. depth is how many values it is inside of. ok is false where the
// value runs past the end.
func (res *Result) value(set *Set, b []byte, depth int) (v Value, size int, ok bool) {
	le := binary.LittleEndian
	if len(b) < 4 {
		return v, 0, false
	}
	typ := le.Uint16(b)
	v, size, ok = res.scalar(set, typ, b[4:], depth)
	return v, size + 4, ok
}

// scalar reads a value of the type, without its type.
func (res *Result) scalar(set *Set, typ uint16, b []byte, depth int) (v Value, size int, ok bool) {
	le := binary.LittleEndian
	need := func(n int) bool { return len(b) >= n }
	switch typ {
	case vtEmpty, vtNull:
		return v, 0, true
	case vtI2:
		if !need(2) {
			return v, 0, false
		}
		return Value{Type: Int, Int: int64(int16(le.Uint16(b)))}, 4, true
	case vtUI2:
		if !need(2) {
			return v, 0, false
		}
		return Value{Type: Int, Int: int64(le.Uint16(b))}, 4, true
	case vtI1, vtUI1:
		if !need(1) {
			return v, 0, false
		}
		n := int64(b[0])
		if typ == vtI1 {
			n = int64(int8(b[0]))
		}
		return Value{Type: Int, Int: n}, 4, true
	case vtI4, vtInt, vtError:
		if !need(4) {
			return v, 0, false
		}
		return Value{Type: Int, Int: int64(int32(le.Uint32(b)))}, 4, true
	case vtUI4, vtUint:
		if !need(4) {
			return v, 0, false
		}
		return Value{Type: Int, Int: int64(le.Uint32(b))}, 4, true
	case vtI8, vtUI8:
		if !need(8) {
			return v, 0, false
		}
		u := le.Uint64(b)
		if typ == vtUI8 && u > math.MaxInt64 {
			return Value{Type: String, Text: strconv.FormatUint(u, 10)}, 8, true
		}
		return Value{Type: Int, Int: int64(u)}, 8, true
	case vtR4:
		if !need(4) {
			return v, 0, false
		}
		return number(float64(math.Float32frombits(le.Uint32(b)))), 4, true
	case vtR8:
		if !need(8) {
			return v, 0, false
		}
		return number(math.Float64frombits(le.Uint64(b))), 8, true
	case vtCY:
		// Currency: a count of ten-thousandths.
		if !need(8) {
			return v, 0, false
		}
		return number(float64(int64(le.Uint64(b))) / 10000), 8, true
	case vtBool:
		if !need(2) {
			return v, 0, false
		}
		n := int64(0)
		if le.Uint16(b) != 0 {
			n = 1
		}
		return Value{Type: Bool, Int: n}, 4, true
	case vtDate:
		// Days since 30 December 1899, with the time as a fraction.
		if !need(8) {
			return v, 0, false
		}
		days := math.Float64frombits(le.Uint64(b))
		if math.IsNaN(days) || days < -657434 || days > 2958466 { // the years 100 to 9999
			return Value{Type: Time}, 8, true
		}
		whole, frac := math.Modf(days)
		t := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(whole)).
			Add(time.Duration(math.Round(math.Abs(frac)*86400)) * time.Second)
		return Value{Type: Time, Time: t}, 8, true
	case vtFiletime:
		if !need(8) {
			return v, 0, false
		}
		return Value{Type: Time, Time: Filetime(le.Uint64(b))}, 8, true
	case vtLPSTR, vtBSTR:
		// A count of bytes, and the text in the set's code page.
		if !need(4) {
			return v, 0, false
		}
		n := int64(le.Uint32(b))
		if n > int64(len(b)-4) {
			return Value{Type: String, Text: res.text(set.CodePage, b[4:])}, len(b), false
		}
		raw := b[4 : 4+n]
		size := 4 + int(n)
		// A string of a vector is not padded to four bytes, as Office
		// writes it ([MS-OSHARED] 2.3.3.1.9); one that is a property's
		// own value is.
		if !res.inVector {
			size += (4 - size%4) % 4
		}
		return Value{Type: String, Text: res.text(set.CodePage, raw)}, size, true
	case vtLPWSTR:
		// A count of 16-bit units.
		if !need(4) {
			return v, 0, false
		}
		n := int64(le.Uint32(b)) * 2
		if n > int64(len(b)-4) {
			return Value{Type: String, Text: res.text(cpUTF16, b[4:])}, len(b), false
		}
		size := 4 + int(n)
		size += (4 - size%4) % 4
		return Value{Type: String, Text: res.text(cpUTF16, b[4:4+n])}, size, true
	case vtVariant:
		if depth >= 4 {
			return v, 0, false
		}
		return res.value(set, b, depth+1)
	}
	if typ&vtVector != 0 && depth < 4 {
		return res.vector(set, typ&^vtVector, b, depth)
	}
	return v, 0, true // a type that is not read: a thumbnail, a blob
}

// vector reads a vector of strings as one text, its elements on lines of
// their own: the titles of the parts of a document are kept so. A vector
// of anything else is not read.
func (res *Result) vector(set *Set, typ uint16, b []byte, depth int) (v Value, size int, ok bool) {
	if typ != vtLPSTR && typ != vtLPWSTR && typ != vtBSTR && typ != vtVariant {
		return v, 0, true
	}
	if len(b) < 4 {
		return v, 0, false
	}
	count := binary.LittleEndian.Uint32(b)
	at := 4
	var text []byte
	any := false
	was := res.inVector
	res.inVector = true
	defer func() { res.inVector = was }()
	for i := uint32(0); i < count; i++ {
		if i == maxVector {
			res.ValuesCut++
			break
		}
		if at > len(b) {
			return v, at, false
		}
		var el Value
		var n int
		var good bool
		if typ == vtVariant {
			el, n, good = res.value(set, b[at:], depth+1)
		} else {
			el, n, good = res.scalar(set, typ, b[at:], depth+1)
		}
		if el.Type == String {
			if any {
				text = append(text, '\n')
			}
			text = append(text, el.Text...)
			any = true
		}
		at += n
		if !good || n == 0 {
			return Value{Type: String, Text: string(text)}, at, good
		}
		if len(text) > maxValue {
			res.ValuesCut++
			res.ValueBytes += int64(len(text) - maxValue)
			text = text[:maxValue]
			break
		}
	}
	if !any {
		return v, at, true
	}
	return Value{Type: String, Text: string(text)}, at, true
}

func number(f float64) Value {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Value{}
	}
	return Value{Type: Float, Float: f}
}

// Filetime turns a count of 100-nanosecond intervals since 1 January
// 1601, which is how these files keep a date, into a time in UTC. Zero,
// and a count past the year 9999, give the zero time: the date is not
// set.
func Filetime(ft uint64) time.Time {
	const toUnix = 116444736000000000 // 1601 to 1970, in the same intervals
	const maxFiletime = 2650467743999999999
	if ft == 0 || ft > maxFiletime {
		return time.Time{}
	}
	ticks := int64(ft) - toUnix
	sec, rem := ticks/1e7, ticks%1e7
	if rem < 0 {
		sec, rem = sec-1, rem+1e7
	}
	return time.Unix(sec, rem*100).UTC()
}
