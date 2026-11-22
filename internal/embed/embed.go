// Package embed finds the objects a file of the three formats embeds as
// storages of its compound file, and hands each on as a file of its own.
//
// A Word file keeps its objects in the storage ObjectPool, one storage
// each; a workbook keeps them at its root, in storages named MBD and
// eight digits. What such a storage holds is one of:
//
//   - a file in a stream: "\x01Ole10Native" (a file of any kind, wrapped
//     by the object packager, or the bytes of the program that made the
//     object), "Package" (an Office Open XML file) or "CONTENTS" (what
//     the embedding program wrote, a PDF for one). The bytes are handed
//     on as they are.
//   - a compound document: the streams of a Word, Excel or PowerPoint
//     file, or of any other program that stores in compound files. The
//     storage is written out as a compound file of its own.
//   - a link to a file outside: counted, and not followed.
package embed

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/cfb"
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/codepage"
	"github.com/AOShei/go-fast-msbin/internal/detect"
	"github.com/AOShei/go-fast-msbin/internal/unit"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// Object is an embedded object: a storage of the file.
type Object struct {
	// Path is the storage's path from the root.
	Path []string
	// Link says the object is a link to a file outside, by the flag of
	// its "\x01Ole" stream ([MS-OLEDS] 2.3.3) or, in a workbook, by its
	// storage's name.
	Link bool
}

// Streams of an object's storage.
const (
	oleStream     = "\x01Ole"
	nativeStream  = "\x01Ole10Native"
	packageStream = "Package"
	contentStream = "CONTENTS"
)

// Find lists the embedded objects of a file, in the order of its
// directory, and counts those past the bound on their number.
func Find(f *cfb.File) (objects []Object, past int64) {
	add := func(path ...string) {
		if len(objects) == warn.MaxObjects {
			past++
			return
		}
		o := Object{Path: path}
		if s, err := f.Open(append(append([]string(nil), path...), oleStream)...); err == nil {
			var head [8]byte
			if n, _ := s.ReadAt(head[:], 0); n == 8 {
				o.Link = binary.LittleEndian.Uint32(head[4:])&1 != 0
			}
		}
		objects = append(objects, o)
	}
	for _, name := range f.List() {
		if !f.IsStorage(name) {
			continue
		}
		switch {
		case name == "ObjectPool":
			for _, child := range f.List(name) {
				if f.IsStorage(name, child) {
					add(name, child)
				}
			}
		case isNumbered(name, "MBD"):
			add(name)
		case isNumbered(name, "LNK"):
			if len(objects) == warn.MaxObjects {
				past++
				continue
			}
			objects = append(objects, Object{Path: []string{name}, Link: true})
		}
	}
	return objects, past
}

// isNumbered reports whether name is the prefix and eight hexadecimal
// digits, as a workbook names the storages of its objects.
func isNumbered(name, prefix string) bool {
	if len(name) != len(prefix)+8 || !strings.HasPrefix(name, prefix) {
		return false
	}
	for _, c := range name[len(prefix):] {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Result is what Hand gives.
type Result struct {
	Children []*gofastcore.Document
	// Links counts the objects that are links to files outside.
	Links int
	// Damaged names the objects that could not be read.
	Damaged []string
	// Deep counts the objects whose storages nest too deep to write out.
	Deep int64
	// Limited says the decode budget ran out: the objects from there on
	// are listed and not read.
	Limited bool
}

// Hand hands the embedded objects to the caller's child extractor. With
// no child extractor, or with read false, each is listed without being
// read. What is handed on is taken from the decode budget in ctx.
func Hand(ctx context.Context, f *cfb.File, opts *gofastcore.Options, objects []Object, read bool) Result {
	var res Result
	decoded := gofastcore.DecodeBudgetFrom(ctx)
	for _, o := range objects {
		if o.Link {
			res.Links++
			continue
		}
		in := func(name string) []string {
			return append(append(make([]string, 0, len(o.Path)+1), o.Path...), name)
		}
		label := clean.Label(o.Path[len(o.Path)-1])
		// What the storage holds decides how it is handed on.
		var stream []string
		switch {
		case f.Has(in(nativeStream)...) && !f.IsStorage(in(nativeStream)...):
			stream = in(nativeStream)
		case f.Has(in(packageStream)...) && !f.IsStorage(in(packageStream)...):
			stream = in(packageStream)
		case f.Has(in(contentStream)...) && !f.IsStorage(in(contentStream)...):
			stream = in(contentStream)
		}
		name, mime, size := label, "", int64(0)
		if stream != nil {
			if s, err := f.Open(stream...); err == nil {
				size = s.Size()
			}
		} else {
			name, mime = describe(f, o.Path, label)
		}
		if opts == nil || opts.Child == nil {
			// Listed, not read: there is nobody to read it.
			res.Children = append(res.Children, opts.ExtractChild(ctx, name, mime, nil, size))
			continue
		}
		if !read || res.Limited || ctx.Err() != nil {
			res.Children = append(res.Children, listed(name, mime, size))
			continue
		}
		left := int64(1) << 62
		if decoded != nil {
			left = decoded.Left()
		}
		var data []byte
		if stream != nil {
			s, err := f.Open(stream...)
			if err != nil {
				res.Damaged = append(res.Damaged, label)
				continue
			}
			if s.Size() > left {
				res.Limited = true
				res.Children = append(res.Children, listed(name, mime, size))
				continue
			}
			data, _, err = unit.ReadAll(s, s.Size())
			if err != nil {
				res.Damaged = append(res.Damaged, label)
				if len(data) == 0 {
					continue
				}
			}
			if stream[len(stream)-1] == nativeStream {
				name, data = native(data, label)
			}
		} else {
			var err error
			data, _, err = f.Export(left, o.Path...)
			switch {
			case errors.Is(err, cfb.ErrDeep):
				res.Deep++
				res.Children = append(res.Children, listed(name, mime, size))
				continue
			case errors.Is(err, cfb.ErrLimit):
				res.Limited = true
				res.Children = append(res.Children, listed(name, mime, size))
				continue
			case err != nil:
				res.Damaged = append(res.Damaged, label)
				continue
			}
		}
		if !decoded.Take(int64(len(data))) {
			res.Limited = true
			res.Children = append(res.Children, listed(name, mime, size))
			continue
		}
		res.Children = append(res.Children, opts.ExtractChild(ctx, name, mime, bytes.NewReader(data), int64(len(data))))
	}
	return res
}

// describe names a compound document held in a storage, and gives its
// type where the storage is a Word, Excel or PowerPoint document: the
// name is the storage's, with the extension of the format.
func describe(f *cfb.File, path []string, label string) (name, mime string) {
	has := func(stream string) bool {
		return f.Has(append(append(make([]string, 0, len(path)+1), path...), stream)...)
	}
	switch {
	case has("WordDocument"):
		return label + "." + detect.Doc, detect.MIME(detect.Doc)
	case has("Workbook"), has("Book"):
		return label + "." + detect.Xls, detect.MIME(detect.Xls)
	case has("PowerPoint Document"):
		return label + "." + detect.Ppt, detect.MIME(detect.Ppt)
	}
	return label, "application/x-ole-storage"
}

// listed is an embedded file that is named and not read, because a limit
// was reached before it. The child extractor is not called for it: there
// is nothing to give it.
func listed(name, mime string, size int64) *gofastcore.Document {
	doc := gofastcore.New("")
	doc.Source.Name, doc.Source.MIME, doc.Source.Size = name, mime, size
	doc.Warn(gofastcore.SeverityWarning, gofastcore.WarnChildNotExtracted, "a limit was reached before this file could be read")
	return doc
}

// native takes the file out of an "\x01Ole10Native" stream ([MS-OLEDS]
// 2.3.6): a length and the native data. Where the native data is what
// the object packager writes (a label, the path the file came from, and
// the file), the file and its name are given. Otherwise the native data
// is given whole, under the storage's name: it is in the format of the
// program that made the object.
func native(b []byte, label string) (name string, data []byte) {
	le := binary.LittleEndian
	if len(b) < 4 {
		return label, b
	}
	body := b[4:]
	if n := int64(le.Uint32(b)); n < int64(len(body)) {
		body = body[:n]
	}
	// The packager's layout: a flag word, the label, the path, a word
	// of flags and a word saying embedded (3) or linked (1), the length
	// of the temporary path and that path, the length of the file and
	// the file.
	cstring := func(at int) (s []byte, next int, ok bool) {
		i := bytes.IndexByte(body[min(at, len(body)):], 0)
		if i < 0 || i > 4096 {
			return nil, 0, false
		}
		return body[at : at+i], at + i + 1, true
	}
	if len(body) < 2 || le.Uint16(body) != 2 {
		return label, body
	}
	shown, at, ok := cstring(2)
	if !ok {
		return label, body
	}
	path, at, ok := cstring(at)
	if !ok || at+8 > len(body) || le.Uint16(body[at+2:]) != 3 {
		return label, body
	}
	tmp := int64(le.Uint32(body[at+4:]))
	at += 8
	if tmp > int64(len(body)-at) || int64(at)+tmp+4 > int64(len(body)) {
		return label, body
	}
	at += int(tmp)
	size := int64(le.Uint32(body[at:]))
	at += 4
	if size > int64(len(body)-at) {
		return label, body
	}
	// The name is the label the packager shows, or the last element of
	// the path the file came from. Both are in a code page the stream
	// does not name: bytes above 0x7F are read as Windows-1252.
	for _, raw := range [][]byte{shown, path} {
		s, _ := codepage.Decode(1252, raw)
		if i := strings.LastIndexAny(s, `\/`); i >= 0 {
			s = s[i+1:]
		}
		if s = clean.Label(s); s != "" {
			name = s
			break
		}
	}
	if name == "" {
		name = label
	}
	return name, body[at : at+int(size)]
}
