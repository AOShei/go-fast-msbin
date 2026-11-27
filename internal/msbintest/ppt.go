package msbintest

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"unicode/utf16"
)

// This file writes PowerPoint presentations ([MS-PPT]) for the tests, on
// its own from the published description: records, the texts and shapes
// of slides, and the stream with its edits and persist directories.

// Types of the records the tests write.
const (
	PPTDocument     = 0x03E8
	PPTSlide        = 0x03EE
	PPTNotes        = 0x03F0
	PPTEnvironment  = 0x03F2
	PPTMainMaster   = 0x03F8
	PPTList         = 0x07D0
	PPTExObjList    = 0x0409
	PPTDrawingGroup = 0x040B
	PPTDrawing      = 0x040C
	PPTFonts        = 0x07D5
	PPTSlideList    = 0x0FF0
	PPTHeadFoot     = 0x0FD9
	PPTExEmbed      = 0x0FCC
	PPTExHyperlink  = 0x0FD7
	PPTInteractive  = 0x0FF2
	PPTProgTags     = 0x1388
	PPTBinaryTag    = 0x138A
	PPTComment      = 0x2EE0
)

// PPTAtom is a record that holds data; PPTContainer (ArtContainer) one
// that holds records.
func PPTAtom(typ uint16, inst uint16, body ...byte) []byte { return Art(0, inst, typ, body...) }

// PPTContainer is a container with an instance.
func PPTContainer(typ uint16, inst uint16, children ...[]byte) []byte {
	return Art(0xF, inst, typ, Cat(children...)...)
}

// PPTCString is a text of a record: a name, a footer, an address.
func PPTCString(inst uint16, text string) []byte { return PPTAtom(0x0FBA, inst, utf16le(text)...) }

// PPTDocumentAtom gives the size of a slide in master units (576 to the
// inch), the number of the first slide, and whether title slides leave
// out the date, the footer and the number.
func PPTDocumentAtom(width, height uint32, first uint16, omitTitle bool) []byte {
	b := Cat(U32(width, height), U32(width, height), U32(1, 1), U32(0, 0), U16(first, 0))
	b = append(b, 0, 0, 0, 1)
	if omitTitle {
		b[37] = 1
	}
	return PPTAtom(0x03E9, 0, b...)
}

// PPTSlidePersist lists a slide, a master or a notes page: the object
// that holds it and its number.
func PPTSlidePersist(persist, id uint32) []byte {
	return PPTAtom(0x03F3, 0, U32(persist, 0, 0, id, 0)...)
}

// PPTSlideAtom says what a slide's layout is (0 for a title slide), the
// master it follows, its notes page, and whether it shows the shapes of
// its master.
func PPTSlideAtom(layout, master, notes uint32, followsMaster bool) []byte {
	flags := uint16(0)
	if followsMaster {
		flags = 1
	}
	return PPTAtom(0x03EF, 0, Cat(U32(layout), make([]byte, 8), U32(master, notes), U16(flags, 0))...)
}

// PPTHidden is the record of a slide that is not shown in the show.
func PPTHidden() []byte {
	return PPTAtom(0x03F9, 0, Cat(U32(0, 0), U16(0, 0x0004), U16(0, 0))...)
}

// PPTHeadFootAtom says what slides show at their foot: 0x01 a date, 0x02
// today's, 0x04 one that was given, 0x08 the slide's number, 0x10 a
// header, 0x20 a footer.
func PPTHeadFootAtom(flags uint16) []byte { return PPTAtom(0x0FDA, 0, U16(0, flags)...) }

// PPTText is a text: its kind (0 a title, 1 a body, 2 notes, 4 other),
// its characters in 16 bits, and the records that follow them.
func PPTText(kind uint32, text string, after ...[]byte) []byte {
	return Cat(PPTAtom(0x0F9F, 0, U32(kind)...), PPTAtom(0x0FA0, 0, utf16le(text)...), Cat(after...))
}

// PPTTextBytes is a text whose characters are kept in bytes.
func PPTTextBytes(kind uint32, text string, after ...[]byte) []byte {
	return Cat(PPTAtom(0x0F9F, 0, U32(kind)...), PPTAtom(0x0FA8, 0, []byte(text)...), Cat(after...))
}

// PPTParaRun is a run of paragraphs with the same properties: how many
// characters, their level, and whether they say they have a bullet (1),
// have none (-1) or do not say (0), with the bullet's character and font
// (-1 for none).
type PPTParaRun struct {
	Count  int
	Level  uint16
	Bullet int
	Char   rune
	Font   int
}

// PPTCharRun is a run of characters: how many, their font and the font
// of their symbols (-1 for none), and the entry of the PowerPoint 2000
// extension that is theirs.
type PPTCharRun struct {
	Count     int
	Font, Sym int
	Ext       uint16
}

func pfBytes(p PPTParaRun) (mask uint32, body []byte) {
	if p.Bullet != 0 {
		mask |= 0x0001
		flags := uint16(0)
		if p.Bullet > 0 {
			flags = 1
		}
		body = append(body, U16(flags)...)
	}
	if p.Char != 0 {
		mask |= 0x0080
		body = append(body, U16(uint16(p.Char))...)
	}
	if p.Font >= 0 {
		mask |= 0x0010
		body = append(body, U16(uint16(p.Font))...)
	}
	return mask, body
}

func cfBytes(c PPTCharRun) (mask uint32, body []byte) {
	if c.Ext != 0 {
		mask |= 0x0400
		body = append(body, U16(c.Ext<<10)...)
	}
	if c.Font >= 0 {
		mask |= 0x10000
		body = append(body, U16(uint16(c.Font))...)
	}
	if c.Sym >= 0 {
		mask |= 0x800000
		body = append(body, U16(uint16(c.Sym))...)
	}
	return mask, body
}

// PPTStyle is the record of the styles of a text: its runs of paragraphs,
// then its runs of characters. The counts cover the text and its end.
func PPTStyle(paras []PPTParaRun, chars []PPTCharRun) []byte {
	var b []byte
	for _, p := range paras {
		mask, body := pfBytes(p)
		b = append(b, Cat(U32(uint32(p.Count)), U16(p.Level), U32(mask), body)...)
	}
	for _, c := range chars {
		mask, body := cfBytes(c)
		b = append(b, Cat(U32(uint32(c.Count)), U32(mask), body)...)
	}
	return PPTAtom(0x0FA1, 0, b...)
}

// PPTMasterStyle is the record of the styles a master gives a kind of
// text: for each level, its paragraphs' and its characters' properties.
func PPTMasterStyle(kind uint16, levels ...PPTParaRun) []byte {
	b := U16(uint16(len(levels)))
	for i, p := range levels {
		if kind >= 5 {
			b = append(b, U16(uint16(i))...)
		}
		mask, body := pfBytes(p)
		b = append(b, Cat(U32(mask), body, U32(0))...)
	}
	return PPTAtom(0x0FA3, kind, b...)
}

// PPTFont is an entry of the fonts: its number and its name.
func PPTFont(index uint16, name string) []byte {
	b := make([]byte, 68)
	copy(b, utf16le(name))
	return PPTAtom(0x0FB7, index, b...)
}

// PPTField stands for a field at a place in a text: 0x0FD8 the slide's
// number, 0x0FFA the footer, 0x0FF8 the date that was given, 0x0FF7
// today's date.
func PPTField(typ uint16, at uint32) []byte { return PPTAtom(typ, 0, U32(at)...) }

// PPTLink makes the characters from one place to another of a text lead
// to the link with the given number; action is 4 for a link, 2 for one
// that starts a program, 1 for one that runs a macro.
func PPTLink(id uint32, action byte, from, to uint32) []byte {
	info := PPTAtom(0x0FF3, 0, Cat(U32(0, id), []byte{action, 0, 0, 0, 0, 0, 0, 0})...)
	return Cat(PPTContainer(PPTInteractive, 0, info), PPTAtom(0x0FDF, 0, U32(from, to)...))
}

// PPTHyperlink is an entry of the links: its number and where it leads.
func PPTHyperlink(id uint32, target string) []byte {
	return PPTContainer(PPTExHyperlink, 0, PPTAtom(0x0FD3, 0, U32(id)...), PPTCString(0, target), PPTCString(1, target))
}

// PPTShape is a shape of a slide.
type PPTShape struct {
	// Top, left, right and bottom, in master units; all zero for a shape
	// that says no place.
	Top, Left, Right, Bottom int16
	// Placeholder is the kind of placeholder the shape is, or 0.
	Placeholder byte
	// Text is the records of the shape's text; OutlineRef names a text of
	// the outline instead, by its number among the slide's, from 1.
	Text       []byte
	OutlineRef int
	Props      []ArtProp
	// Data is records of the shape's own beside its placeholder: a
	// reference to an embedded object, what a click does, tags.
	Data []byte
	// Child gives the shape's place in the coordinates of its group.
	Child *[4]int32
	// Group marks the first shape of a group, with the group's own
	// coordinates.
	Group *[4]int32
	Flags uint32
}

// Bytes writes the shape.
func (s PPTShape) Bytes() []byte {
	var parts [][]byte
	if s.Group != nil {
		parts = append(parts, Art(1, 0, 0xF009, U32(uint32(s.Group[0]), uint32(s.Group[1]), uint32(s.Group[2]), uint32(s.Group[3]))...))
	}
	parts = append(parts, Art(2, 1, 0xF00A, U32(1, 0x0A00|s.Flags)...))
	if len(s.Props) > 0 {
		parts = append(parts, ArtProps(s.Props...))
	}
	if s.Child != nil {
		parts = append(parts, Art(0, 0, 0xF00F, U32(uint32(s.Child[0]), uint32(s.Child[1]), uint32(s.Child[2]), uint32(s.Child[3]))...))
	}
	if s.Top != 0 || s.Left != 0 || s.Right != 0 || s.Bottom != 0 {
		parts = append(parts, Art(0, 0, 0xF010, U16(uint16(s.Top), uint16(s.Left), uint16(s.Right), uint16(s.Bottom))...))
	}
	var data []byte
	if s.Placeholder != 0 {
		data = append(data, PPTAtom(0x0BC3, 0, 0, 0, 0, 0, s.Placeholder, 0, 0, 0)...)
	}
	if data = append(data, s.Data...); len(data) > 0 {
		parts = append(parts, Art(0xF, 0, 0xF011, data...))
	}
	switch {
	case s.OutlineRef > 0:
		parts = append(parts, Art(0xF, 0, 0xF00D, PPTAtom(0x0F9E, 0, U32(uint32(s.OutlineRef-1))...)...))
	case s.Text != nil:
		parts = append(parts, Art(0xF, 0, 0xF00D, s.Text...))
	}
	return ArtContainer(0xF004, parts...)
}

// PPTGroup is a group of shapes: its own shape, then its shapes.
func PPTGroup(shapes ...PPTShape) []byte {
	var parts [][]byte
	for _, s := range shapes {
		parts = append(parts, s.Bytes())
	}
	return ArtContainer(0xF003, parts...)
}

// PPTShapes is the drawing of a slide: its shapes and groups, each as
// PPTShape.Bytes or PPTGroup wrote it.
func PPTShapes(shapes ...[]byte) []byte {
	top := ArtContainer(0xF004, Art(1, 0, 0xF009, make([]byte, 16)...), Art(2, 0, 0xF00A, U32(0, 0x0005)...))
	group := ArtContainer(0xF003, append([][]byte{top}, shapes...)...)
	return PPTContainer(PPTDrawing, 0, ArtContainer(0xF002, Art(0, 1, 0xF008, U32(uint32(len(shapes)+1), 0)...), group))
}

// PPTPictures is the drawing group of a presentation, whose store names
// the pictures, and the Pictures stream that holds them, each a PNG file.
func PPTPictures(pictures ...[]byte) (group, stream []byte) {
	var entries [][]byte
	for _, p := range pictures {
		blip := Art(0, 0x6E0, 0xF01E, Cat(make([]byte, 16), []byte{0xFF}, p)...)
		head := Cat([]byte{6, 6}, make([]byte, 16), U16(0xFF), U32(uint32(len(blip))), U32(1), U32(uint32(len(stream))), []byte{0, 0, 0, 0})
		entries = append(entries, Art(2, 6, 0xF007, head...))
		stream = append(stream, blip...)
	}
	return PPTContainer(PPTDrawingGroup, 0, ArtContainer(0xF000, Art(0, 0, 0xF006, make([]byte, 16)...), ArtContainer(0xF001, entries...))), stream
}

// PPTExObj lists an embedded object: the number shapes name it by, what
// made it (4 for Microsoft Graph), and the object that holds its storage.
func PPTExObj(id, subType, persist uint32) []byte {
	return PPTContainer(PPTExEmbed, 0, PPTAtom(0x0FCD, 0, U32(0, 0)...), PPTAtom(0x0FC3, 1, U32(1, 0, id, subType, persist, 0)...))
}

// PPTExObjRef is the record by which a shape names its embedded object.
func PPTExObjRef(id uint32) []byte { return PPTAtom(0x0BC1, 0, U32(id)...) }

// PPTStorage is the record of an object that holds a compound file,
// compressed or as it is.
func PPTStorage(file []byte, compressed bool) []byte {
	if !compressed {
		return PPTAtom(0x1011, 0, file...)
	}
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(file)
	w.Close()
	return PPTAtom(0x1011, 1, Cat(U32(uint32(len(file))), buf.Bytes())...)
}

// PPTVbaInfo names the object that holds the storage of the VBA project.
func PPTVbaInfo(persist uint32) []byte {
	return PPTContainer(0x03FF, 1, PPTAtom(0x0400, 2, U32(persist, 1, 2)...))
}

// PPTTags wraps records in the tags of a later PowerPoint: the name of
// the version that wrote them, and their data.
func PPTTags(name string, records ...[]byte) []byte {
	return PPTContainer(PPTProgTags, 0, PPTContainer(PPTBinaryTag, 0, PPTCString(0, name), PPTAtom(0x138B, 0, Cat(records...)...)))
}

// PPTAutoNumbers is the record of the PowerPoint 2000 extension of a
// text: for each entry, whether its paragraphs are numbered, with what
// scheme and from what number (scheme -1 for none said).
func PPTAutoNumbers(entries ...[3]int) []byte {
	var b []byte
	for _, e := range entries {
		mask, body := uint32(0), []byte(nil)
		if e[0] != 0 {
			mask |= 0x2000000
			body = append(body, U16(1)...)
		}
		if e[1] >= 0 {
			mask |= 0x1000000
			body = append(body, U16(uint16(e[1]), uint16(e[2]))...)
		}
		b = append(b, Cat(U32(mask), body, U32(0), U32(0))...)
	}
	return PPTAtom(0x0FAC, 0, b...)
}

// PPTOutlineNumbers is the extension for the texts of the outline: for a
// slide and a kind of text, its automatic numbers.
func PPTOutlineNumbers(slide uint32, kind uint32, numbers []byte) []byte {
	return PPTContainer(0x0FAE, 0, PPTAtom(0x0FAF, uint16(kind), U32(slide, kind)...), numbers)
}

// PPTCommentOn is a comment on a slide: its author, its text and when it
// was written.
func PPTCommentOn(author, text string, year, month, day, hour, minute uint16) []byte {
	return PPTContainer(PPTComment, 0, PPTCString(0, author), PPTCString(1, text), PPTCString(2, "A"),
		PPTAtom(0x2EE1, 0, Cat(U32(1), U16(year, month, 0, day, hour, minute, 0, 0), U32(0, 0))...))
}

// PPTFile assembles the stream of a presentation: objects with their
// persist numbers, written by one or more edits.
type PPTFile struct {
	// Summary is the property sets an encrypted presentation keeps,
	// encrypted, in a stream of their own: the name of each and its
	// bytes.
	Summary map[string][]byte
	stream  []byte
	last    uint32 // where the last edit is
	seed    uint32
}

// Edit writes objects, each under a persist number, with the directory
// that says where they are and the record of the edit. doc is the number
// of the document's object.
func (f *PPTFile) Edit(doc uint32, objects map[uint32][]byte) {
	var ids []uint32
	for id := range objects {
		ids = append(ids, id)
	}
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			if ids[j] < ids[i] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	var dir []byte
	for _, id := range ids {
		dir = append(dir, U32(id|1<<20, uint32(len(f.stream)))...)
		f.stream = append(f.stream, objects[id]...)
		f.seed = max(f.seed, id)
	}
	at := uint32(len(f.stream))
	f.stream = append(f.stream, PPTAtom(0x1772, 0, dir...)...)
	edit := uint32(len(f.stream))
	f.stream = append(f.stream, PPTAtom(0x0FF5, 0, Cat(U32(0x100), U16(0), []byte{0, 3}, U32(f.last, at, doc, f.seed), U16(1, 0))...)...)
	f.last = edit
}

// Streams returns the streams of the presentation's compound file.
func (f *PPTFile) Streams() map[string][]byte {
	user := Cat(PPTAtom(0x0FF6, 0, Cat(U32(20, 0xE391C05F, f.last), U16(0, 0x03F4), []byte{3, 0, 0, 0})...), U32(8))
	return map[string][]byte{"PowerPoint Document": bytes.Clone(f.stream), "Current User": user}
}

// utf16le is s in UTF-16, little-endian.
func utf16le(s string) []byte {
	var out []byte
	for _, u := range utf16.Encode([]rune(s)) {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}
