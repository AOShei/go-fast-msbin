package gofastmsbin

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"iter"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/cfb"
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/detect"
	"github.com/AOShei/go-fast-msbin/internal/doc"
	"github.com/AOShei/go-fast-msbin/internal/embed"
	"github.com/AOShei/go-fast-msbin/internal/ppt"
	"github.com/AOShei/go-fast-msbin/internal/stored"
	"github.com/AOShei/go-fast-msbin/internal/unit"
	"github.com/AOShei/go-fast-msbin/internal/warn"
	"github.com/AOShei/go-fast-msbin/internal/xls"
)

// File is a Word, Excel or PowerPoint file opened for reading a part at a
// time: a Word document's one body, a workbook's sheets, a presentation's
// slides. Opening a file identifies it and reads its properties and what
// its parts share; the parts are read when they are asked for, so a
// caller that handles one part and lets it go holds little at a time
// however large the file is.
//
// The parts are the same as those in the document that Extract returns,
// each complete: its warnings are on it, and what the file declares about
// the display of its text is set (Part.Finish has been called).
//
// A File is safe for concurrent use. A limit of Options.Limits counts
// over one pass: one call of Document, Part or Children, or one run
// through Parts. Passes that run at the same time share it.
type File struct {
	e           Extractor
	opts        gofastcore.Options
	r           io.ReaderAt
	size        int64
	closer      io.Closer
	cf          *cfb.File
	info        detect.Info
	source      gofastcore.Source
	meta        gofastcore.Metadata
	opened      []gofastcore.Warning // warnings about the document known when it was opened
	projects    [][]string           // the storages that hold a VBA project
	objects     []embed.Object       // the embedded objects
	deck        []byte               // a presentation's stream of records
	pictures    []byte               // and its stream of pictures
	currentEdit uint32               // where the presentation's last edit is
	book        []byte               // a workbook's stream of records
	word        []byte               // a Word document's main stream
	wordTable   []byte               // and its table stream
	parts       int
	stored      int // the index of the stored part, or -1
	noun        string

	mu      sync.Mutex
	sess    *session            // the reading in use
	clean   bool                // no limit has cut it short, so it can be used again
	tallies map[int]*unit.Tally // what the parts read so far found out, by index
	handed  *unit.Tally         // what handing on the embedded files found out
	took    int64               // the text the latest reading of parts took, which their embedded files cannot have
}

// session is one reading of the file by the reader of its format.
type session struct {
	src unit.Source
}

// Open opens the named file with default settings. It is
// (&Extractor{}).Open.
func Open(path string, opts *gofastcore.Options) (*File, error) {
	return (&Extractor{}).Open(path, opts)
}

// OpenBytes opens a file held in memory with default settings.
func OpenBytes(data []byte, opts *gofastcore.Options) (*File, error) {
	return (&Extractor{}).OpenBytes(data, opts)
}

// OpenReaderAt opens a file of the given size read through r, with
// default settings.
func OpenReaderAt(r io.ReaderAt, size int64, opts *gofastcore.Options) (*File, error) {
	return (&Extractor{}).OpenReaderAt(r, size, opts)
}

// Open opens the named file. The file's base name is recorded as the
// source name unless opts.Name is set. Close the File when done with it.
func (e *Extractor) Open(path string, opts *gofastcore.Options) (*File, error) {
	return e.openFile(context.Background(), path, opts)
}

func (e *Extractor) openFile(ctx context.Context, path string, opts *gofastcore.Options) (*File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	var o gofastcore.Options
	if opts != nil {
		o = *opts
	}
	if o.Name == "" {
		o.Name = info.Name()
	}
	f, err := e.open(ctx, file, info.Size(), &o)
	if err != nil {
		file.Close()
		return nil, err
	}
	f.closer = file
	return f, nil
}

// OpenBytes opens a file held in memory. The bytes must not change while
// the File is in use.
func (e *Extractor) OpenBytes(data []byte, opts *gofastcore.Options) (*File, error) {
	return e.OpenReaderAt(bytes.NewReader(data), int64(len(data)), opts)
}

// OpenReaderAt opens a file of the given size read through r, which must
// stay readable until the File is closed. The errors are those of
// Extract.
func (e *Extractor) OpenReaderAt(r io.ReaderAt, size int64, opts *gofastcore.Options) (*File, error) {
	return e.open(context.Background(), r, size, opts)
}

func (e *Extractor) open(ctx context.Context, r io.ReaderAt, size int64, opts *gofastcore.Options) (*File, error) {
	f := &File{r: r, size: size, tallies: map[int]*unit.Tally{}}
	if e != nil {
		f.e = *e
	}
	if opts != nil {
		f.opts = *opts
	}
	if r == nil {
		return nil, fmt.Errorf("gofastmsbin: no input to read: %w", gofastcore.ErrMalformed)
	}
	if limit := gofastcore.ResolveLimit(f.opts.Limits.MaxInputBytes, DefaultMaxInputBytes); size > limit {
		return nil, fmt.Errorf("gofastmsbin: input of %d bytes is over the limit of %d: %w", size, limit, gofastcore.ErrLimitExceeded)
	}
	cf, info, err := identify(r, size)
	if err != nil {
		return nil, err
	}
	if cf == nil || !info.Read || info.Encryption != "" {
		return nil, refusal(info)
	}
	f.cf, f.info = cf, info
	f.source = gofastcore.Source{Format: info.Format, MIME: detect.MIME(info.Format), Name: f.opts.Name, Size: size}
	warn := func(severity gofastcore.Severity, code, message string) {
		f.opened = append(f.opened, gofastcore.Warning{Code: code, Severity: severity, Message: message})
	}
	if cf.Damaged != "" {
		warn(gofastcore.SeverityWarning, gofastcore.WarnInputRepaired,
			"the compound file is damaged, and was read as far as it could be: "+clean.Label(cf.Damaged))
	}
	var ws []gofastcore.Warning
	f.meta, ws = metadata(cf, f.stream)
	f.opened = append(f.opened, ws...)
	f.projects = stored.Projects(cf)
	if len(f.projects) > 0 {
		warn(gofastcore.SeverityInfo, gofastcore.WarnInputActiveContent, "a VBA project (macros); its code is read as text into the stored part, and none of it is run")
	}
	var past int64
	f.objects, past = embed.Find(cf)
	if past > 0 {
		warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, warnCap(past))
	}
	if err := f.load(); err != nil {
		return nil, err
	}
	sess, err := f.reader()
	if err != nil {
		return nil, err
	}
	f.sess, f.clean = sess, !sess.src.Limited()
	if deck, ok := sess.src.(*ppt.Deck); ok && deck.Macros() && len(f.projects) == 0 {
		warn(gofastcore.SeverityInfo, gofastcore.WarnInputActiveContent, "a VBA project (macros); its code is read as text into the stored part, and none of it is run")
	}
	f.parts, f.noun = sess.src.Len(), sess.src.Noun()
	f.stored = sess.src.Stored()
	return f, nil
}

// warnCap is the message for the embedded objects past the bound on
// their number.
func warnCap(past int64) string { return warn.CapObjects.Message(past) }

// stream opens a stream at the root of the file as the readers see it.
func (f *File) stream(name string) (unit.Stream, error) {
	return f.cf.Open(name)
}

// reader opens the reader of the file's format on it. A later reading
// goes through the same steps as the first, so that it meets every limit
// where the first did.
func (f *File) reader() (*session, error) {
	if f.word != nil && f.info.Version != detect.Word97 {
		cfg := doc.Config{HasStored: len(f.projects) > 0,
			Stored: func(ctx context.Context, part *stored.Part) { stored.Macros(ctx, f.cf, f.projects, part) }}
		d, err := doc.Open6(f.word, &f.opts, cfg)
		if err != nil {
			return nil, fmt.Errorf("gofastmsbin: the Word document cannot be read: %s: %w", clean.Label(err.Error()), gofastcore.ErrMalformed)
		}
		return &session{src: d}, nil
	}
	if f.word != nil {
		data := func() []byte {
			s, err := f.stream("Data")
			if err != nil {
				return nil
			}
			b, _, _ := unit.ReadAll(s, s.Size())
			return b
		}
		cfg := doc.Config{HasStored: len(f.projects) > 0, Data: data,
			Stored: func(ctx context.Context, part *stored.Part) { stored.Macros(ctx, f.cf, f.projects, part) }}
		// The table stream the document names, read once.
		table := func(name string) []byte {
			if f.wordTable == nil {
				if s, err := f.stream(name); err == nil {
					f.wordTable, _, _ = unit.ReadAll(s, s.Size())
				}
				if f.wordTable == nil {
					f.wordTable = []byte{}
				}
			}
			return f.wordTable
		}
		d, err := doc.OpenStreams(f.word, table, &f.opts, cfg)
		if err != nil {
			return nil, fmt.Errorf("gofastmsbin: the Word document cannot be read: %s: %w", clean.Label(err.Error()), gofastcore.ErrMalformed)
		}
		return &session{src: d}, nil
	}
	if f.deck != nil {
		cfg := ppt.Config{Pictures: f.pictures, CurrentEdit: f.currentEdit, HasStored: len(f.projects) > 0, Chart: f.graph,
			Stored: func(ctx context.Context, part *stored.Part) { stored.Macros(ctx, f.cf, f.projects, part) }}
		return &session{src: ppt.Open(f.deck, &f.opts, cfg)}, nil
	}
	if f.book != nil {
		cfg := xls.Config{MaxCells: gofastcore.ResolveLimit(f.e.MaxCells, DefaultMaxCells), HasStored: len(f.projects) > 0,
			Stored: func(ctx context.Context, part *stored.Part) { stored.Macros(ctx, f.cf, f.projects, part) }}
		return &session{src: xls.Open(f.book, &f.opts, cfg)}, nil
	}
	return &session{src: &pending{f: f, version: f.info.Version}}, nil
}

// graph reads the compound file of a Microsoft Graph chart, which keeps
// the chart and its datasheet in a workbook stream, into the chart's
// blocks.
func (f *File) graph(ctx context.Context, storage []byte) (blocks []gofastcore.Block, cut, ok bool) {
	cf, err := cfb.Open(bytes.NewReader(storage), int64(len(storage)))
	if err != nil {
		return nil, false, false
	}
	s, err := cf.Open("Workbook")
	if err != nil {
		if s, err = cf.Open("Book"); err != nil {
			return nil, false, false
		}
	}
	data, _, err := unit.ReadAll(s, s.Size())
	if err != nil && len(data) == 0 {
		return nil, false, false
	}
	book := xls.Open(data, &f.opts, xls.Config{MaxCells: gofastcore.ResolveLimit(f.e.MaxCells, DefaultMaxCells)})
	if book.Stored() == 0 {
		return nil, false, false // no sheet, only what is stored
	}
	res := book.Read(ctx, 0)
	for _, b := range res.Part.Blocks {
		switch {
		case b.Kind == gofastcore.KindCaption:
			blocks = append(blocks, b)
		case b.Attrs[gofastcore.AttrRole] == gofastcore.RoleChart:
			blocks, ok = append(blocks, b), true
		}
	}
	return blocks, res.Tally.Limited, ok
}

// load reads into memory the stream the reader of the file's format
// reads whole: a workbook's records. The stream is no larger than the
// file, which Limits.MaxInputBytes bounds.
func (f *File) load() error {
	if f.info.Format == detect.Doc {
		s, err := f.stream("WordDocument")
		if err != nil {
			return fmt.Errorf("gofastmsbin: the document's stream cannot be read: %s: %w", clean.Label(err.Error()), gofastcore.ErrMalformed)
		}
		data, _, err := unit.ReadAll(s, s.Size())
		if err != nil && len(data) == 0 {
			return fmt.Errorf("gofastmsbin: the document's stream cannot be read: %s: %w", clean.Label(err.Error()), gofastcore.ErrMalformed)
		}
		if err != nil {
			f.opened = append(f.opened, gofastcore.Warning{Code: gofastcore.WarnInputRepaired, Severity: gofastcore.SeverityWarning,
				Message: fmt.Sprintf("the document's stream breaks off after %d of %d bytes; what was read of it is used", len(data), s.Size())})
		}
		f.word = data
		return nil
	}
	if f.info.Format == detect.Ppt {
		// A presentation saved for PowerPoint 95 and 97 both keeps the
		// one this library reads in a storage of its own.
		var in []string
		if f.cf.Has(detect.DualStorage, "PowerPoint Document") {
			in = []string{detect.DualStorage}
		}
		open := func(name string) (unit.Stream, error) { return f.cf.Open(append(in[:len(in):len(in)], name)...) }
		s, err := open("PowerPoint Document")
		if err != nil {
			return fmt.Errorf("gofastmsbin: the presentation's stream cannot be read: %s: %w", clean.Label(err.Error()), gofastcore.ErrMalformed)
		}
		data, _, err := unit.ReadAll(s, s.Size())
		if err != nil && len(data) == 0 {
			return fmt.Errorf("gofastmsbin: the presentation's stream cannot be read: %s: %w", clean.Label(err.Error()), gofastcore.ErrMalformed)
		}
		if err != nil {
			f.opened = append(f.opened, gofastcore.Warning{Code: gofastcore.WarnInputRepaired, Severity: gofastcore.SeverityWarning,
				Message: fmt.Sprintf("the presentation's stream breaks off after %d of %d bytes; what was read of it is used", len(data), s.Size())})
		}
		if data == nil {
			data = []byte{} // an empty stream is still a presentation's
		}
		f.deck = data
		// Where the last edit is: the Current User stream says.
		if cu, err := open("Current User"); err == nil {
			var head [20]byte
			if n, _ := cu.ReadAt(head[:], 0); n == len(head) {
				f.currentEdit = binary.LittleEndian.Uint32(head[16:])
			}
		}
		// The pictures of the shapes, where they are asked for.
		if f.opts.Images != gofastcore.ImagesNone {
			if s, err := open("Pictures"); err == nil {
				f.pictures, _, _ = unit.ReadAll(s, s.Size())
			}
		}
		return nil
	}
	if f.info.Format != detect.Xls {
		return nil
	}
	name := "Workbook"
	if !f.cf.Has(name) {
		name = "Book"
	}
	s, err := f.stream(name)
	if err != nil {
		return fmt.Errorf("gofastmsbin: the workbook's stream cannot be read: %s: %w", clean.Label(err.Error()), gofastcore.ErrMalformed)
	}
	data, _, err := unit.ReadAll(s, s.Size())
	if err != nil && len(data) == 0 {
		return fmt.Errorf("gofastmsbin: the workbook's stream cannot be read: %s: %w", clean.Label(err.Error()), gofastcore.ErrMalformed)
	}
	if err != nil {
		f.opened = append(f.opened, gofastcore.Warning{Code: gofastcore.WarnInputRepaired, Severity: gofastcore.SeverityWarning,
			Message: fmt.Sprintf("the workbook's stream breaks off after %d of %d bytes; what was read of it is used", len(data), s.Size())})
	}
	f.book = data
	return nil
}

// Close closes the file if Open opened it. The File must not be used
// afterwards.
func (f *File) Close() error {
	if f.closer == nil {
		return nil
	}
	return f.closer.Close()
}

// Format returns the kind of file: "doc", "xls" or "ppt". A template is
// the format it is a template of.
func (f *File) Format() string { return f.source.Format }

// Source returns what is known of where the document came from, as in
// gofastcore.Document.Source.
func (f *File) Source() gofastcore.Source { return f.source }

// Metadata returns the document's properties.
func (f *File) Metadata() gofastcore.Metadata { return f.meta }

// NumParts returns the number of parts: 1 for a Word document, the
// number of sheets of a workbook, the number of slides of a
// presentation. A workbook or presentation with none has one part, which
// says so. A document that stores what it shows nowhere (defined names,
// macro code) has one more, last, of kind gofastcore.PartStored; it comes
// with the whole document, not with a range of parts.
func (f *File) NumParts() int { return f.parts }

// tree gives ctx the budgets a document and the files embedded in it
// share: the text it may hold, and the bytes it may decode.
func (f *File) tree(ctx context.Context) (_ context.Context, done func()) {
	ctx, room := gofastcore.WithTextBudget(ctx, f.opts.Limits)
	ctx, decoded := gofastcore.WithDecodeBudget(ctx, f.opts.Limits, DefaultMaxDecodedBytes)
	return ctx, func() {
		decoded.Close()
		room.Close()
	}
}

// record keeps what reading a part found out about the document. Most
// parts find out nothing, and of those nothing is kept: what a File holds
// on to does not grow with the parts read.
func (f *File) record(index int, t *unit.Tally) {
	empty := len(t.Unread) == 0 && len(t.Damaged) == 0 && t.External == 0 && t.Programs == 0 && !t.Macros && !t.Limited &&
		len(t.Warnings) == 0
	f.mu.Lock()
	if empty {
		delete(f.tallies, index)
	} else {
		f.tallies[index] = t
	}
	f.mu.Unlock()
}

// known returns copies of what the parts read so far found out, in the
// order of the parts, and after them what handing on the embedded files
// found out, if they have been.
func (f *File) known() (*session, []*unit.Tally) {
	f.mu.Lock()
	defer f.mu.Unlock()
	indexes := make([]int, 0, len(f.tallies))
	for index := range f.tallies {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	out := make([]*unit.Tally, 0, len(indexes)+1)
	for _, index := range indexes {
		tally := *f.tallies[index]
		out = append(out, &tally)
	}
	if f.handed != nil {
		tally := *f.handed
		out = append(out, &tally)
	}
	return f.sess, out
}

// hand hands the embedded objects to Options.Child and returns the
// results, with what it found out about the document: the links to files
// outside it, the objects that could not be read, the bounds met. With a
// limit met, or no text left to take, the objects are listed and not
// read.
func (f *File) hand(ctx context.Context, limited bool) ([]*gofastcore.Document, *unit.Tally) {
	read := !limited && !gofastcore.TextBudgetFrom(ctx).Spent()
	res := embed.Hand(ctx, f.cf, &f.opts, f.objects, read)
	// A presentation keeps its embedded objects in records of its
	// stream, each a compound file.
	f.mu.Lock()
	sess := f.sess
	f.mu.Unlock()
	if deck, ok := sess.src.(*ppt.Deck); ok {
		ids, files, limited := deck.Objects(ctx)
		res.Limited = res.Limited || limited
		for i, data := range files {
			name, mime := "object"+strconv.FormatUint(uint64(ids[i]), 10), "application/x-ole-storage"
			if cf, err := cfb.Open(bytes.NewReader(data), int64(len(data))); err == nil {
				if in := detect.Compound(cf); in.Format != "" {
					name, mime = name+"."+in.Format, detect.MIME(in.Format)
				}
			}
			if data == nil || !read || f.opts.Child == nil {
				res.Children = append(res.Children, f.opts.ExtractChild(ctx, name, mime, nil, int64(len(data))))
			} else {
				res.Children = append(res.Children, f.opts.ExtractChild(ctx, name, mime, bytes.NewReader(data), int64(len(data))))
			}
		}
	}
	tally := &unit.Tally{External: res.Links, Damaged: res.Damaged, Limited: res.Limited}
	var lost warn.Lost
	lost.Add(warn.CapObjectDepth, res.Deep)
	tally.Warnings = lost.Warnings()
	if res.Limited {
		tally.Warnings = append(tally.Warnings, gofastcore.Warning{Code: gofastcore.WarnLimitExceeded, Severity: gofastcore.SeverityError,
			Message: "the decompressed size limit was reached; the embedded files from there on are listed and not read"})
	}
	f.mu.Lock()
	f.handed = tally
	f.mu.Unlock()
	return res.Children, tally
}

// Children extracts the files embedded in the document (a workbook in a
// Word document, a document in a document) through Options.Child, and
// returns them as in gofastcore.Document.Children. With no Options.Child
// each is listed, not extracted. They have what the parts read last left
// of the text the document may hold (Limits.MaxTextBytes), as in
// Document.
func (f *File) Children(ctx context.Context) ([]*gofastcore.Document, error) {
	ctx, done := f.tree(ctx)
	defer done()
	f.mu.Lock()
	took := f.took
	limited := false
	for _, t := range f.tallies {
		limited = limited || t.Limited
	}
	f.mu.Unlock()
	text := gofastcore.TextBudgetFrom(ctx)
	for ; took > 0; took -= 1 << 30 {
		text.Take(int(min(took, 1<<30)))
	}
	out, _ := f.hand(ctx, limited)
	return out, ctx.Err()
}

// acquire returns a reading to make a pass with: the one in use if no
// limit has cut it short, otherwise a new one, since what a reading
// keeps after meeting a limit is what the limit left of it.
func (f *File) acquire() (*session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.clean {
		sess, err := f.reader()
		if err != nil {
			return nil, err
		}
		f.sess, f.clean = sess, true
	}
	return f.sess, nil
}

// spoil records that a limit has cut a reading short.
func (f *File) spoil(sess *session) {
	f.mu.Lock()
	if f.sess == sess {
		f.clean = false
	}
	f.mu.Unlock()
}

// selection returns the indexes of the parts Options.Parts asks for, cut
// to Options.Limits.MaxParts.
func (f *File) selection() (selected []int, asked int, err error) {
	whole := f.opts.Parts == (gofastcore.PartRange{})
	for i := 0; i < f.parts; i++ {
		// The stored part comes with the whole document only.
		if i == f.stored && !whole {
			continue
		}
		if f.opts.Parts.Contains(i + 1) {
			selected = append(selected, i)
		}
	}
	if len(selected) == 0 {
		return nil, 0, fmt.Errorf("gofastmsbin: the %s has %d %s, numbered from 1: %w", f.kind(), f.parts, f.noun, gofastcore.ErrNoPartsSelected)
	}
	asked = len(selected)
	if limit := f.opts.Limits.MaxParts; limit > 0 && asked > limit {
		selected = selected[:limit]
	}
	return selected, asked, nil
}

// kind names the kind of document.
func (f *File) kind() string {
	switch f.source.Format {
	case detect.Xls:
		return "workbook"
	case detect.Ppt:
		return "presentation"
	}
	return "Word document"
}

// pass reads the parts with the given indexes and hands each to yield,
// in order, until yield returns false. window is how many parts may be
// read ahead of the one being handed on; zero means all of them.
//
// With more than one worker, which parts are read before a limit is
// reached depends on which worker gets there first. What a limit leaves
// out must not, so when one is met the parts are read again in order on
// a reading of their own, and those already handed on, which are the
// same in both, are passed over.
func (f *File) pass(ctx context.Context, selected []int, window int, yield func(index int, res *unit.Result) bool) (*session, error) {
	sess, err := f.acquire()
	if err != nil {
		return nil, err
	}
	sess.src.Begin()
	text := gofastcore.TextBudgetFrom(ctx)
	text.Reset()
	// What the parts take of the text the document may hold is not there
	// for the files they embed, which Children reads later on a budget of
	// its own.
	start := text.Left()
	defer func() {
		f.mu.Lock()
		f.took = start - text.Left()
		f.mu.Unlock()
	}()
	workers := f.opts.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	workers = min(workers, len(selected))
	if workers <= 1 {
		limited := false
		for _, index := range selected {
			res := sess.src.Read(ctx, index)
			if err := ctx.Err(); err != nil {
				return sess, err
			}
			limited = limited || res.Limited || sess.src.Limited()
			if limited {
				f.spoil(sess)
			}
			if !yield(index, res) {
				break
			}
		}
		return sess, nil
	}
	if window <= 0 {
		window = len(selected)
	}
	handed := 0
	for handed < len(selected) {
		batch := selected[handed:min(handed+window, len(selected))]
		results := make([]*unit.Result, len(batch))
		var wg sync.WaitGroup
		var next atomic.Int64
		for w := 0; w < min(workers, len(batch)); w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					k := int(next.Add(1)) - 1
					if k >= len(batch) || ctx.Err() != nil {
						return
					}
					results[k] = sess.src.Read(ctx, batch[k])
				}
			}()
		}
		wg.Wait()
		if err := ctx.Err(); err != nil {
			return sess, err
		}
		limited := sess.src.Limited()
		for _, res := range results {
			limited = limited || res.Limited
		}
		if limited {
			f.spoil(sess)
			return f.inOrder(ctx, selected, handed, yield)
		}
		for k, res := range results {
			handed++
			if !yield(batch[k], res) {
				return sess, nil
			}
		}
	}
	return sess, nil
}

// inOrder reads the parts one after another on a reading of its own,
// and hands on those after the first handed, which were handed on
// before.
func (f *File) inOrder(ctx context.Context, selected []int, handed int, yield func(index int, res *unit.Result) bool) (*session, error) {
	sess, err := f.reader()
	if err != nil {
		return nil, err
	}
	// It has met the limit too by the end, so it is kept only for what
	// it knows of the document, not to read with again.
	f.mu.Lock()
	f.sess, f.clean = sess, false
	f.mu.Unlock()
	// The reading starts over: what the first attempt took of the text
	// the document may hold is given back.
	sess.src.Begin()
	gofastcore.TextBudgetFrom(ctx).Reset()
	for k, index := range selected {
		res := sess.src.Read(ctx, index)
		if err := ctx.Err(); err != nil {
			return sess, err
		}
		if k >= handed && !yield(index, res) {
			break
		}
	}
	return sess, nil
}

// once leaves the bytes of a picture with the first block that shows it
// among the parts it is given, in order, and takes them from the others.
type once map[string]bool

func (seen once) keep(part *gofastcore.Part) {
	for i := range part.Blocks {
		img := part.Blocks[i].Image
		if img == nil || img.Data == nil {
			continue
		}
		if seen[img.ID] {
			img.Data = nil
		}
		seen[img.ID] = true
	}
}

// Document reads the parts Options.Parts selects and returns the
// document, the same one Extract returns.
func (f *File) Document(ctx context.Context) (*gofastcore.Document, error) {
	ctx, done := f.tree(ctx)
	defer done()
	selected, asked, err := f.selection()
	if err != nil {
		return nil, err
	}
	doc := gofastcore.New(f.source.Format)
	doc.Source, doc.Metadata = f.source, f.meta
	warnAll(doc, f.opened)
	if asked > len(selected) {
		warnAll(doc, []gofastcore.Warning{f.capped(asked, len(selected))})
	}
	pictures := once{}
	var tallies []*unit.Tally
	sess, err := f.pass(ctx, selected, 0, func(index int, res *unit.Result) bool {
		pictures.keep(&res.Part)
		doc.Parts = append(doc.Parts, res.Part)
		tally, kept := res.Tally, res.Tally
		tallies = append(tallies, &tally)
		f.record(index, &kept)
		return true
	})
	if err != nil {
		return nil, err
	}
	limited := false
	for _, t := range tallies {
		limited = limited || t.Limited
	}
	children, handed := f.hand(ctx, limited)
	doc.Children = children
	warnAll(doc, sess.src.Summary(append(tallies, handed)))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return doc, nil
}

// capped is the warning that fewer parts are given than were asked for.
func (f *File) capped(asked, given int) gofastcore.Warning {
	return gofastcore.Warning{Code: gofastcore.WarnLimitExceeded, Severity: gofastcore.SeverityError,
		Message: fmt.Sprintf("%d %s were asked for; the first %d are given", asked, f.noun, given)}
}

// Parts returns an iterator over the parts Options.Parts selects, in
// order. Each part is read when the loop reaches it, a few ahead with
// more than one worker, and is the caller's to keep or let go. The bytes
// of a picture that several parts show (Options.Images) come with the
// first of them.
//
// The iterator yields an error, with an empty part, and stops: when no
// part is selected (gofastcore.ErrNoPartsSelected), when ctx is
// cancelled, or when the file can no longer be read. A part that is
// damaged or cut short by a limit is not an error: it is yielded with a
// warning that says so.
//
// What the parts say about the document as a whole is in Warnings and
// Children once they have been read.
func (f *File) Parts(ctx context.Context) iter.Seq2[gofastcore.Part, error] {
	return func(yield func(gofastcore.Part, error) bool) {
		ctx, done := f.tree(ctx)
		defer done()
		selected, _, err := f.selection()
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			yield(gofastcore.Part{}, err)
			return
		}
		workers := f.opts.Workers
		if workers <= 0 {
			workers = runtime.GOMAXPROCS(0)
		}
		pictures := once{}
		stopped := false
		_, err = f.pass(ctx, selected, 4*workers, func(index int, res *unit.Result) bool {
			pictures.keep(&res.Part)
			tally := res.Tally
			f.record(index, &tally)
			stopped = !yield(res.Part, nil)
			return !stopped
		})
		if err != nil && !stopped {
			yield(gofastcore.Part{}, err)
		}
	}
}

// Part reads the part with the given number, from 1, whether or not
// Options.Parts selects it.
func (f *File) Part(ctx context.Context, number int) (gofastcore.Part, error) {
	ctx, done := f.tree(ctx)
	defer done()
	if number < 1 || number > f.parts {
		return gofastcore.Part{}, fmt.Errorf("gofastmsbin: part %d: the %s has %d %s, numbered from 1: %w", number, f.kind(), f.parts, f.noun, gofastcore.ErrNoPartsSelected)
	}
	if err := ctx.Err(); err != nil {
		return gofastcore.Part{}, err
	}
	var part gofastcore.Part
	_, err := f.pass(ctx, []int{number - 1}, 1, func(index int, res *unit.Result) bool {
		part = res.Part
		tally := res.Tally
		f.record(index, &tally)
		return true
	})
	if err != nil {
		return gofastcore.Part{}, err
	}
	return part, nil
}

// Warnings returns the warnings about the document as a whole, as in
// gofastcore.Document.Warnings: those known when the file was opened,
// and those that the parts read so far give rise to (content of a kind
// that is not read, damaged supporting parts, references to things
// outside the file). It is complete once every part has been read.
func (f *File) Warnings() []gofastcore.Warning {
	var doc gofastcore.Document
	warnAll(&doc, f.opened)
	if selected, asked, err := f.selection(); err == nil && asked > len(selected) {
		warnAll(&doc, []gofastcore.Warning{f.capped(asked, len(selected))})
	}
	sess, tallies := f.known()
	warnAll(&doc, sess.src.Summary(tallies))
	return doc.Warnings
}

// warnAll adds ws to doc through Document.Warn, which bounds how many a
// document keeps.
func warnAll(doc *gofastcore.Document, ws []gofastcore.Warning) {
	for _, w := range ws {
		doc.Warn(w.Severity, w.Code, w.Message)
	}
}
