package cfb_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-msbin/internal/cfb"
)

// tree builds the nodes of a file from paths with slashes, as sample has
// them.
func tree(streams map[string][]byte) *cfb.Node {
	root := &cfb.Node{Storage: true}
	paths := make([]string, 0, len(streams))
	for p := range streams {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		at := root
		parts := splitPath(p)
		for i, name := range parts {
			var child *cfb.Node
			for _, c := range at.Children {
				if c.Name == name {
					child = c
				}
			}
			if child == nil {
				child = &cfb.Node{Name: name, Storage: i < len(parts)-1}
				at.Children = append(at.Children, child)
			}
			if i == len(parts)-1 {
				child.Data = streams[p]
			}
			at = child
		}
	}
	return root
}

func TestWrittenFileIsReadBack(t *testing.T) {
	streams := sample()
	streams["Big"] = content(9<<20, 11) // past the 109 table sectors of the header
	for i := 0; i < 40; i++ {
		streams[fmt.Sprintf("many/s%02d", i)] = content(10+i*150, int64(20+i))
	}
	root := tree(streams)
	root.CLSID = [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	data := cfb.Write(root)
	f, err := cfb.Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if f.Damaged != "" {
		t.Fatal(f.Damaged)
	}
	if got := f.CLSID(); got != root.CLSID {
		t.Errorf("class of the root: %v", got)
	}
	for path, want := range streams {
		s, err := f.Open(splitPath(path)...)
		if err != nil {
			t.Errorf("%q: %v", path, err)
			continue
		}
		if got := readAll(t, s); !bytes.Equal(got, want) {
			t.Errorf("%q: %d bytes read, not the %d written", path, len(got), len(want))
		}
	}
	if got := len(f.List("many")); got != 40 {
		t.Errorf("%d entries in the storage of 40", got)
	}
}

// An empty file, and one with nothing but empty streams, are files too.
func TestWriteEmpty(t *testing.T) {
	for name, root := range map[string]*cfb.Node{
		"no children":  {Storage: true},
		"empty stream": {Storage: true, Children: []*cfb.Node{{Name: "a"}, {Name: "s", Storage: true}}},
	} {
		data := cfb.Write(root)
		f, err := cfb.Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := len(f.Names()); got != len(root.Children) {
			t.Errorf("%s: %d names", name, got)
		}
	}
}

// A storage of a file, written out, is a file with the storage's streams
// and class.
func TestExport(t *testing.T) {
	inner := map[string][]byte{"WordDocument": content(6000, 1), "1Table": content(300, 2), "ObjectPool/_1/\x01Ole": content(20, 3), "Empty": {}}
	outer := tree(map[string][]byte{"Workbook": content(5000, 4)})
	object := tree(inner)
	object.Name, object.CLSID = "MBD0001", [16]byte{9, 9}
	outer.Children = append(outer.Children, object)
	data := cfb.Write(outer)
	f, err := cfb.Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !f.IsStorage("MBD0001") || f.IsStorage("Workbook") {
		t.Error("IsStorage")
	}
	out, used, err := f.Export(1<<20, "MBD0001")
	if err != nil {
		t.Fatal(err)
	}
	if used != 6320 {
		t.Errorf("%d bytes read", used)
	}
	g, err := cfb.Open(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	if g.CLSID() != object.CLSID {
		t.Errorf("class: %v", g.CLSID())
	}
	for path, want := range inner {
		s, err := g.Open(splitPath(path)...)
		if err != nil {
			t.Errorf("%q: %v", path, err)
			continue
		}
		if got := readAll(t, s); !bytes.Equal(got, want) {
			t.Errorf("%q differs", path)
		}
	}
	if _, _, err := f.Export(6000, "MBD0001"); !errors.Is(err, cfb.ErrLimit) {
		t.Errorf("over the limit: %v", err)
	}
	if _, _, err := f.Export(1<<20, "Workbook"); !errors.Is(err, cfb.ErrNotFound) {
		t.Errorf("a stream: %v", err)
	}
}

// olefile, where the comparison tools are installed, reads the written
// file as this reader does.
func TestWriteAgainstOlefile(t *testing.T) {
	python := os.Getenv("GOFAST_MSBIN_PYTHON")
	if python == "" {
		python = filepath.Join("..", "..", "..", "bench-env", ".venv-ooxml", "bin", "python")
	}
	if _, err := os.Stat(python); err != nil {
		t.Skip("the Python comparison tools are not installed")
	}
	streams := sample()
	streams["Big"] = content(9<<20, 11)
	path := filepath.Join(t.TempDir(), "written.bin")
	if err := os.WriteFile(path, cfb.Write(tree(streams)), 0o600); err != nil {
		t.Fatal(err)
	}
	const script = `
import sys, hashlib, olefile
ole = olefile.OleFileIO(sys.argv[1])
for parts in sorted(ole.listdir()):
    data = ole.openstream(parts).read()
    print(len(parts), len(data), hashlib.sha256(data).hexdigest())
`
	out, err := exec.Command(python, "-I", "-c", script, path).CombinedOutput()
	if err != nil {
		t.Fatalf("olefile: %v\n%s", err, out)
	}
	if got := strings.Count(string(out), "\n"); got != len(streams) {
		t.Errorf("olefile lists %d streams of %d:\n%s", got, len(streams), out)
	}
	for p, data := range streams {
		want := fmt.Sprintf("%d %d %x", len(splitPath(p)), len(data), sha(data))
		if !strings.Contains(string(out), want) {
			t.Errorf("%q: olefile does not read what was written", p)
		}
	}
}
