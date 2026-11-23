// Package vba reads the source code of a VBA project: the modules of
// macros an Office file carries in a storage of its compound file (a
// presentation, in a compound file of its own within its stream). It is written from the format's published description,
// [MS-OVBA]. The code is read as text. Nothing in it is run.
//
// A project keeps each module's source compressed, after a cache of
// compiled code that is not read. Its "dir" stream, compressed the same
// way, lists the modules, the stream each is in, and where in the stream
// the source starts.
//
// The reader trusts nothing in the file: a module's source is read only
// up to a limit on what all of them decompress to, and anything out of
// place ends the module where it is, with what was read kept.
package vba

import (
	"encoding/binary"
	"errors"
	"math/bits"
	"strings"
	"unicode/utf16"

	"github.com/AOShei/go-fast-msbin/internal/cfb"
	"github.com/AOShei/go-fast-msbin/internal/codepage"
)

// ErrNotProject says the compound file holds no VBA project.
var ErrNotProject = errors.New("vba: no VBA project")

// ErrTooLarge says a stream of the project is larger than is read.
var ErrTooLarge = errors.New("vba: a stream of the project is larger than 256 MiB")

// Module is one module of a project.
type Module struct {
	// Name is the module's name ("Module1", "ThisWorkbook").
	Name string
	// Code is its source, lines ended by line feeds.
	Code string
}

// Project is what Read finds in a project.
type Project struct {
	Modules []Module
	// CodePage is the code page the project's text is written in.
	CodePage int
	// Undecoded counts characters of text that the code page does not
	// define, or bytes of a code page not known: each became U+FFFD.
	Undecoded int
	// Cut says the limit was reached: a module is cut off, or later ones
	// are left out.
	Cut bool
	// Damaged says some of the project could not be read: what was read
	// up to the damage is kept.
	Damaged bool
	// Unlisted counts the modules past maxModules, which are not read.
	Unlisted int
}

// maxModules bounds the modules read from one project.
const maxModules = 1 << 12

// maxStream bounds the size of a stream of a project that is read.
const maxStream = 1 << 28

// Read reads the project in the storage of f that path names: the storage
// that holds the VBA storage, which is Macros in a Word file,
// _VBA_PROJECT_CUR in a workbook and the root of the file a presentation
// keeps its project in. limit bounds the source it gives, in bytes, all
// modules together.
func Read(f *cfb.File, path []string, limit int64) (*Project, error) {
	in := func(name string) []string {
		return append(append(make([]string, 0, len(path)+2), path...), "VBA", name)
	}
	if !f.Has(in("dir")...) {
		return nil, ErrNotProject
	}
	dirStream, err := f.Open(in("dir")...)
	if err != nil {
		return nil, err
	}
	raw, err := readAll(dirStream, maxStream)
	if err != nil {
		return nil, err
	}
	p := &Project{CodePage: 1252}
	dir, err := Decompress(raw, 1<<24)
	if err != nil {
		if len(dir) == 0 {
			return nil, err
		}
		p.Damaged = true
	}
	mods, damaged := p.parseDir(dir)
	p.Damaged = p.Damaged || damaged
	left := limit
	for _, m := range mods {
		if left <= 0 {
			p.Cut = true
			break
		}
		stream, err := f.Open(in(m.stream)...)
		if err != nil {
			p.Damaged = true
			continue
		}
		src, err := readAll(stream, maxStream)
		if err != nil || m.offset > int64(len(src)) {
			p.Damaged = true
			continue
		}
		code, err := Decompress(src[m.offset:], left)
		switch {
		case errors.Is(err, errLimit):
			p.Cut = true
		case err != nil:
			p.Damaged = true
		}
		text := p.decode(code)
		text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
		left -= int64(len(text))
		p.Modules = append(p.Modules, Module{Name: m.name, Code: text})
	}
	return p, nil
}

// module is a module as the dir stream lists it.
type module struct {
	name   string
	stream string
	offset int64
}

// Records of the dir stream ([MS-OVBA] 2.3.4.2) that are read. Every
// record is an identifier, a size and that many bytes, except
// PROJECTVERSION, whose size field is followed by six bytes whatever it
// says.
const (
	recCodePage          = 0x0003
	recVersion           = 0x0009
	recModuleName        = 0x0019
	recModuleNameUnicode = 0x0047
	recStreamName        = 0x001A
	recStreamNameUnicode = 0x0032
	recModuleOffset      = 0x0031
	recModuleEnd         = 0x002B
	recDirEnd            = 0x0010
)

// parseDir reads the modules the dir stream lists. damaged says it ended
// early.
func (p *Project) parseDir(dir []byte) (mods []module, damaged bool) {
	var cur *module
	end := func() {
		switch {
		case cur == nil || cur.stream == "":
		case len(mods) < maxModules:
			if cur.name == "" {
				cur.name = cur.stream
			}
			mods = append(mods, *cur)
		default:
			p.Unlisted++
		}
		cur = nil
	}
	unicodeStream := false
	for off := 0; ; {
		if off == len(dir) {
			end()
			return mods, true // no terminator
		}
		if off+6 > len(dir) {
			end()
			return mods, true
		}
		id := binary.LittleEndian.Uint16(dir[off:])
		size := int(binary.LittleEndian.Uint32(dir[off+2:]))
		if id == recVersion {
			size = 6
		}
		off += 6
		if size < 0 || size > len(dir)-off {
			end()
			return mods, true
		}
		data := dir[off : off+size]
		off += size
		switch id {
		case recCodePage:
			if len(data) >= 2 {
				p.CodePage = int(binary.LittleEndian.Uint16(data))
			}
		case recModuleName:
			end()
			cur = &module{name: p.decode(data)}
			unicodeStream = false
		case recModuleNameUnicode:
			if cur != nil && len(data) > 0 {
				cur.name = fromUTF16(data)
			}
		case recStreamName:
			if cur != nil && !unicodeStream {
				cur.stream = p.decode(data)
			}
		case recStreamNameUnicode:
			if cur != nil && len(data) > 0 {
				cur.stream, unicodeStream = fromUTF16(data), true
			}
		case recModuleOffset:
			if cur != nil && len(data) >= 4 {
				cur.offset = int64(binary.LittleEndian.Uint32(data))
			}
		case recModuleEnd:
			end()
		case recDirEnd:
			end()
			return mods, false
		}
	}
}

// decode turns text in the project's code page into UTF-8.
func (p *Project) decode(b []byte) string {
	text, undecoded := codepage.Decode(p.CodePage, b)
	p.Undecoded += undecoded
	return text
}

// fromUTF16 decodes UTF-16 little-endian text.
func fromUTF16(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

var (
	errLimit      = errors.New("vba: limit reached")
	errCompressed = errors.New("vba: compressed data is damaged")
)

// Decompress decompresses a compressed container ([MS-OVBA] 2.4.1),
// giving at most limit bytes. What was decompressed before an error is
// returned with it; reaching the limit is an error too.
func Decompress(src []byte, limit int64) ([]byte, error) {
	if len(src) == 0 || src[0] != 1 {
		return nil, errCompressed
	}
	out := make([]byte, 0, min(int64(len(src))*2, limit, 1<<20))
	for pos := 1; pos+2 <= len(src); {
		header := binary.LittleEndian.Uint16(src[pos:])
		if header>>12&7 != 3 {
			return out, errCompressed
		}
		end := min(pos+int(header&0x0FFF)+3, len(src))
		data := src[pos+2 : end]
		pos = end
		start := len(out)
		if header&0x8000 == 0 {
			// Stored as it is: 4096 bytes.
			if int64(len(out)+len(data)) > limit {
				return append(out, data[:limit-int64(len(out))]...), errLimit
			}
			out = append(out, data...)
			continue
		}
		for i := 0; i < len(data); {
			flags := data[i]
			i++
			for bit := 0; bit < 8 && i < len(data); bit++ {
				if int64(len(out)) >= limit {
					return out, errLimit
				}
				if flags&(1<<bit) == 0 {
					out = append(out, data[i])
					i++
					continue
				}
				if i+2 > len(data) {
					return out, errCompressed
				}
				token := int(binary.LittleEndian.Uint16(data[i:]))
				i += 2
				// How a copy token divides into offset and length
				// depends on how much of the chunk is decompressed.
				done := len(out) - start
				if done == 0 {
					return out, errCompressed
				}
				count := max(bits.Len(uint(done-1)), 4)
				lengthMask := 0xFFFF >> count
				length := token&lengthMask + 3
				offset := token>>(16-count) + 1
				if offset > done || done+length > 4096 {
					return out, errCompressed
				}
				for k := 0; k < length; k++ {
					if int64(len(out)) >= limit {
						return out, errLimit
					}
					out = append(out, out[len(out)-offset])
				}
			}
		}
	}
	return out, nil
}

// readAll reads a whole stream, which may be no longer than limit.
func readAll(s *cfb.Stream, limit int) ([]byte, error) {
	if s.Size() > int64(limit) {
		return nil, ErrTooLarge
	}
	buf := make([]byte, s.Size())
	n, err := s.ReadAt(buf, 0)
	if n == len(buf) {
		err = nil
	}
	return buf[:n], err
}
