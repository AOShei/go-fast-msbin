package gofastmsbin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// blockKey is a block as the comparison of twins sees it: its kind, its
// level and marker where it has them, and its text.
func blockKey(b *gofastcore.Block) string {
	text := squash(b.Text)
	if b.Table != nil {
		var rows []string
		for _, r := range b.Table.Rows {
			rows = append(rows, strings.Join(r, "|"))
		}
		text = squash(strings.Join(rows, "/"))
	}
	kind := b.Kind.String()
	switch b.Kind {
	case gofastcore.KindHeading:
		kind += fmt.Sprint(b.Level)
	case gofastcore.KindListItem:
		kind += fmt.Sprintf("%d[%s]", b.Level, b.Marker)
	case gofastcore.KindFootnote:
		kind += "[" + b.Marker + "]"
	case gofastcore.KindNote:
		kind += ":" + b.Attrs[gofastcore.AttrNoteType]
	}
	return kind + " " + text
}

// A Word document and the .docx file LibreOffice makes of it give the
// same blocks, read by this library and by go-fast-ooxml. Held-out files
// are counted and never named.
func TestDocTwins(t *testing.T) {
	if testing.Short() {
		t.Skip("a comparison over the whole corpus")
	}
	dir := filepath.Join("testdata", "corpus", "twins-doc")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no twins: run testdata/oracle_lo.py --twin --format doc")
	}
	bin := ooxmlCommand(t)
	entries, err := msbintest.Corpus("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var files, same, onlyHere, onlyThere, textSame, textHere, textThere int
	kinds := map[string][3]int{} // by kind: the same, only here, only in the twin
	shown := 0
	perFile := map[string]int{}
	note := func(e msbintest.Entry, format string, args ...any) {
		if !e.HeldOut() && shown < 120 && perFile[e.Path] < 4 {
			shown++
			perFile[e.Path]++
			t.Logf("%s: %s", filepath.Base(e.Path), fmt.Sprintf(format, args...))
		}
	}
	only := os.Getenv("GOFAST_MSBIN_ONLY")
	for _, e := range entries {
		twin := filepath.Join(dir, e.SHA256+".docx")
		if _, err := os.Stat(twin); e.Format != "doc" || err != nil {
			continue
		}
		if only != "" && !strings.Contains(e.Path, only) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		doc, err := gofastmsbin.ExtractFile(ctx, e.Path, nil)
		if err != nil || doc.Source.Format != "doc" {
			cancel()
			continue
		}
		if _, failed := warning(doc.Parts[0].Warnings, gofastcore.WarnPartFailed); failed {
			cancel()
			continue
		}
		out, err := exec.CommandContext(ctx, bin, "-quiet", twin).Output()
		cancel()
		var other gofastcore.Document
		if err != nil || json.Unmarshal(out, &other) != nil {
			continue
		}
		files++
		count := func(d *gofastcore.Document) (blocks map[string]int, words map[string]int) {
			blocks, words = map[string]int{}, map[string]int{}
			for i := range d.Parts {
				if d.Parts[i].Kind != gofastcore.PartBody {
					continue
				}
				for k := range d.Parts[i].Blocks {
					b := &d.Parts[i].Blocks[k]
					if b.Kind == gofastcore.KindImage {
						continue
					}
					key := blockKey(b)
					blocks[key]++
					for _, w := range strings.Fields(key[strings.IndexByte(key, ' ')+1:]) {
						words[w]++
					}
				}
			}
			return blocks, words
		}
		b1, w1 := count(doc)
		b2, w2 := count(&other)
		tally := func(key string, which, n int) {
			kind := key[:strings.IndexAny(key, " 0123456789[:")]
			v := kinds[kind]
			v[which] += n
			kinds[kind] = v
		}
		var here, there []string
		for key, n := range b1 {
			m := min(n, b2[key])
			same += m
			tally(key, 0, m)
			if n > m {
				onlyHere += n - m
				tally(key, 1, n-m)
				here = append(here, key)
			}
		}
		for key, n := range b2 {
			if m := min(n, b1[key]); n > m {
				onlyThere += n - m
				tally(key, 2, n-m)
				there = append(there, key)
			}
		}
		sort.Strings(here)
		sort.Strings(there)
		for i := 0; i < len(here) && i < 2; i++ {
			note(e, "only here:  %.160q", here[i])
		}
		for i := 0; i < len(there) && i < 2; i++ {
			note(e, "only there: %.160q", there[i])
		}
		for w, n := range w1 {
			m := min(n, w2[w])
			textSame += m
			textHere += n - m
		}
		for w, n := range w2 {
			textThere += n - min(n, w1[w])
		}
	}
	var names []string
	for k := range kinds {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		v := kinds[k]
		t.Logf("%-10s %7d the same, %6d only here, %6d only in the twin", k, v[0], v[1], v[2])
	}
	precision := float64(textSame) / float64(max(textSame+textHere, 1))
	recall := float64(textSame) / float64(max(textSame+textThere, 1))
	t.Logf("%d documents: %d blocks the same, %d only here, %d only in the twin; words: precision %.4f, recall %.4f",
		files, same, onlyHere, onlyThere, precision, recall)
}

// wordsOf counts the words of a text: runs of letters and digits.
func wordsOf(text string, into map[string]int) {
	for _, w := range strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r > 0x7F && r != 0xA0 && r != 0xAD && r != 0xFFFD)
	}) {
		into[w]++
	}
}

// The words of a Word document are the words of the plain text
// LibreOffice makes of it (testdata/oracle_lo.py --text). Held-out files
// are counted and never named.
func TestDocTextAgainstLibreOffice(t *testing.T) {
	if testing.Short() {
		t.Skip("a comparison over the whole corpus")
	}
	dir := filepath.Join("testdata", "corpus", "oracle-lo-doc")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no oracle output: run testdata/oracle_lo.py --text --format doc")
	}
	entries, err := msbintest.Corpus("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var files, same, here, there int
	type score struct {
		name string
		f1   float64
		n    int
	}
	var worst []score
	for _, e := range entries {
		if e.Format != "doc" {
			continue
		}
		want, err := os.ReadFile(filepath.Join(dir, e.SHA256+".txt"))
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		doc, err := gofastmsbin.ExtractFile(ctx, e.Path, nil)
		cancel()
		if err != nil || doc.Source.Format != "doc" {
			continue
		}
		if _, failed := warning(doc.Parts[0].Warnings, gofastcore.WarnPartFailed); failed {
			continue
		}
		files++
		w1, w2 := map[string]int{}, map[string]int{}
		for i := range doc.Parts[0].Blocks {
			b := &doc.Parts[0].Blocks[i]
			// What LibreOffice's text does not have: the codes of fields,
			// text a tracked change removed, footnotes and endnotes, and
			// headers and footers, which it gives once and with other
			// page numbers. The twins are what these are compared with.
			if b.Kind == gofastcore.KindNote || b.Kind == gofastcore.KindHeader || b.Kind == gofastcore.KindFooter || b.Kind == gofastcore.KindFootnote {
				continue
			}
			wordsOf(b.Text, w1)
			wordsOf(b.Marker, w1)
			if b.Table != nil {
				for _, row := range b.Table.Rows {
					for _, cell := range row {
						wordsOf(cell, w1)
					}
				}
			}
		}
		wordsOf(strings.TrimPrefix(string(want), "\xef\xbb\xbf"), w2)
		s, h, th := 0, 0, 0
		for w, n := range w1 {
			m := min(n, w2[w])
			s += m
			h += n - m
		}
		for w, n := range w2 {
			th += n - min(n, w1[w])
		}
		same, here, there = same+s, here+h, there+th
		if total := 2*s + h + th; total > 200 {
			name := e.SHA256[:12]
			if !e.HeldOut() {
				name = filepath.Base(e.Path)
			}
			worst = append(worst, score{name, 2 * float64(s) / float64(total), total})
		}
	}
	sort.Slice(worst, func(i, j int) bool { return worst[i].f1 < worst[j].f1 })
	for i := 0; i < len(worst) && i < 25; i++ {
		t.Logf("%-50s F1 %.3f over %d words", worst[i].name, worst[i].f1, worst[i].n/2)
	}
	precision := float64(same) / float64(max(same+here, 1))
	recall := float64(same) / float64(max(same+there, 1))
	f1 := 2 * precision * recall / (precision + recall)
	t.Logf("%d documents: word precision %.4f, recall %.4f, F1 %.4f against LibreOffice's text", files, precision, recall, f1)
}
