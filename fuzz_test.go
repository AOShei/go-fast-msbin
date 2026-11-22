package gofastmsbin_test

import (
	"errors"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// checkOutcome holds the result of Extract to the contract: a document
// that passes Validate, or an error that wraps one of the shared errors,
// and never both.
func checkOutcome(t testing.TB, doc *gofastcore.Document, err error) {
	t.Helper()
	if err != nil {
		if doc != nil {
			t.Fatalf("a document and an error: %v", err)
		}
		for _, shared := range []error{gofastcore.ErrUnsupported, gofastcore.ErrMalformed, gofastcore.ErrLimitExceeded,
			gofastcore.ErrPasswordRequired, gofastcore.ErrWrongPassword, gofastcore.ErrNoPartsSelected} {
			if errors.Is(err, shared) {
				return
			}
		}
		t.Fatalf("an error of no shared kind: %v", err)
	}
	if doc == nil {
		t.Fatal("neither a document nor an error")
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("invalid document: %v", err)
	}
}

// FuzzExtract feeds arbitrary bytes to Extract. Whatever the input, the
// call must return a valid document or an error that wraps one of the
// shared errors; it must not panic, hang or exhaust memory.
func FuzzExtract(f *testing.F) {
	f.Add(msbintest.Compound(map[string][]byte{"WordDocument": fib(0xA5EC, 0x00C1, 0x0300), "1Table": u16s(1, 1)}))
	f.Add(msbintest.Compound(map[string][]byte{"Workbook": append(record(0x0809, u16s(0x0600, 5, 0, 0, 0, 0, 0, 0)...), record(0x002F, u16s(1, 4, 2)...)...)}))
	f.Add(msbintest.Compound(map[string][]byte{"PowerPoint Document": {1}, "Current User": currentUser(0xF3D1C4DF, 0x03F4)}))
	f.Add(msbintest.CompoundV4(map[string][]byte{"Book": record(0x0809, u16s(0x0500, 5, 0, 0)...), "a/b": make([]byte, 5000)}))
	f.Add([]byte(`{\rtf1 }`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		// An input that takes seconds is a bug: crash the worker, so that
		// the fuzzer keeps the input.
		timer := time.AfterFunc(5*time.Second, func() { panic("Extract took more than 5s") })
		defer timer.Stop()
		doc, err := extract(data, nil)
		checkOutcome(t, doc, err)
	})
}
