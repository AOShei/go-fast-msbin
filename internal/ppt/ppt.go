// Package ppt reads PowerPoint presentations in the binary format of
// PowerPoint 97 to 2003 ([MS-PPT]): the PowerPoint Document stream, a tree
// of records, of which the presentation as it now stands is found through
// the edits the Current User stream leads to.
package ppt

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"io"
	"sort"
	"strconv"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/crypt"
	"github.com/AOShei/go-fast-msbin/internal/officeart"
	"github.com/AOShei/go-fast-msbin/internal/stored"
	"github.com/AOShei/go-fast-msbin/internal/symbol"
	"github.com/AOShei/go-fast-msbin/internal/unit"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

var le = binary.LittleEndian

// Record types ([MS-PPT] 2.13.24).
const (
	rtDocument            = 0x03E8
	rtDocumentAtom        = 0x03E9
	rtSlide               = 0x03EE
	rtSlideAtom           = 0x03EF
	rtNotes               = 0x03F0
	rtSlidePersistAtom    = 0x03F3
	rtSlideShowInfo       = 0x03F9
	rtVbaInfo             = 0x03FF
	rtVbaInfoAtom         = 0x0400
	rtExObjList           = 0x0409
	rtSoundCollection     = 0x07E4
	rtSound               = 0x07E6
	rtExAviMovie          = 0x1006
	rtExMCIMovie          = 0x1007
	rtExMIDIAudio         = 0x100D
	rtExCDAudio           = 0x100E
	rtExWAVAudioLink      = 0x1010
	rtPPDrawingGroup      = 0x040B
	rtPPDrawing           = 0x040C
	rtPlaceholderAtom     = 0x0BC3
	rtOutlineTextRef      = 0x0F9E
	rtTextHeaderAtom      = 0x0F9F
	rtTextCharsAtom       = 0x0FA0
	rtStyleTextPropAtom   = 0x0FA1
	rtTextBytesAtom       = 0x0FA8
	rtCString             = 0x0FBA
	rtExObjRefAtom        = 0x0BC1
	rtExOleObjAtom        = 0x0FC3
	rtExEmbed             = 0x0FCC
	rtSlideListWithText   = 0x0FF0
	rtUserEditAtom        = 0x0FF5
	rtExOleObjStg         = 0x1011
	rtPersistDirectory    = 0x1772
	rtCryptSession        = 0x2F14
	rtProgTags            = 0x1388
	rtList                = 0x07D0
	rtBinaryTagData       = 0x138B
	rtStyleTextProp9      = 0x0FAC
	rtOutlineTextProps9   = 0x0FAE
	rtOutlineTextHeader9  = 0x0FAF
	rtExHyperlinkAtom     = 0x0FD3
	rtExHyperlink         = 0x0FD7
	rtSlideNumberMC       = 0x0FD8
	rtHeadersFooters      = 0x0FD9
	rtHeadersFootersAtom  = 0x0FDA
	rtTxInteractiveInfo   = 0x0FDF
	rtInteractiveInfo     = 0x0FF2
	rtInteractiveInfoAtom = 0x0FF3
	rtDateTimeMC          = 0x0FF7
	rtGenericDateMC       = 0x0FF8
	rtHeaderMC            = 0x0FF9
	rtFooterMC            = 0x0FFA
	rtRTFDateTimeMC       = 0x1015
	rtEnvironment         = 0x03F2
	rtFontEntityAtom      = 0x0FB7
	rtTxMasterStyleAtom   = 0x0FA3
	rtComment10           = 0x2EE0
	rtComment10Atom       = 0x2EE1
)

// Bounds on a presentation.
const (
	maxEdits   = 1 << 16 // edits followed back from the last
	maxPersist = 1 << 22 // objects of the persist directory
	maxFoot    = 64      // places for the date, the footer and the number kept of a master
)

// Config is what the caller gives beyond the streams.
type Config struct {
	// Pictures is the Pictures stream, which holds the pictures of the
	// presentation's shapes, or nil.
	Pictures []byte
	// CurrentEdit is where the Current User stream says the last edit
	// is in the PowerPoint Document stream.
	CurrentEdit uint32
	// Stored reads into the stored part what the file stores besides
	// what this reader finds (the source of its macros), or is nil.
	Stored func(ctx context.Context, part *stored.Part)
	// Chart reads the compound file of a Microsoft Graph chart into the
	// blocks of the chart, or reports that it is none. cut says the text
	// the document may hold ran out.
	Chart func(ctx context.Context, storage []byte) (blocks []gofastcore.Block, cut, ok bool)
	// Equation reads the compound file of an equation of the Equation
	// Editor into its linear form, and counts the characters Unicode has
	// nothing for. ok is false for a file that holds no equation that
	// can be read.
	Equation  func(storage []byte) (text string, unmapped int, ok bool)
	HasStored bool
}

// slideRef is a slide as the presentation lists it.
type slideRef struct {
	persist uint32
	id      uint32
	outline []byte // the records of its texts in the outline of the presentation
}

// Deck is a presentation opened for reading a slide at a time.
type Deck struct {
	data             []byte
	opts             *gofastcore.Options
	cfg              Config
	persist          map[uint32]uint32 // where each object is in the stream
	slides           []slideRef
	masterRefs       []slideRef
	notes            map[uint32]uint32 // the persist number of each notes page, by its number
	width            float64           // of a slide, in points
	height           float64
	store            []officeart.StoreEntry
	objects          []uint32 // the persist numbers of the storages of embedded objects, the macros' among them
	vba              uint32   // the persist number of the storage of the VBA project, or 0
	damaged          []string
	found            bool // the last edit was found by looking through the stream
	docLost          warn.Lost
	symbols          []symbol.Font         // of each font, if it is a symbol font
	links            map[uint32]string     // the addresses of the links, by number
	byRef            map[uint32]uint32     // the storage of each embedded object, by the number shapes name it by
	graphs           map[uint32]bool       // the storages that hold a Microsoft Graph chart
	equations        map[uint32]bool       // the storages that are said to hold an equation
	unread           map[string]int        // content of kinds that are not read: sounds, videos
	first            int                   // the number of the first slide
	omitTitle        bool                  // title slides show no date, footer or number
	slideHF, notesHF headFoot              // what slides and notes pages show at head and foot
	fonts            []string              // the names of the fonts, by number
	masters          map[uint32]*master    // what each master gives its slides, by its number; 0 is the first
	outline9         map[outlineKey][]auto // the automatic numbers of the outline's texts
}

// directory follows the edits from the last back to the first: each
// names the persist directory of what it changed, and the last to name
// an object has it as it now is ([MS-PPT] 2.1.2). It returns the number
// of the document's object, and of the object that holds what an
// encrypted presentation says of its encryption, or 0.
func (d *Deck) directory(at uint32) (doc, session uint32) {
	if edit, ok := d.rec(at); at == 0 || !ok || edit.Type != rtUserEditAtom {
		// The Current User stream does not lead to an edit: the last
		// edit the stream holds is used.
		at = 0
		for b := d.data; ; {
			r, rest, ok := officeart.Next(b)
			if !ok {
				break
			}
			if r.Type == rtUserEditAtom {
				at = uint32(len(d.data) - len(b))
			}
			b = rest
		}
		d.found = at != 0
	}
	seen := map[uint32]bool{}
	for n := 0; at != 0 && n < maxEdits && !seen[at]; n++ {
		seen[at] = true
		edit, ok := d.rec(at)
		if !ok || edit.Type != rtUserEditAtom || len(edit.Body) < 20 {
			d.damaged = append(d.damaged, "the chain of edits")
			break
		}
		if doc == 0 {
			doc = le.Uint32(edit.Body[16:])
			if len(edit.Body) >= 32 {
				session = le.Uint32(edit.Body[28:])
			}
		}
		if dir, ok := d.rec(le.Uint32(edit.Body[12:])); ok && dir.Type == rtPersistDirectory {
			b := dir.Body
			for len(b) >= 4 {
				head := le.Uint32(b)
				id, count := head&0xFFFFF, int(head>>20)
				b = b[4:]
				for k := 0; k < count && len(b) >= 4; k, b = k+1, b[4:] {
					if _, later := d.persist[id+uint32(k)]; !later && len(d.persist) < maxPersist {
						d.persist[id+uint32(k)] = le.Uint32(b)
					}
				}
			}
		} else {
			d.damaged = append(d.damaged, "a persist directory")
		}
		at = le.Uint32(edit.Body[8:])
	}
	return doc, session
}

// Decrypt decrypts in place the objects of an encrypted presentation and
// its stream of pictures ([MS-PPT] 2.3.7): each object, its header with
// it, with the key of its number; of a picture each field on its own
// with the first key. open is given what the presentation says of its
// encryption, and returns the key or why there is none.
func Decrypt(data, pictures []byte, currentEdit uint32, open func(header []byte) (*crypt.Key, error)) error {
	d := &Deck{data: data, persist: map[uint32]uint32{}}
	_, session := d.directory(currentEdit)
	info, ok := d.object(session)
	if !ok || session == 0 || info.Type != rtCryptSession {
		return crypt.ErrHeader
	}
	key, err := open(info.Body)
	if err != nil {
		return err
	}
	// The objects in the order of their places: an object ends where the
	// next begins at the latest, so that what is decrypted is no more
	// than the stream holds, whatever the lengths in it say.
	type object struct{ at, id uint32 }
	objects := make([]object, 0, len(d.persist))
	for id, at := range d.persist {
		if id != session && int64(at)+8 <= int64(len(data)) {
			objects = append(objects, object{at, id})
		}
	}
	sort.Slice(objects, func(i, j int) bool {
		if objects[i].at != objects[j].at {
			return objects[i].at < objects[j].at
		}
		return objects[i].id < objects[j].id
	})
	for i, o := range objects {
		if i > 0 && objects[i-1].at == o.at {
			continue // named twice
		}
		end := int64(len(data))
		if i+1 < len(objects) && objects[i+1].at > o.at {
			end = int64(objects[i+1].at)
		}
		if int64(o.at)+8 > end {
			continue
		}
		c := key.Block(data[o.at:o.at+8], o.id)
		size := int64(le.Uint32(data[o.at+4:]))
		body := data[o.at+8 : min(int64(o.at)+8+size, end)]
		c.XORKeyStream(body, body)
	}
	// The pictures: a record's header, the identifiers of the picture,
	// what is said of a metafile or the one byte before another kind of
	// picture, and the picture, each encrypted from the start of the
	// first key's run.
	for b := pictures; len(b) >= 8; {
		key.Block(b[:8], 0)
		inst, typ, size := le.Uint16(b)>>4, le.Uint16(b[2:]), int64(le.Uint32(b[4:]))
		body := b[8:min(8+size, int64(len(b)))]
		b = b[8+len(body):]
		field := func(n int) {
			n = min(n, len(body))
			key.Block(body[:n], 0)
			body = body[n:]
		}
		field(16)
		if inst&1 != 0 {
			field(16) // a second identifier
		}
		if typ >= 0xF01A && typ <= 0xF01C {
			field(34)
		} else {
			field(1)
		}
		field(len(body))
	}
	return nil
}

// rec reads the record at offset at of the stream.
func (d *Deck) rec(at uint32) (officeart.Rec, bool) {
	if int64(at)+8 > int64(len(d.data)) {
		return officeart.Rec{}, false
	}
	r, _, ok := officeart.Next(d.data[at:])
	return r, ok
}

// object reads the record of the object with the given persist number.
func (d *Deck) object(id uint32) (officeart.Rec, bool) {
	at, ok := d.persist[id]
	if !ok {
		return officeart.Rec{}, false
	}
	return d.rec(at)
}

// Open reads what the slides of the presentation share: which objects
// the file now holds, the list of slides, the size of a slide and the
// store of pictures. It does not fail: a presentation whose structure
// cannot be read has one part that says so.
func Open(data []byte, opts *gofastcore.Options, cfg Config) *Deck {
	d := &Deck{data: data, opts: opts, cfg: cfg, persist: map[uint32]uint32{}, notes: map[uint32]uint32{}, links: map[uint32]string{}, byRef: map[uint32]uint32{}, graphs: map[uint32]bool{}, equations: map[uint32]bool{}, unread: map[string]int{}, first: 1, outline9: map[outlineKey][]auto{}}
	doc, _ := d.directory(cfg.CurrentEdit)
	root, ok := d.object(doc)
	if !ok || root.Type != rtDocument {
		d.damaged = append(d.damaged, "the document")
		return d
	}
	// The document: the size of a slide, the lists of slides and of notes
	// pages with the texts of each, the pictures, the embedded objects.
	d.masters = map[uint32]*master{}
	env := map[int][]paraStyle{}
	envAtoms := map[int][]byte{}
	b := root.Body
	for {
		r, rest, ok := officeart.Next(b)
		if !ok {
			break
		}
		b = rest
		switch r.Type {
		case rtDocumentAtom:
			// The size of a slide, in units of 576 to the inch.
			if len(r.Body) >= 8 {
				d.width = float64(int32(le.Uint32(r.Body))) / 8
				d.height = float64(int32(le.Uint32(r.Body[4:]))) / 8
				if d.width <= 0 || d.height <= 0 {
					d.width, d.height = 0, 0 // no size that a slide can have
				}
			}
			if len(r.Body) >= 38 {
				d.first, d.omitTitle = int(le.Uint16(r.Body[32:])), r.Body[37] != 0
			}
		case rtHeadersFooters:
			switch r.Inst {
			case 3:
				d.slideHF = readHeadFoot(r)
			case 4:
				d.notesHF = readHeadFoot(r)
			}
		case rtSlideListWithText:
			d.slideList(r)
		case rtEnvironment:
			// The fonts, and the styles of text that is in no placeholder.
			officeart.Walk(r.Body, func(c officeart.Rec, depth int) bool {
				switch c.Type {
				case rtFontEntityAtom:
					if len(c.Body) >= 64 && len(d.fonts) < 1<<12 {
						name := wide(c.Body[:64])
						for i, ch := range name {
							if ch == 0 {
								name = name[:i]
								break
							}
						}
						for len(d.fonts) <= int(c.Inst) && len(d.fonts) < 1<<12 {
							d.fonts = append(d.fonts, "")
						}
						if int(c.Inst) < len(d.fonts) {
							d.fonts[c.Inst] = name
						}
					}
				case rtTxMasterStyleAtom:
					envAtoms[int(c.Inst)] = c.Body
				}
				return true
			})
		case rtProgTags, rtList:
			// What PowerPoint 2000 added, which the list of what the
			// document holds besides its slides keeps: the automatic
			// numbers of the texts of the outline, by slide and kind of
			// text.
			binaryTags(r.Body, func(t officeart.Rec) {
				if t.Type != rtOutlineTextProps9 {
					return
				}
				b := t.Body
				var key outlineKey
				for {
					c, rest, ok := officeart.Next(b)
					if !ok {
						break
					}
					switch {
					case c.Type == rtOutlineTextHeader9 && len(c.Body) >= 8:
						key = outlineKey{le.Uint32(c.Body), int(int32(le.Uint32(c.Body[4:])))}
					case c.Type == rtStyleTextProp9 && len(d.outline9) < 1<<16:
						d.outline9[key] = autos(c.Body)
					}
					b = rest
				}
			})
		case rtSoundCollection:
			// The sounds the file holds, which shapes and the passing
			// from slide to slide play: not read.
			officeart.Walk(r.Body, func(c officeart.Rec, _ int) bool {
				if c.Type == rtSound {
					d.unread[unit.UnreadSounds]++
				}
				return true
			})
		case rtPPDrawingGroup:
			var past int
			d.store, past = officeart.Store(r.Body, warn.MaxPictures)
			d.docLost.Add(warn.CapPictures, int64(past))
		case rtExObjList:
			// Embedded objects: each names the storage that holds it.
			officeart.Walk(r.Body, func(c officeart.Rec, depth int) bool {
				if c.Type == rtExOleObjAtom && len(c.Body) >= 24 && len(d.objects) < warn.MaxObjects {
					// An embedded object: the number shapes name it by,
					// what made it, and the storage that holds it. A
					// Microsoft Graph chart and an equation are read with
					// the slide that shows them.
					at := le.Uint32(c.Body[16:])
					d.objects = append(d.objects, at)
					d.byRef[le.Uint32(c.Body[8:])] = at
					if le.Uint32(c.Body[12:]) == 4 && cfg.Chart != nil {
						d.graphs[at] = true
					}
					if le.Uint32(c.Body[12:]) == 6 && cfg.Equation != nil {
						d.equations[at] = true
					}
				}
				// Videos, and sounds kept outside the file or played from
				// a disc: not read.
				switch c.Type {
				case rtExAviMovie, rtExMCIMovie:
					d.unread[unit.UnreadVideos]++
				case rtExMIDIAudio, rtExCDAudio, rtExWAVAudioLink:
					d.unread[unit.UnreadSounds]++
				}
				if c.Type == rtExHyperlink && len(d.links) < 1<<16 {
					// A link: its number, and where it leads.
					id, target, ok := uint32(0), "", false
					officeart.Walk(c.Body, func(h officeart.Rec, _ int) bool {
						switch {
						case h.Type == rtExHyperlinkAtom && len(h.Body) >= 4:
							id, ok = le.Uint32(h.Body), true
						case h.Type == rtCString && h.Inst == 1:
							target = wide(h.Body)
						case h.Type == rtCString && h.Inst == 0 && target == "":
							target = wide(h.Body)
						}
						return true
					})
					if target = clean.Address(target); ok && target != "" {
						d.links[id] = target
					}
					return false
				}
				return true
			})
		case rtVbaInfo:
			officeart.Walk(r.Body, func(c officeart.Rec, depth int) bool {
				if c.Type == rtVbaInfoAtom && len(c.Body) >= 4 {
					d.vba = le.Uint32(c.Body)
				}
				return true
			})
		}
	}
	applyStyles(env, envAtoms)
	d.symbols = make([]symbol.Font, len(d.fonts))
	for i, name := range d.fonts {
		d.symbols[i] = symbol.ByName(name)
	}
	// The masters: the styles each gives the kinds of text, over those of
	// the presentation or of the master it follows, and what it puts on
	// every slide.
	for i, ref := range d.masterRefs {
		m := &master{styles: map[int][]paraStyle{}, has: map[same]bool{}}
		obj, ok := d.object(ref.persist)
		if ok {
			officeart.Walk(obj.Body, func(c officeart.Rec, depth int) bool {
				if c.Type == rtSlideAtom && len(c.Body) >= 22 {
					// A title master follows a master.
					if id := le.Uint32(c.Body[12:]); id != 0 {
						m.parent = d.masters[id]
					}
					m.follow = le.Uint16(c.Body[20:])&1 != 0
				}
				return depth == 0 && c.Type != rtPPDrawing
			})
		}
		from := env
		if m.parent != nil {
			from = m.parent.styles
		}
		for kind, v := range from {
			m.styles[kind] = v
		}
		if ok {
			atoms := map[int][]byte{}
			b := obj.Body
			for {
				c, rest, ok := officeart.Next(b)
				if !ok {
					break
				}
				if c.Type == rtTxMasterStyleAtom {
					atoms[int(c.Inst)] = c.Body
				}
				b = rest
			}
			applyStyles(m.styles, atoms)
			// Its shapes: the text and the pictures of its own, which
			// every slide that follows it shows, and the places of the
			// date, the footer and the number of a slide.
			r := &slideReader{d: d, ref: &d.masterRefs[i], styles: m.styles}
			// A master that holds the same thing twice in the same place
			// shows it once.
			for _, it := range r.shapes(drawingOf(obj)) {
				switch {
				case it.placeholder == phMasterDate || it.placeholder == phMasterNumber || it.placeholder == phMasterFooter:
					if key := sameAs(&it); !m.has[key] && len(m.foot) < maxFoot {
						m.has[key] = true
						m.foot = append(m.foot, it)
					}
				case it.placeholder < 0 && (len(it.paras) > 0 || it.blip > 0 || it.fields) && it.rows == nil:
					if key := sameAs(&it); !m.has[key] {
						m.has[key] = true
						m.static = append(m.static, it)
					}
				}
			}
		}
		d.masters[ref.id] = m
		if i == 0 {
			d.masters[0] = m
		}
	}
	if d.masters[0] == nil {
		d.masters[0] = &master{styles: env}
	}
	return d
}

// applyStyles reads the styles a master gives the kinds of text over
// those it has from what it follows: first the kinds that stand alone,
// then those that follow another kind.
func applyStyles(styles map[int][]paraStyle, atoms map[int][]byte) {
	for kind := 0; kind < 5; kind++ {
		if b, ok := atoms[kind]; ok {
			styles[kind] = masterStyles(b, kind, nil)
		}
	}
	for kind, b := range atoms {
		switch kind {
		case textCenterBody, textHalfBody, textQuarterBody:
			styles[kind] = masterStyles(b, kind, styles[textBody])
		case textCenterTitle:
			styles[kind] = masterStyles(b, kind, styles[textTitle])
		}
	}
}

// master is what a master gives the slides that follow it.
type master struct {
	styles map[int][]paraStyle // of the kinds of text
	static []item              // the text and the pictures of its own
	foot   []item              // the places of the date, the footer and the slide's number
	parent *master             // the master a title master follows
	follow bool                // it shows its parent's shapes too
	has    map[same]bool       // what it holds, for telling a repeat
}

// outlineKey names a text of the outline: the slide's number and the
// kind of text.
type outlineKey struct {
	slide uint32
	kind  int
}

// auto is what the extension of PowerPoint 2000 says of the paragraphs
// of a run of text: whether they are numbered, how, and from what.
type auto struct {
	on     bool
	scheme int
	start  int
}

// autos reads a StyleTextProp9Atom ([MS-PPT] 2.9.67): for each run of
// the text that names it, the automatic numbering of its paragraphs.
func autos(b []byte) []auto {
	var out []auto
	u16 := func() int {
		if len(b) < 2 {
			b = nil
			return 0
		}
		v := int(le.Uint16(b))
		b = b[2:]
		return v
	}
	u32 := func() uint32 {
		if len(b) < 4 {
			b = nil
			return 0
		}
		v := le.Uint32(b)
		b = b[4:]
		return v
	}
	for len(b) >= 12 && len(out) < 16 {
		a := auto{scheme: 3, start: 1}
		mask := u32()
		if mask&0x800000 != 0 {
			u16() // the picture of the bullet
		}
		if mask&0x2000000 != 0 {
			a.on = u16() != 0
		}
		if mask&0x1000000 != 0 {
			a.scheme, a.start = u16(), u16()
		}
		// The properties of the characters and of the language follow,
		// each a mask and what it says is there.
		for k := 0; k < 2; k++ {
			mask := u32()
			for _, f := range []struct {
				bit  uint32
				size int
			}{{0x1, 2}, {0x2, 2}, {0x4, 2}, {0x40, 2}, {0x20, 4}, {0x100000, 4}} {
				if mask&f.bit != 0 && len(b) >= f.size {
					b = b[f.size:]
				} else if mask&f.bit != 0 {
					b = nil
				}
			}
			if mask&0x200 != 0 {
				n := int(u32())
				if n < 0 || 4*n > len(b) {
					b = nil
				} else {
					b = b[4*n:]
				}
			}
		}
		out = append(out, a)
	}
	return out
}

// binaryTags calls fn for each record in the data of the binary tags
// among recs, which programs later than PowerPoint 97 keep what they
// added in ([MS-PPT] 2.3.4).
func binaryTags(recs []byte, fn func(officeart.Rec)) {
	officeart.Walk(recs, func(c officeart.Rec, _ int) bool {
		if c.Type == rtBinaryTagData {
			b := c.Body
			for {
				r, rest, ok := officeart.Next(b)
				if !ok {
					break
				}
				fn(r)
				b = rest
			}
		}
		return true
	})
}

// symbol returns the symbol font the font with the given number is, or
// none.
func (d *Deck) symbol(font int) symbol.Font {
	if font < 0 || font >= len(d.symbols) {
		return symbol.None
	}
	return d.symbols[font]
}

// slideList reads a list of slides, of masters or of notes pages: for
// each, the object that holds it, and after it the texts the outline
// keeps for it.
func (d *Deck) slideList(list officeart.Rec) {
	b := list.Body
	var cur *slideRef
	var from []byte // what follows the slide's entry
	end := func(rest []byte) {
		if cur != nil {
			cur.outline = from[:len(from)-len(rest)]
		}
	}
	for {
		r, rest, ok := officeart.Next(b)
		if !ok {
			break
		}
		if r.Type == rtSlidePersistAtom && len(r.Body) >= 16 {
			end(b)
			cur = nil
			ref := slideRef{persist: le.Uint32(r.Body), id: le.Uint32(r.Body[12:])}
			switch list.Inst {
			case 0: // the slides
				if len(d.slides) < warn.MaxSlides {
					d.slides = append(d.slides, ref)
					cur, from = &d.slides[len(d.slides)-1], rest
				} else {
					d.docLost.Add(warn.CapSlides, 1)
				}
			case 1: // the masters
				if len(d.masterRefs) < 1<<10 {
					d.masterRefs = append(d.masterRefs, ref)
				}
			case 2: // the notes pages
				d.notes[ref.id] = ref.persist
			}
		}
		b = rest
	}
	end(b)
}

// maxExpect bounds the room made beforehand for an object that says how
// large it is: a record can say anything.
const maxExpect = 64 << 20

// storage returns the compound file an ExOleObjStg record holds, which
// is kept compressed where the record's instance says so. limit bounds
// what it may expand to; ok is false where it is over, or cannot be read.
func (d *Deck) storage(id uint32, limit int64) (data []byte, compressed, ok bool) {
	r, found := d.object(id)
	if !found || r.Type != rtExOleObjStg {
		return nil, false, false
	}
	if r.Inst != 1 {
		return r.Body, false, true // as it is in the stream: nothing is expanded
	}
	if len(r.Body) < 4 {
		return nil, true, false
	}
	zr, err := zlib.NewReader(bytes.NewReader(r.Body[4:]))
	if err != nil {
		return nil, true, false
	}
	defer zr.Close()
	// Room for what the record says it expands to, where that is
	// within the limit, so that the bytes are not copied as they grow.
	// The limit is what is left of a budget, which may be overdrawn.
	room := max(min(int64(le.Uint32(r.Body)), limit, maxExpect), 0)
	out := bytes.NewBuffer(make([]byte, 0, room+bytes.MinRead))
	_, err = out.ReadFrom(io.LimitReader(zr, limit+1))
	if int64(out.Len()) > limit || err != nil && out.Len() == 0 {
		return nil, true, false
	}
	return out.Bytes(), true, true
}

// Len returns the number of parts: the slides, one part for a
// presentation with none, and the stored part if there is one.
func (d *Deck) Len() int {
	if i := d.Stored(); i >= 0 {
		return i + 1
	}
	return max(len(d.slides), 1)
}

// Stored returns the index of the stored part, or -1.
func (d *Deck) Stored() int {
	if !d.cfg.HasStored && d.vba == 0 {
		return -1
	}
	return max(len(d.slides), 1)
}

// Noun names the parts of a presentation.
func (d *Deck) Noun() string { return "slides" }

// Begin starts a pass over the presentation.
func (d *Deck) Begin() {}

// Limited reports whether a limit cut short what the slides share.
func (d *Deck) Limited() bool { return false }

// Macros reports whether the presentation holds a VBA project.
func (d *Deck) Macros() bool { return d.vba != 0 }

// Summary turns what was found while the slides were read into warnings
// about the document.
func (d *Deck) Summary(tallies []*unit.Tally) []gofastcore.Warning {
	out := d.docLost.Warnings()
	if d.found {
		out = append(out, gofastcore.Warning{Code: gofastcore.WarnInputRepaired, Severity: gofastcore.SeverityWarning,
			Message: "the Current User stream does not say where the last edit of the presentation is; the last edit in the PowerPoint Document stream is used"})
	}
	programs := 0
	for _, t := range tallies {
		programs += t.Programs
	}
	if programs > 0 {
		out = append(out, gofastcore.Warning{Code: gofastcore.WarnInputActiveContent, Severity: gofastcore.SeverityInfo,
			Message: strconv.Itoa(programs) + " actions on slides that start a program or run a macro; none was run"})
	}
	if len(d.unread) > 0 {
		tallies = append(tallies[:len(tallies):len(tallies)], &unit.Tally{Unread: d.unread})
	}
	return append(out, unit.Summarize(tallies, d.damaged)...)
}

// Object is an embedded object of a presentation, as a file: a compound
// file. Data holds it whole. Where it is only to be listed it may not
// have been expanded: then Data is nil and Peek reads it, expanding as
// much of it as is read, and Size is what it says it expands to. An
// object with neither could not be read, or is over what the document
// may expand.
type Object struct {
	ID   uint32
	Data []byte
	Peek io.ReaderAt
	Size int64
}

// Objects returns the embedded objects of the presentation as files.
// Left out are those read elsewhere: the VBA project, Microsoft Graph
// charts, and equations that can be read. What they expand to is taken
// from the decode budget in ctx; an object over it is given with no
// bytes. With read false the objects are to be listed and not handed on,
// and a compressed one is expanded only as far as it is looked at.
func (d *Deck) Objects(ctx context.Context, read bool) (objects []Object, limited bool) {
	decoded := gofastcore.DecodeBudgetFrom(ctx)
	seen := map[uint32]bool{}
	list := append([]uint32(nil), d.objects...)
	sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
	for _, id := range list {
		if seen[id] || id == d.vba || d.graphs[id] {
			continue
		}
		seen[id] = true
		left := int64(1) << 40
		if decoded != nil {
			left = decoded.Left()
		}
		if !read && !d.equations[id] {
			if o, compressed, ok := d.peek(id, left); ok {
				if compressed && !decoded.Take(o.Size) {
					limited = true
					o = Object{ID: id}
				}
				objects = append(objects, o)
				continue
			} else if compressed {
				limited = true
			}
			objects = append(objects, Object{ID: id})
			continue
		}
		data, compressed, ok := d.storage(id, left)
		if !ok || compressed && !decoded.Take(int64(len(data))) {
			limited = limited || compressed
			data = nil
		}
		if data != nil && d.equations[id] {
			// An equation that is read is on its slide, and no file.
			if _, _, ok := d.cfg.Equation(data); ok {
				continue
			}
		}
		objects = append(objects, Object{ID: id, Data: data, Size: int64(len(data))})
	}
	return objects, limited
}

// peek returns the object an ExOleObjStg record holds without expanding
// a compressed one: it is expanded as it is read. ok is false where it
// says it expands to more than limit, or nothing of it can be expanded.
func (d *Deck) peek(id uint32, limit int64) (o Object, compressed, ok bool) {
	r, found := d.object(id)
	if !found || r.Type != rtExOleObjStg {
		return Object{}, false, false
	}
	if r.Inst != 1 {
		return Object{ID: id, Data: r.Body, Size: int64(len(r.Body))}, false, true
	}
	if len(r.Body) < 4 {
		return Object{}, true, false
	}
	size := int64(le.Uint32(r.Body))
	zr, err := zlib.NewReader(bytes.NewReader(r.Body[4:]))
	if err != nil || size > limit {
		return Object{}, true, false
	}
	l := &lazy{zr: zr, size: size}
	// The first of it: an object of which nothing expands is none.
	var first [1]byte
	if n, _ := l.ReadAt(first[:], 0); n == 0 {
		return Object{}, true, false
	}
	return Object{ID: id, Peek: l, Size: size}, true, true
}

// lazy reads a compressed object, expanding it as far as it is read and
// no further. It is read by one goroutine.
type lazy struct {
	zr   io.Reader
	buf  []byte
	size int64 // what the object says it expands to
	done bool
}

func (l *lazy) ReadAt(p []byte, off int64) (int, error) {
	end := min(off+int64(len(p)), l.size)
	for int64(len(l.buf)) < end && !l.done {
		have := int64(len(l.buf))
		more := min(max(end-have, 64<<10), l.size-have)
		l.buf = append(l.buf, make([]byte, more)...)
		n, err := io.ReadFull(l.zr, l.buf[have:])
		l.buf = l.buf[:have+int64(n)]
		if err != nil {
			l.done = true
		}
	}
	if off < 0 || off >= int64(len(l.buf)) {
		return 0, io.EOF
	}
	n := copy(p, l.buf[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// Read reads the slide with the given index into a part.
func (d *Deck) Read(ctx context.Context, index int) *unit.Result {
	if index == d.Stored() {
		part := stored.NewPart(ctx, index+1)
		if d.vba != 0 {
			d.macros(ctx, part)
		}
		if d.cfg.Stored != nil {
			d.cfg.Stored(ctx, part)
		}
		limited := part.Finish()
		return &unit.Result{Part: part.Part, Tally: unit.Tally{Damaged: part.Damaged, Limited: limited}}
	}
	if len(d.slides) == 0 {
		res := &unit.Result{Part: gofastcore.Part{Kind: gofastcore.PartSlide, Number: 1}}
		msg := "the presentation lists no slides"
		if len(d.damaged) > 0 {
			msg = "the presentation's slides cannot be found: " + d.damaged[0] + " cannot be read"
		}
		res.Part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, msg)
		res.Part.Finish()
		return res
	}
	return d.slide(ctx, index)
}
