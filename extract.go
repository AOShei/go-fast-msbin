package gofastmsbin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/cfb"
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/detect"
)

// Defaults for the limits of gofastcore.Options.Limits, used where a limit
// is zero. A negative limit means no limit.
const (
	// DefaultMaxInputBytes bounds the size of the file.
	DefaultMaxInputBytes = 1 << 30
)

// Extractor extracts Word, Excel and PowerPoint files in the binary
// formats of Office 97 to 2003. The zero value is ready to use and safe
// for concurrent use. Its fields are the settings only this library has;
// everything else is in gofastcore.Options.
type Extractor struct{}

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
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	o := gofastcore.Options{}
	if opts != nil {
		o = *opts
	}
	if o.Name == "" {
		o.Name = info.Name()
	}
	return e.Extract(ctx, f, info.Size(), &o)
}

// Extract reads size bytes from r and returns the document.
//
// The library is in development and reads no format yet: the error wraps
// gofastcore.ErrUnsupported and says what the file is. The other errors
// are already the final ones:
//
//   - ErrLimitExceeded: the input is larger than Limits.MaxInputBytes, or
//     its compound file has more entries than are read.
//   - ErrMalformed: a compound file too damaged to say what it holds.
//   - ErrUnsupported: a file that is none of the three formats, or one of
//     a generation that is not read (Excel 2 to 4, Word 2, PowerPoint 95
//     and older).
func (e *Extractor) Extract(ctx context.Context, r io.ReaderAt, size int64, opts *gofastcore.Options) (*gofastcore.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var limits gofastcore.Limits
	if opts != nil {
		limits = opts.Limits
	}
	if limit := gofastcore.ResolveLimit(limits.MaxInputBytes, DefaultMaxInputBytes); size > limit {
		return nil, fmt.Errorf("gofastmsbin: input of %d bytes is over the limit of %d: %w", size, limit, gofastcore.ErrLimitExceeded)
	}
	info, err := identify(r, size)
	if err != nil {
		return nil, err
	}
	return nil, refusal(info)
}

// identify says what the file is, or returns the error for a file that
// cannot be told: one of the shared errors, wrapped.
func identify(r io.ReaderAt, size int64) (detect.Info, error) {
	start := make([]byte, min(size, 512))
	if n, err := r.ReadAt(start, 0); err != nil && err != io.EOF {
		return detect.Info{}, fmt.Errorf("gofastmsbin: reading the input: %w", err)
	} else {
		start = start[:n]
	}
	f, err := cfb.Open(r, size)
	switch {
	case errors.Is(err, cfb.ErrNotCompound):
		if bytes.HasPrefix(start, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}) {
			return detect.Info{}, fmt.Errorf("gofastmsbin: a compound file cut off inside its header (%d bytes): %w", size, gofastcore.ErrMalformed)
		}
		return detect.Bare(start), nil
	case errors.Is(err, cfb.ErrLimit):
		return detect.Info{}, fmt.Errorf("gofastmsbin: %s: %w", clean.Label(err.Error()), gofastcore.ErrLimitExceeded)
	case err != nil:
		return detect.Info{}, fmt.Errorf("gofastmsbin: a compound file that cannot be read: %s: %w", clean.Label(err.Error()), gofastcore.ErrMalformed)
	}
	return detect.Compound(f), nil
}

// refusal is the error for a file that is not read, saying what it is.
func refusal(in detect.Info) error {
	what := "the file is not a compound file, and so none of the binary Office formats (.doc, .xls, .ppt)"
	switch {
	case in.Format != "" && in.Read:
		what = in.Version + " file"
		if in.Encryption != "" {
			what += ", encrypted (" + in.Encryption + ")"
		}
		what += ": this format is not read yet"
	case in.Format != "" && in.Version != "":
		what = in.Version + " file: this generation of the format is not read"
	case in.Format != "":
		what = "a ." + in.Format + " file of a generation the file does not state: not read"
	case in.Other != "":
		what = "the file is " + in.Other + ", not one of the binary Office formats (.doc, .xls, .ppt)"
	}
	return fmt.Errorf("gofastmsbin: %s: %w", what, gofastcore.ErrUnsupported)
}
