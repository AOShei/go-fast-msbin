// Package stored reads what a file of any of the three formats keeps for
// the file as a whole and shows on no page: the code of its VBA projects.
// Each format gives it in its stored part (gofastcore.PartStored), after
// what is its own. The code is read as text and never run.
//
// The stored part says where its text comes from (Attrs["stored"]). No
// display reason is set for being stored: the file declares nothing about
// the display of such data.
package stored

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/cfb"
	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-core/vba"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// Projects names the storages of the file that hold a VBA project: the
// storage itself, which has a VBA storage with a dir stream in it. A Word
// file keeps its project in Macros, a workbook in _VBA_PROJECT_CUR; a
// storage of any other name at the root of the file that holds one is
// found as well.
func Projects(f *cfb.File) [][]string {
	var out [][]string
	if f.Has("VBA", "dir") {
		out = append(out, nil)
	}
	for _, name := range f.List() {
		if f.IsStorage(name) && f.Has(name, "VBA", "dir") {
			out = append(out, []string{name})
		}
	}
	return out
}

// Macros reads the modules of the VBA projects into the part. What they
// decompress to is taken from the decode budget in ctx.
func Macros(ctx context.Context, f *cfb.File, projects [][]string, p *Part) {
	if len(projects) > warn.MaxProjects {
		p.Lost.Add(warn.CapProjects, int64(len(projects)-warn.MaxProjects))
		projects = projects[:warn.MaxProjects]
	}
	decoded := gofastcore.DecodeBudgetFrom(ctx)
	for _, path := range projects {
		if p.Full() || ctx.Err() != nil {
			return
		}
		name := strings.Join(append(append([]string(nil), path...), "VBA"), "/")
		limit := p.room.Left()
		if decoded != nil {
			limit = min(limit, decoded.Left())
		}
		project, err := vba.Read(f, path, limit)
		switch {
		case errors.Is(err, vba.ErrTooLarge):
			p.Lost.Add(warn.CapProjectSize, 1)
			continue
		case err != nil:
			p.Damaged = append(p.Damaged, clean.Label(name))
			continue
		}
		p.Lost.Add(warn.CapModules, int64(project.Unlisted))
		if project.Damaged {
			p.Damaged = append(p.Damaged, clean.Label(name))
		}
		total := int64(0)
		for _, m := range project.Modules {
			total += int64(len(m.Code))
		}
		if !decoded.Take(total) {
			p.budget = true
			return
		}
		if project.Undecoded > 0 {
			p.undecoded += project.Undecoded
			if !slices.Contains(p.codePages, project.CodePage) {
				p.codePages = append(p.codePages, project.CodePage)
			}
		}
		for _, m := range project.Modules {
			code, _ := clean.String([]byte(m.Code), clean.Verbatim)
			code = strings.TrimRight(code, "\n")
			if code == "" {
				continue
			}
			if !p.Add(gofastcore.Block{Kind: gofastcore.KindCode, Text: code, Attrs: map[string]string{
				gofastcore.AttrStored: gofastcore.StoredMacro, gofastcore.AttrName: p.Lost.Name(m.Name), gofastcore.AttrLanguage: "vba"}}) {
				return
			}
		}
		p.cut = p.cut || project.Cut
	}
}

// Part is a stored part being made.
type Part struct {
	gofastcore.Part
	// Damaged names what could not be read to its end.
	Damaged []string

	// Lost counts what the fixed bounds left out of it.
	Lost warn.Lost

	room      *gofastcore.TextBudget
	cut       bool // the limit on text was reached
	budget    bool // the decode budget ran out
	undecoded int  // characters of macro code not decoded
	codePages []int
}

// NewPart starts the stored part, which is numbered number. What it holds
// is taken from the text budget in ctx.
func NewPart(ctx context.Context, number int) *Part {
	return &Part{Part: gofastcore.Part{Kind: gofastcore.PartStored, Number: number},
		room: gofastcore.TextBudgetFrom(ctx)}
}

// Room is the text budget the part takes from.
func (p *Part) Room() *gofastcore.TextBudget { return p.room }

// Add adds a block, taking its cost from the budget. It reports false,
// and adds nothing more, once the budget has run out. Spaces and line
// breaks at the ends of the text of a block other than code are left
// out, as everywhere in a document (code is verbatim); a block with no
// text left is not added.
func (p *Part) Add(b gofastcore.Block) bool {
	if b.Kind != gofastcore.KindCode {
		if t := strings.TrimSpace(b.Text); len(t) != len(b.Text) {
			b.Text = t
		}
	}
	if b.Text == "" && b.Table == nil {
		return !p.Full()
	}
	if p.Full() || !p.room.Take(gofastcore.BlockCost+len(b.Text)) {
		p.cut = true
		return false
	}
	p.Blocks = append(p.Blocks, b)
	return true
}

// Cut records that the limit on text cut something short.
func (p *Part) Cut() { p.cut = true }

// Full reports whether a limit has stopped the reading.
func (p *Part) Full() bool { return p.cut || p.budget }

// Finish adds the part's warnings and completes it (gofastcore's
// Part.Finish). It reports whether a limit cut the reading short.
func (p *Part) Finish() (limited bool) {
	if p.budget {
		p.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the decompressed size limit was reached; what the file stores is cut off there")
	}
	if p.cut {
		p.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the limit on the amount of text was reached; what the file stores is cut off there")
	}
	if p.undecoded > 0 {
		pages := make([]string, len(p.codePages))
		for i, cp := range p.codePages {
			pages[i] = strconv.Itoa(cp)
		}
		p.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs, fmt.Sprintf(
			"%d characters of macro code in code page %s, which it does not define or which is not known here, are U+FFFD", p.undecoded, strings.Join(pages, ", ")))
	}
	capped := p.Lost.Say(&p.Part)
	if len(p.Blocks) == 0 {
		p.Warn(gofastcore.SeverityInfo, gofastcore.WarnPartNoText, "")
	}
	p.Part.Finish()
	return p.cut || p.budget || capped
}
