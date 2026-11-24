package gofastmsbin_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// counted counts the bytes read through it.
type counted struct {
	r io.ReaderAt
	n int64
}

func (c *counted) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	c.n += int64(n)
	return n, err
}

// checkDetect holds Detect to what Extract made of the same input: the
// format of a document that was read, and nothing for an input refused
// as none of the three formats.
func checkDetect(t testing.TB, what string, format string, doc *gofastcore.Document, err error) {
	t.Helper()
	switch {
	case err == nil && format != doc.Source.Format:
		t.Fatalf("%s: Detect says %q, the document is %q", what, format, doc.Source.Format)
	case err != nil && format != "" && errors.Is(err, gofastcore.ErrUnsupported) &&
		(strings.Contains(err.Error(), "not one of the binary Office formats") || strings.Contains(err.Error(), "not a compound file")):
		t.Fatalf("%s: Detect says %q of a file refused as none of the formats: %v", what, format, err)
	}
}

func TestDetect(t *testing.T) {
	big := make([]byte, 4<<20)
	deck := map[string][]byte{"PowerPoint Document": {1}, "Current User": currentUser(0xF3D1C4DF, 0x03F4)}
	for _, c := range []struct {
		name string
		data []byte
		want string
	}{
		{"a Word document", msbintest.Compound(wordFile()), "doc"},
		{"a workbook", msbintest.Compound(bookFile()), "xls"},
		{"a workbook of Excel 95", msbintest.Compound(map[string][]byte{"Book": record(0x0809, u16s(0x0500, 5, 0, 0)...)}), "xls"},
		{"a presentation", msbintest.Compound(deck), "ppt"},
		{"a presentation of PowerPoint 95, which is refused", msbintest.Compound(map[string][]byte{"PP40": {1}}), "ppt"},
		{"a Word document of Word 2's kind in a compound file, which is refused", msbintest.Compound(map[string][]byte{"WordDocument": fib(0xA5DB, 33, 0)}), "doc"},
		{"a Word document with a workbook in it", msbintest.Compound(with(wordFile(), map[string][]byte{"ObjectPool/_1/Workbook": bookFile()["Workbook"]})), "doc"},
		{"a file with sectors of 4096 bytes", msbintest.CompoundV4(map[string][]byte{"Book": record(0x0809, u16s(0x0500, 5, 0, 0)...), "a/b": make([]byte, 5000)}), "xls"},
		{"an Outlook message", msbintest.Compound(map[string][]byte{"__properties_version1.0": make([]byte, 32), "__substg1.0_0037001F": {'a', 0}}), ""},
		{"a workbook only inside a storage", msbintest.Compound(map[string][]byte{"MBD1/Workbook": bookFile()["Workbook"], "\x01CompObj": {1}}), ""},
		{"a compound file with nothing in it", msbintest.Compound(map[string][]byte{}), ""},
		{"an RTF document", []byte(`{\rtf1 }`), ""},
		{"a zip archive", []byte("PK\x03\x04"), ""},
		{"a workbook of Excel 4, which is no compound file", record(0x0409, u16s(0, 16, 0)...), ""},
		{"nothing", nil, ""},
		{"a large workbook", msbintest.Compound(map[string][]byte{"Workbook": big, "Other": big}), "xls"},
	} {
		r := &counted{r: bytes.NewReader(c.data)}
		got := gofastmsbin.Detect(r, int64(len(c.data)))
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
		// The header, the directory and the sectors of the allocation
		// table its chain goes through: not the streams.
		if r.n > 64<<10 {
			t.Errorf("%s: %d bytes read of %d", c.name, r.n, len(c.data))
		}
		doc, err := extract(c.data, nil)
		checkDetect(t, c.name, got, doc, err)
		// Cut short anywhere, it says what it can and does not fail.
		if len(c.data) < 1<<20 {
			for n := 0; n < len(c.data); n += max(len(c.data)/97, 1) {
				gofastmsbin.Detect(bytes.NewReader(c.data[:n]), int64(n))
				gofastmsbin.Detect(bytes.NewReader(c.data[:n]), int64(len(c.data)))
			}
		}
	}
}

// Over the corpus Detect names the format Extract reads a file as, and
// names none where Extract refuses the file as none of the three.
func TestDetectOverCorpus(t *testing.T) {
	entries, err := msbintest.Corpus("testdata")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Skip("the corpus is not downloaded: python3 testdata/fetch.py")
	}
	counts := map[string]int{}
	var read, size int64
	for _, e := range entries {
		f, err := os.Open(e.Path)
		if err != nil {
			t.Fatal(err)
		}
		r := &counted{r: f}
		format := gofastmsbin.Detect(r, e.Size)
		read, size = read+r.n, size+e.Size
		doc, err := gofastmsbin.Extract(context.Background(), f, e.Size, nil)
		f.Close()
		checkDetect(t, name(e), format, doc, err)
		switch {
		case err == nil:
			counts[format+", read"]++
		case format == "":
			counts["none, refused"]++
		default:
			counts[format+", refused or failed"]++
		}
	}
	t.Logf("%d files: %v; Detect read %d of %d bytes", len(entries), counts, read, size)
}

// detect is Detect of bytes in memory.
func detect(data []byte) string {
	return gofastmsbin.Detect(bytes.NewReader(data), int64(len(data)))
}
