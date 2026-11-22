package gofastmsbin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/cfb"
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/detect"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// Defaults for the limits of gofastcore.Options.Limits, used where a limit
// is zero. A negative limit means no limit.
const (
	// DefaultMaxInputBytes bounds the size of the file.
	DefaultMaxInputBytes = 1 << 30
	// DefaultMaxDecodedBytes bounds the bytes the file is expanded to
	// beyond its own size, over the document and the files embedded in
	// it: macro code and pictures the file keeps compressed, and embedded
	// files written out to be read.
	DefaultMaxDecodedBytes = 2 << 30
	// DefaultMaxCells bounds the cells kept from a workbook, over all its
	// sheets. Each cell kept costs memory in the result whatever the size
	// of the file, so this is what bounds the memory a workbook can ask
	// for.
	DefaultMaxCells = 20_000_000
)

// Warning codes specific to this library. Each states a fact about the
// file. The codes shared by all go-fast libraries are in gofastcore, and
// this library raises these of them besides the ones about parts, limits
// and embedded documents:
//
//	input.active_content      the file contains a VBA project
//	input.external_reference  an embedded object is a link to a file
//	                          outside the document
//	input.repaired            the compound file is damaged, and was read
//	                          as far as it could be
//	text.unmapped_glyphs      text in a code page that does not define
//	                          some of its characters
const (
	// WarnPropertiesDamaged: a stream of document properties could not be
	// read to its end; the properties read before the damage are kept.
	WarnPropertiesDamaged = warn.PropertiesDamaged
	// WarnControlCharsRemoved: control characters with no meaning as text
	// were removed from extracted strings.
	WarnControlCharsRemoved = warn.ControlCharsRemoved
	// WarnUnreadContent: the document holds content of a kind this version
	// does not read.
	WarnUnreadContent = warn.UnreadContent
	// WarnPartDamaged: a stream of the file other than its main one (a
	// VBA project, an embedded object) could not be read to its end; what
	// was read is used.
	WarnPartDamaged = warn.PartDamaged
)

// Extractor extracts Word, Excel and PowerPoint files in the binary
// formats of Office 97 to 2003. The zero value is ready to use and safe
// for concurrent use. Its fields are the settings only this library has;
// everything else is in gofastcore.Options.
type Extractor struct {
	// MaxCells bounds the cells kept from a workbook, over all its
	// sheets. A sheet that reaches it is cut off there, with a
	// limit.exceeded warning. Zero means DefaultMaxCells; negative means
	// no limit.
	MaxCells int64
}

var _ gofastcore.Extractor = (*Extractor)(nil)

// Formats lists the formats this library extracts, as they appear in
// gofastcore.Source.Format.
func (e *Extractor) Formats() []string { return []string{detect.Doc, detect.Xls, detect.Ppt} }

// Extract reads a file with default settings. It is
// (&Extractor{}).Extract.
func Extract(ctx context.Context, r io.ReaderAt, size int64, opts *gofastcore.Options) (*gofastcore.Document, error) {
	return (&Extractor{}).Extract(ctx, r, size, opts)
}

// ExtractFile opens the named file and extracts it with default settings.
func ExtractFile(ctx context.Context, path string, opts *gofastcore.Options) (*gofastcore.Document, error) {
	return (&Extractor{}).ExtractFile(ctx, path, opts)
}

// ExtractFile opens the named file and extracts it. The file's base name
// is recorded as the source name unless opts.Name is set.
func (e *Extractor) ExtractFile(ctx context.Context, path string, opts *gofastcore.Options) (*gofastcore.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := e.openFile(ctx, path, opts)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Document(ctx)
}

// Extract reads size bytes from r and returns the document.
//
// The format is decided from the file itself, never from a name. The
// library is in development: workbooks and Word documents are read; of a
// presentation the properties are, and the slides not yet (the part says
// so, with part.failed).
//
// An error wraps one of the gofastcore errors:
//
//   - ErrLimitExceeded: the input is larger than Limits.MaxInputBytes, or
//     its compound file has more entries than are read.
//   - ErrMalformed: a compound file too damaged to say what it holds.
//   - ErrUnsupported: a file that is none of the three formats; one of a
//     generation that is not read (Excel 2 to 4, Word 2, PowerPoint 95
//     and older); an encrypted file, which is not read yet.
//   - ErrNoPartsSelected: Options.Parts selects none of the document's
//     parts.
//
// It is OpenReaderAt followed by Document.
func (e *Extractor) Extract(ctx context.Context, r io.ReaderAt, size int64, opts *gofastcore.Options) (*gofastcore.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := e.open(ctx, r, size, opts)
	if err != nil {
		return nil, err
	}
	return f.Document(ctx)
}

// identify opens the compound file and says what it is. For a file that
// is not a compound file it returns no file, and what the file is
// instead. The error is one of the shared errors, wrapped.
func identify(r io.ReaderAt, size int64) (*cfb.File, detect.Info, error) {
	start := make([]byte, min(size, 512))
	if n, err := r.ReadAt(start, 0); err != nil && err != io.EOF {
		return nil, detect.Info{}, fmt.Errorf("gofastmsbin: reading the input: %w", err)
	} else {
		start = start[:n]
	}
	f, err := cfb.Open(r, size)
	switch {
	case errors.Is(err, cfb.ErrNotCompound):
		if bytes.HasPrefix(start, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}) {
			return nil, detect.Info{}, fmt.Errorf("gofastmsbin: a compound file cut off inside its header (%d bytes): %w", size, gofastcore.ErrMalformed)
		}
		return nil, detect.Bare(start), nil
	case errors.Is(err, cfb.ErrLimit):
		return nil, detect.Info{}, fmt.Errorf("gofastmsbin: %s: %w", clean.Label(err.Error()), gofastcore.ErrLimitExceeded)
	case err != nil:
		return nil, detect.Info{}, fmt.Errorf("gofastmsbin: a compound file that cannot be read: %s: %w", clean.Label(err.Error()), gofastcore.ErrMalformed)
	}
	return f, detect.Compound(f), nil
}

// refusal is the error for a file that is not read, saying what it is.
func refusal(in detect.Info) error {
	what := "the file is not a compound file, and so none of the binary Office formats (.doc, .xls, .ppt)"
	switch {
	case in.Format != "" && in.Read && in.Encryption != "":
		what = in.Version + " file, encrypted (" + in.Encryption + "): encrypted files are not read yet"
	case in.Format != "" && in.Version != "":
		what = in.Version + " file: this generation of the format is not read"
	case in.Format != "":
		what = "a ." + in.Format + " file of a generation the file does not state: not read"
	case in.Other != "":
		what = "the file is " + in.Other + ", not one of the binary Office formats (.doc, .xls, .ppt)"
	}
	return fmt.Errorf("gofastmsbin: %s: %w", what, gofastcore.ErrUnsupported)
}
