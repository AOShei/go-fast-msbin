package gofastmsbin

import (
	"fmt"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/cfb"
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/oleps"
	"github.com/AOShei/go-fast-msbin/internal/unit"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// customPrefix comes before the name of a property the author defined,
// in Metadata.Extra, as go-fast-ooxml has it.
const customPrefix = "custom."

// linkedProperty marks, in the identifier of a custom property, the
// companion that says which bookmark or cell the property is linked to;
// it is not a property of its own.
const linkedProperty = 0x01000000

// metadata reads the document's properties from its two property-set
// streams. A stream that is missing gives nothing; one that is damaged
// gives what could be read of it, and a warning.
func metadata(f *cfb.File, open func(name string) (unit.Stream, error)) (gofastcore.Metadata, []gofastcore.Warning) {
	var m gofastcore.Metadata
	var ws []gofastcore.Warning
	var lost warn.Lost
	removed, undecoded, custom := 0, 0, 0
	text := func(v oleps.Value) string {
		s, n := clean.String([]byte(v.String()), clean.Prose)
		removed += n
		return s
	}
	extra := func(key, v string) {
		if v == "" {
			return
		}
		if m.Extra == nil {
			m.Extra = map[string]string{}
		}
		m.Extra[key] = v
	}
	for _, name := range []string{oleps.SummaryStream, oleps.DocSummaryStream} {
		if !f.Has(name) || f.IsStorage(name) {
			continue
		}
		label := strings.TrimPrefix(name, "\x05")
		s, err := open(name)
		if err != nil {
			ws = append(ws, gofastcore.Warning{Code: WarnPropertiesDamaged, Severity: gofastcore.SeverityWarning, Message: label})
			continue
		}
		data, cut, err := unit.ReadAll(s, oleps.MaxStream)
		res := oleps.Read(data)
		if res.Damaged || cut || err != nil {
			ws = append(ws, gofastcore.Warning{Code: WarnPropertiesDamaged, Severity: gofastcore.SeverityWarning, Message: label})
		}
		undecoded += res.Undecoded
		lost.AddCut(warn.CapPropertyValue, res.ValuesCut, res.ValueBytes)
		lost.Add(warn.CapProperties, res.Past)
		for i := range res.Sets {
			set := &res.Sets[i]
			switch set.Kind {
			case oleps.Summary:
				for _, p := range set.Properties {
					v := text(p.Value)
					if v == "" {
						continue
					}
					switch p.ID {
					case oleps.PIDTitle:
						m.Title = v
					case oleps.PIDSubject:
						m.Subject = v
					case oleps.PIDAuthor:
						m.Authors = []string{v}
					case oleps.PIDKeywords:
						m.Keywords = []string{v}
					case oleps.PIDComments:
						extra("description", v)
					case oleps.PIDTemplate:
						extra("template", v)
					case oleps.PIDLastAuthor:
						extra("last_modified_by", v)
					case oleps.PIDRevision:
						extra("revision", v)
					case oleps.PIDAppName:
						m.Application = v
					case oleps.PIDCreated:
						if p.Value.Type == oleps.Time {
							m.Created = p.Value.Time
						}
					case oleps.PIDSaved:
						if p.Value.Type == oleps.Time {
							m.Modified = p.Value.Time
						}
					}
				}
			case oleps.DocSummary:
				for _, p := range set.Properties {
					switch p.ID {
					case oleps.PIDCategory:
						extra("category", text(p.Value))
					case oleps.PIDManager:
						extra("manager", text(p.Value))
					case oleps.PIDCompany:
						extra("company", text(p.Value))
					case oleps.PIDContentStatus:
						extra("content_status", text(p.Value))
					case oleps.PIDVersion:
						// The version of the program: the major number in
						// the high half, the minor in the low.
						if p.Value.Type == oleps.Int && p.Value.Int > 0 {
							extra("app_version", fmt.Sprintf("%d.%04d", p.Value.Int>>16&0xFFFF, p.Value.Int&0xFFFF))
						}
					}
				}
			case oleps.User:
				for _, p := range set.Properties {
					if p.Name == "" || p.ID&linkedProperty != 0 || p.Value.Type == oleps.None {
						continue
					}
					if custom == warn.MaxCustomProps {
						lost.Add(warn.CapCustomProps, 1)
						continue
					}
					key, n := clean.String([]byte(p.Name), clean.Prose)
					removed += n
					if clean.LabelCut(key) {
						lost.AddCut(warn.CapPropertyName, 1, int64(len(key)-clean.MaxLabel))
					}
					key = strings.ToLower(clean.Label(key))
					if key == "" {
						continue
					}
					custom++
					extra(customPrefix+key, text(p.Value))
				}
			}
		}
	}
	ws = append(ws, lost.Warnings()...)
	if undecoded > 0 {
		ws = append(ws, gofastcore.Warning{Code: gofastcore.WarnTextUnmappedGlyphs, Severity: gofastcore.SeverityWarning,
			Message: fmt.Sprintf("%d characters of the document properties, in a code page that does not define them or is not known here, are U+FFFD", undecoded)})
	}
	if removed > 0 {
		ws = append(ws, gofastcore.Warning{Code: WarnControlCharsRemoved, Severity: gofastcore.SeverityInfo,
			Message: fmt.Sprintf("%d in document properties", removed)})
	}
	return m, ws
}
