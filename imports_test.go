package gofastmsbin_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// imports returns the import paths of every non-test Go file under dir,
// by file.
func imports(t *testing.T, dir string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) && path != dir {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range parsed.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			out[path] = append(out[path], p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The library's packages depend on the standard library and go-fast-core
// only. The command, and nothing else, may also import go-fast-ooxml, to
// read the Office Open XML files embedded in a document.
func TestImportsStandardLibraryAndGoFastOnly(t *testing.T) {
	files := imports(t, ".")
	if len(files) < 10 {
		t.Fatalf("only %d files were checked", len(files))
	}
	within := func(p, module string) bool { return p == module || strings.HasPrefix(p, module+"/") }
	for file, paths := range files {
		command := strings.HasPrefix(filepath.ToSlash(file), "cmd/")
		for _, p := range paths {
			first, _, _ := strings.Cut(p, "/")
			switch {
			case !strings.Contains(first, "."):
			case within(p, "github.com/AOShei/go-fast-core"), within(p, "github.com/AOShei/go-fast-msbin"):
			case command && within(p, "github.com/AOShei/go-fast-ooxml"):
			default:
				t.Errorf("%s imports %q", file, p)
			}
		}
	}
}

// Source files hold no invisible or look-alike characters: a test that
// needs one writes it as an escape.
func TestSourceHygiene(t *testing.T) {
	checked := 0
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		line := 1
		for _, r := range string(data) {
			bad := unicode.In(r, unicode.Cf, unicode.Co, unicode.Zl, unicode.Zp) ||
				unicode.Is(unicode.Cc, r) && r != '\n' && r != '\t' ||
				unicode.Is(unicode.Zs, r) && r != ' ' ||
				r >= 0xFB00 && r <= 0xFB06
			if bad {
				t.Errorf("%s:%d: character U+%04X", path, line, r)
			}
			if r == '\n' {
				line++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 15 {
		t.Fatalf("only %d files were checked", checked)
	}
}

// The packages that read a file work on the bytes they are given and on
// nothing else: they open no file, start no program and reach no network,
// because they do not import the packages that would let them. Only the
// public package opens a file, in ExtractFile.
func TestReadersImportNothingThatReachesOutside(t *testing.T) {
	files := imports(t, "internal")
	for file, paths := range files {
		if strings.Contains(file, "msbintest") {
			continue
		}
		for _, p := range paths {
			for _, banned := range []string{"os", "net", "syscall", "unsafe", "plugin", "runtime/debug"} {
				if p == banned || strings.HasPrefix(p, banned+"/") {
					t.Errorf("%s imports %q", file, p)
				}
			}
		}
	}
}
