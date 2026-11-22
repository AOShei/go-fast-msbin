package gofastmsbin

import (
	"context"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/detect"
	"github.com/AOShei/go-fast-msbin/internal/stored"
	"github.com/AOShei/go-fast-msbin/internal/unit"
)

// pending stands for the reader of a format that is not written yet. The
// document has one part of the format's kind, which says its content was
// not read (part.failed), and the stored part if the file has macros.
type pending struct {
	f       *File
	version string
}

func (p *pending) Len() int {
	if len(p.f.projects) > 0 {
		return 2
	}
	return 1
}

func (p *pending) Stored() int {
	if len(p.f.projects) > 0 {
		return 1
	}
	return -1
}

func (p *pending) Noun() string {
	switch p.f.source.Format {
	case detect.Xls:
		return "sheets"
	case detect.Ppt:
		return "slides"
	}
	return "parts"
}

func (p *pending) Begin()        {}
func (p *pending) Limited() bool { return false }

func (p *pending) Read(ctx context.Context, index int) *unit.Result {
	if index == p.Stored() {
		part := stored.NewPart(ctx, index+1)
		stored.Macros(ctx, p.f.cf, p.f.projects, part)
		limited := part.Finish()
		return &unit.Result{Part: part.Part, Tally: unit.Tally{Damaged: part.Damaged, Limited: limited}}
	}
	kind := gofastcore.PartBody
	switch p.f.source.Format {
	case detect.Xls:
		kind = gofastcore.PartSheet
	case detect.Ppt:
		kind = gofastcore.PartSlide
	}
	part := gofastcore.Part{Kind: kind, Number: index + 1}
	part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed,
		"the content of a "+p.version+" file is not read by this version of the library; its properties, macros and embedded files are")
	part.Finish()
	return &unit.Result{Part: part}
}

func (p *pending) Summary(tallies []*unit.Tally) []gofastcore.Warning {
	return unit.Summarize(tallies, nil)
}
