package gofastmsbin_test

import (
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/cfb/cfbtest"
	t "github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// A file may name UTF-16 (code page 1200 or 1201) as the code page of
// strings it keeps as bytes, which they cannot be in. Such strings are
// read as in a code page that is not known, the ASCII kept and the rest
// U+FFFD and counted, and never as pairs of bytes.
func TestStringsOfBytesAreNotUTF16(tt *testing.T) {
	for _, cp := range []uint16{1200, 1201} {
		globals := [][]byte{t.CodePage(cp), t.Font5("Arial"), t.XF5(0, 0)}
		sheet := [][]byte{t.Label5(0, 0, 0, "plain"), t.Label5(0, 1, 0, "caf\xe9s")}
		data := cfbtest.Compound(map[string][]byte{"Book": t.Workbook5(globals, t.Sheet{Name: "One", Records: sheet})})
		doc := mustExtract(tt, data, &gofastcore.Options{})
		if len(doc.Parts) != 1 || doc.Parts[0].Name != "One" {
			tt.Fatalf("code page %d: %+v", cp, doc.Parts)
		}
		if got := rows(tt, &doc.Parts[0]); len(got) != 1 || len(got[0]) != 2 || got[0][0] != "plain" || got[0][1] != "caf�s" {
			tt.Errorf("code page %d: cells %q", cp, got)
		}
		w, ok := warning(doc.Parts[0].Warnings, gofastcore.WarnTextUnmappedGlyphs)
		if !ok || !strings.Contains(w.Message, "1 characters in code page "+map[uint16]string{1200: "1200", 1201: "1201"}[cp]) {
			tt.Errorf("code page %d: warning %+v", cp, doc.Parts[0].Warnings)
		}
	}

	// A property set in code page 1201: 1200 is the set's own UTF-16,
	// which its strings are then written in; 1201 is not.
	summary := t.PropertyStream(t.Section{Format: t.FmtSummary, CodePage: 1201, Props: []t.Prop{{ID: 2, Value: "A title"}, {ID: 3, Value: "Caf\xe9"}}})
	data := cfbtest.Compound(map[string][]byte{
		"Book":                   t.Workbook5([][]byte{t.Font5("Arial"), t.XF5(0, 0)}, t.Sheet{Name: "One", Records: [][]byte{t.Label5(0, 0, 0, "x")}}),
		"\x05SummaryInformation": summary,
	})
	doc := mustExtract(tt, data, &gofastcore.Options{})
	if doc.Metadata.Title != "A title" || doc.Metadata.Subject != "Caf�" {
		tt.Errorf("properties: title %q, subject %q", doc.Metadata.Title, doc.Metadata.Subject)
	}
}
