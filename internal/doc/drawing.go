package doc

import (
	"sort"
	"strconv"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-msbin/internal/media"
	"github.com/AOShei/go-fast-msbin/internal/officeart"
)

// What a document holds besides the text of its stories: shapes drawn
// over the page, which may hold text (text boxes) or show a picture, and
// pictures in the line of the text ([MS-DOC] 2.9.172, 2.9.253, 2.8.37).

// shape is a shape of the drawing that is anchored in the text.
type shape struct {
	textbox int // the text box it holds, from 0; -1 for none
	blip    int // the picture it shows, from 1 in the store; 0 for none
	hidden  bool
	alt     string
}

// drawing is what the drawing layer of a document says.
type drawing struct {
	shapes  map[uint32]*shape
	store   []officeart.StoreEntry
	anchors []int64  // where shapes are anchored in the main text, in order
	spids   []uint32 // and which shape each is
	boxes   []int64  // where each text box's text begins, in the story of text boxes
}

// Bounds on the drawing.
const (
	maxShapes   = 1 << 16
	maxPictures = 1 << 16
)

// readDrawing reads the drawing layer: the shapes and the store of
// pictures (in the table stream), where the shapes are anchored, and
// where the text of the text boxes is.
func (d *Doc) readDrawing() *drawing {
	dr := &drawing{shapes: map[uint32]*shape{}}
	f := d.f
	// The drawings: the group, with the store of pictures, and then a
	// drawing for the main text and one for the headers, each after a
	// byte that says which it is.
	b := f.part(d.table, fcDggInfo)
	if group, rest, ok := officeart.Next(b); ok && group.Type == officeart.DggContainer {
		dr.store, _ = officeart.Store(group.Body, maxPictures)
		for len(rest) > 1 {
			dg, after, ok := officeart.Next(rest[1:])
			if !ok {
				break
			}
			rest = after
			var cur *shape
			officeart.Walk(dg.Body, func(r officeart.Rec, depth int) bool {
				switch r.Type {
				case officeart.Sp:
					cur = nil
					if len(r.Body) >= 4 && len(dr.shapes) < maxShapes {
						cur = &shape{textbox: -1}
						dr.shapes[le.Uint32(r.Body)] = cur
					}
				case officeart.OPT, officeart.SecondaryOPT, officeart.TertiaryOPT:
					if cur == nil {
						break
					}
					var p officeart.Props
					p.ReadProps(r.Inst, r.Body)
					// The text a shape holds is named by a number whose
					// high half counts the text boxes from 1.
					if v, ok := p.Value(officeart.PropTextID); ok && v>>16 > 0 {
						cur.textbox = int(v>>16) - 1
					}
					if v, ok := p.Value(officeart.PropBlip); ok {
						cur.blip = int(v)
					}
					if p.Hidden() {
						cur.hidden = true
					}
					if alt := p.String(officeart.PropDescription); alt != "" {
						cur.alt, _ = clean.String([]byte(alt), clean.Prose)
					}
				}
				return true
			})
		}
	}
	// Where the shapes are anchored: positions in the main text, and for
	// each the shape's number ([MS-DOC] 2.9.253, Spa: 26 bytes).
	cps, data := plc(f.part(d.table, fcPlcSpaMom), 26)
	for i := 0; i+1 < len(cps) && 26*i+4 <= len(data) && i < maxShapes; i++ {
		dr.anchors = append(dr.anchors, cps[i])
		dr.spids = append(dr.spids, le.Uint32(data[26*i:]))
	}
	// The text of the text boxes: positions in their story.
	dr.boxes, _ = plc(f.part(d.table, fcPlcftxbxTxt), 22)
	return dr
}

// anchored returns the blocks of what is anchored in the paragraph whose
// characters are cp0 up to end: the text of text boxes, and pictures.
func (r *reader) anchored(cp0, end int64) []gofastcore.Block {
	dr := r.d.drawing
	if dr == nil || len(dr.anchors) == 0 {
		return nil
	}
	var out []gofastcore.Block
	from := sort.Search(len(dr.anchors), func(i int) bool { return dr.anchors[i] >= cp0 })
	for i := from; i < len(dr.anchors) && dr.anchors[i] <= end; i++ {
		sh := dr.shapes[dr.spids[i]]
		if sh == nil {
			continue
		}
		var blocks []gofastcore.Block
		if sh.textbox >= 0 && sh.textbox+1 < len(dr.boxes) && !r.boxDone[sh.textbox] {
			if r.boxDone == nil {
				r.boxDone = map[int]bool{}
			}
			r.boxDone[sh.textbox] = true
			base := r.d.ednAt + r.d.f.ccpEdn
			lo, hi := base+dr.boxes[sh.textbox], base+dr.boxes[sh.textbox+1]
			if hi > lo && hi <= base+r.d.f.ccpTxbx {
				c := r.child()
				blocks = c.story(lo, hi)
				r.merge(c)
			}
		}
		if sh.blip > 0 {
			if b, ok := r.picture(sh.blip, sh.alt); ok {
				blocks = append(blocks, b)
			}
		}
		for k := range blocks {
			if sh.hidden {
				blocks[k].Display |= gofastcore.DisplayHidden
			}
		}
		out = append(out, blocks...)
	}
	return out
}

// picture makes the image block of a picture of the store.
func (r *reader) picture(number int, alt string) (gofastcore.Block, bool) {
	mode := gofastcore.ImagesMeta
	if r.d.opts != nil {
		mode = r.d.opts.Images
	}
	if mode == gofastcore.ImagesNone {
		r.note("sawImage")
		return gofastcore.Block{}, false
	}
	dr := r.d.drawing
	var blip officeart.Rec
	if number <= len(dr.store) {
		e := dr.store[number-1]
		blip = e.Blip
		// A picture the store does not hold is in the WordDocument
		// stream, at the place its entry gives.
		if blip.Type == 0 && e.Size > 0 && int64(e.Offset)+8 <= int64(len(r.d.stream)) {
			if rec, _, ok := officeart.Next(r.d.stream[e.Offset:]); ok {
				blip = rec
			}
		}
	}
	return r.image("image"+strconv.Itoa(number), blip, alt, mode)
}

// image makes an image block of a picture record.
func (r *reader) image(id string, blip officeart.Rec, alt string, mode gofastcore.ImageMode) (gofastcore.Block, bool) {
	if !r.room.Take(gofastcore.BlockCost + len(alt)) {
		r.cut = true
		return gofastcore.Block{}, false
	}
	img, err := media.Image(0, blip, mode, gofastcore.DecodeBudgetFrom(r.ctx))
	if err != nil {
		r.note("budget")
	}
	// The picture's name: its number in the store, or where it is in the
	// Data stream, and the extension of its type.
	ext := ""
	if i := len("image0"); len(img.ID) > i {
		ext = img.ID[i:]
	}
	img.ID = id + ext
	return gofastcore.Block{Kind: gofastcore.KindImage, Text: alt, Image: img}, true
}

// inline makes the image block of a picture in the line of the text,
// which is in the Data stream at the given place ([MS-DOC] 2.9.191,
// PICFAndOfficeArtData: a length, the length of a header, the header,
// and then a shape and the picture it shows).
func (r *reader) inline(at uint32) (gofastcore.Block, bool) {
	data := r.d.dataStream()
	if int64(at)+6 > int64(len(data)) {
		return gofastcore.Block{}, false
	}
	size, header := int64(le.Uint32(data[at:])), int64(le.Uint16(data[at+4:]))
	end := min(int64(at)+size, int64(len(data)))
	start := int64(at) + header
	if header < 6 || start >= end {
		return gofastcore.Block{}, false
	}
	mode := gofastcore.ImagesMeta
	if r.d.opts != nil {
		mode = r.d.opts.Images
	}
	var blip officeart.Rec
	alt := ""
	var props officeart.Props
	officeart.Walk(data[start:end], func(rec officeart.Rec, depth int) bool {
		switch {
		case rec.Type == officeart.OPT || rec.Type == officeart.SecondaryOPT || rec.Type == officeart.TertiaryOPT:
			props.ReadProps(rec.Inst, rec.Body)
		case rec.Type == officeart.BSE:
			// An entry of the store around the picture.
			if len(rec.Body) >= 36 {
				if name := int(rec.Body[33]); 36+name <= len(rec.Body) {
					if inner, _, ok := officeart.Next(rec.Body[36+name:]); ok {
						blip = inner
					}
				}
			}
			return false
		case rec.Type >= 0xF018 && rec.Type <= 0xF117 && !rec.Container():
			blip = rec
		}
		return true
	})
	if blip.Type == 0 {
		return gofastcore.Block{}, false // a picture kept elsewhere, or an object's stand-in with none
	}
	if mode == gofastcore.ImagesNone {
		r.note("sawImage")
		return gofastcore.Block{}, false
	}
	alt, _ = clean.String([]byte(props.String(officeart.PropDescription)), clean.Prose)
	b, ok := r.image("image-d"+strconv.FormatUint(uint64(at), 10), blip, alt, mode)
	if ok && props.Hidden() {
		b.Display = gofastcore.DisplayHidden
	}
	return b, ok
}

// note records something a reading found out, at its root.
func (r *reader) note(what string) {
	root := r
	if r.root != nil {
		root = r.root
	}
	switch what {
	case "sawImage":
		root.sawImage = true
	case "budget":
		root.budget = true
	}
}
