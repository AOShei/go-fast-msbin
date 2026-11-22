package gofastmsbin_test

import (
	"context"
	"errors"
	"fmt"
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
	for _, e := range entries {
		start := time.Now()
		doc, err := gofastmsbin.ExtractFile(context.Background(), e.Path, nil)
		if took := time.Since(start); took > 10*time.Second {
			t.Errorf("%s: %v", name(e), took)
		}
		checkOutcome(t, doc, err)
		// Files public bodies published are what is in use; the test files
		// of other projects lean towards the odd and the broken.
		kind := "test suites"
		if strings.HasPrefix(e.Name, "govdocs/") || strings.HasPrefix(e.Name, "govuk/") {
			kind = "published  "
		}
		counts[kind+"\t"+e.Format+"\t"+outcome(err)]++
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
	case errors.Is(err, gofastcore.ErrMalformed):
		return "malformed"
	case errors.Is(err, gofastcore.ErrLimitExceeded):
		return "over a limit"
	}
	return "other error"
}
