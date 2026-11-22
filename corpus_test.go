package gofastmsbin_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// Every file of the corpus gives a valid document or an error of a shared
// kind, in good time. Held-out files are run like the others and only
// counted. With -v the test prints what the corpus is made of: how many
// files of each format are of which generation, encrypted or not, and how
// many are not what their names say, for the files public bodies
// published and for the test files of other projects.
func TestCorpus(t *testing.T) {
	entries, err := msbintest.Corpus("testdata")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Skip("the corpus is not downloaded: python3 testdata/fetch.py")
	}
	counts := map[string]int{}
	var titled, withMacros, modules, withChildren, children, childrenRead, repaired int
	for _, e := range entries {
		start := time.Now()
		// Embedded files are read too, by this library where they are
		// of its formats.
		var child gofastcore.ChildFunc
		child = func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
			co := *o
			co.Child = child
			return gofastmsbin.Extract(ctx, r, size, &co)
		}
		doc, err := gofastmsbin.ExtractFile(context.Background(), e.Path, &gofastcore.Options{Child: child})
		if took := time.Since(start); took > 10*time.Second {
			t.Errorf("%s: %v", name(e), took)
		}
		checkOutcome(t, doc, err)
		if doc != nil {
			if doc.Metadata.Title != "" {
				titled++
			}
			n := 0
			for _, p := range doc.Parts {
				for _, b := range p.Blocks {
					if b.Attrs[gofastcore.AttrStored] == gofastcore.StoredMacro {
						n++
					}
				}
			}
			if n > 0 {
				withMacros++
				modules += n
			}
			if len(doc.Children) > 0 {
				withChildren++
			}
			for _, c := range doc.Children {
				children++
				if len(c.Parts) > 0 {
					childrenRead++
				}
			}
			for _, w := range doc.Warnings {
				if w.Code == gofastcore.WarnInputRepaired {
					repaired++
				}
			}
		}
		// Files public bodies published are what is in use; the test files
		// of other projects lean towards the odd and the broken.
		kind := "test suites"
		if strings.HasPrefix(e.Name, "govdocs/") || strings.HasPrefix(e.Name, "govuk/") {
			kind = "published  "
		}
		what := outcome(err)
		if doc != nil && len(doc.Parts) > 0 {
			// A format whose reader is not written yet gives its
			// properties, macros and embedded files only.
			if w, ok := warning(doc.Parts[0].Warnings, gofastcore.WarnPartFailed); ok && strings.Contains(w.Message, "not read by this version") {
				what = "properties only: content not read yet"
			}
		}
		counts[kind+"\t"+e.Format+"\t"+what]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&sb, "%5d  %s\n", counts[k], k)
	}
	t.Logf("%d files\n%s", len(entries), sb.String())
	t.Logf("%d documents have a title; %d have macros (%d modules); %d embed files (%d files, %d of them read as one of these formats); %d were repaired",
		titled, withMacros, modules, withChildren, children, childrenRead, repaired)
	if os.Getenv("GOFAST_MSBIN_CENSUS") != "" {
		if err := os.WriteFile(os.Getenv("GOFAST_MSBIN_CENSUS"), []byte(sb.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// name is how a failure names a corpus file: a held-out file by its hash
// only, so that nothing invites a look at it.
func name(e msbintest.Entry) string {
	if e.HeldOut() {
		return "held-out " + e.SHA256[:12]
	}
	return e.Name
}

// outcome is the kind of a result, without what is particular to a file.
func outcome(err error) string {
	switch {
	case err == nil:
		return "read"
	case errors.Is(err, gofastcore.ErrUnsupported):
		msg := strings.TrimSuffix(strings.TrimPrefix(err.Error(), "gofastmsbin: "), ": "+gofastcore.ErrUnsupported.Error())
		return msg
	case errors.Is(err, gofastcore.ErrPasswordRequired):
		return "encrypted: a password is required"
	case errors.Is(err, gofastcore.ErrMalformed):
		return "malformed"
	case errors.Is(err, gofastcore.ErrLimitExceeded):
		return "over a limit"
	}
	return "other error"
}
