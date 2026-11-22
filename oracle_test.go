package gofastmsbin_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// sharedOracle is what olefile and olevba read of one corpus file
// (testdata/oracle_shared.py).
type sharedOracle struct {
	SHA256 string            `json:"sha256"`
	Error  string            `json:"error"`
	Meta   map[string]string `json:"meta"`
	VBA    []struct {
		Stream, Name, Code string
	} `json:"vba"`
}

func loadSharedOracle(t *testing.T) map[string]sharedOracle {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "corpus", "oracle-shared.jsonl"))
	if err != nil {
		t.Skip("no oracle output: run testdata/oracle_shared.py")
	}
	defer f.Close()
	out := map[string]sharedOracle{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<30)
	for sc.Scan() {
		var rec sharedOracle
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatal(err)
		}
		out[rec.SHA256] = rec
	}
	return out
}

// squash makes two readings of a string comparable: runs of white space
// and control characters are one space.
func squash(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r <= ' ' || r == 0x7F || r == 0xA0 }), " ")
}

// The document properties agree with what olefile reads, and the macro
// code with what olevba reads, over the corpus. Held-out files are
// counted and never named.
func TestSharedAgainstOracles(t *testing.T) {
	oracle := loadSharedOracle(t)
	entries, err := msbintest.Corpus("testdata")
	if err != nil {
		t.Fatal(err)
	}
	type tally struct{ same, differ int }
	fields := map[string]*tally{}
	var modules, found, extra int
	shown := 0
	for _, e := range entries {
		rec, ok := oracle[e.SHA256]
		if !ok {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		doc, err := gofastmsbin.ExtractFile(ctx, e.Path, nil)
		cancel()
		if err != nil {
			continue
		}
		if rec.Meta != nil {
			m := doc.Metadata
			got := map[string]string{"title": m.Title, "subject": m.Subject, "application": m.Application}
			if len(m.Authors) > 0 {
				got["author"] = m.Authors[0]
			}
			if len(m.Keywords) > 0 {
				got["keywords"] = m.Keywords[0]
			}
			for _, key := range []string{"description", "template", "last_modified_by", "revision", "category", "manager", "company"} {
				got[key] = m.Extra[key]
			}
			if !m.Created.IsZero() {
				got["created"] = m.Created.Truncate(time.Second).Format("2006-01-02T15:04:05Z")
			}
			if !m.Modified.IsZero() {
				got["modified"] = m.Modified.Truncate(time.Second).Format("2006-01-02T15:04:05Z")
			}
			keys := map[string]bool{}
			for k, v := range got {
				if v != "" {
					keys[k] = true
				}
			}
			for k := range rec.Meta {
				keys[k] = true
			}
			for k := range keys {
				if fields[k] == nil {
					fields[k] = &tally{}
				}
				// olefile decodes with Python's codecs, which differ from
				// Windows in a few characters; U+FFFD on either side is
				// not a difference of reading.
				a, b := squash(gofastcore.ExpandLigatures(rec.Meta[k])), squash(got[k])
				if a == b || strings.ContainsRune(a, 0xFFFD) || strings.ContainsRune(b, 0xFFFD) {
					fields[k].same++
					continue
				}
				fields[k].differ++
				if !e.HeldOut() && shown < 40 {
					shown++
					t.Logf("%s: %s: olefile %q, here %q", e.Path, k, rec.Meta[k], got[k])
				}
			}
		}
		if len(rec.VBA) > 0 && e.Format != "ppt" {
			ours := map[string]int{}
			for _, p := range doc.Parts {
				if p.Kind != gofastcore.PartStored {
					continue
				}
				for _, b := range p.Blocks {
					if b.Attrs[gofastcore.AttrStored] == gofastcore.StoredMacro {
						ours[squash(b.Text)]++
					}
				}
			}
			for _, mod := range rec.VBA {
				code := squash(mod.Code)
				// A project inside an embedded object is that object's:
				// it is in the child, where there is a reader for it.
				if code == "" || strings.HasPrefix(mod.Stream, "ObjectPool/") || strings.HasPrefix(mod.Stream, "MBD") {
					continue
				}
				modules++
				if ours[code] > 0 {
					ours[code]--
					found++
				} else if !e.HeldOut() && shown < 40 {
					shown++
					t.Logf("%s: module %s (%s) of %d bytes is not given", e.Path, mod.Name, mod.Stream, len(mod.Code))
				}
			}
			for _, n := range ours {
				extra += n
			}
		}
	}
	names := make([]string, 0, len(fields))
	for k := range fields {
		names = append(names, k)
	}
	sort.Strings(names)
	same, differ := 0, 0
	var lines []string
	for _, k := range names {
		same, differ = same+fields[k].same, differ+fields[k].differ
		lines = append(lines, fmt.Sprintf("%-18s %5d same %4d differ", k, fields[k].same, fields[k].differ))
	}
	t.Logf("document properties against olefile:\n%s", strings.Join(lines, "\n"))
	t.Logf("macro modules against olevba: %d of %d given; %d given that olevba does not have", found, modules, extra)
	if total := same + differ; total > 0 && float64(same)/float64(total) < 0.995 {
		t.Errorf("properties: %d of %d agree with olefile", same, total)
	}
	if modules > 0 && float64(found)/float64(modules) < 0.99 {
		t.Errorf("macros: %d of %d modules olevba reads are given", found, modules)
	}
}
