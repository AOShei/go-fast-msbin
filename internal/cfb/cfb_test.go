package cfb_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-msbin/internal/cfb"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
)

func content(n int, seed int64) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

// sample is a compound file with streams of every kind: empty, short
// (kept in the short-stream container), just under and at the size where
// a stream stops being short, long, and inside storages.
func sample() map[string][]byte {
	return map[string][]byte{
		"Empty":                  {},
		"Tiny":                   []byte("ten bytes!"),
		"OneShortSector":         content(64, 1),
		"JustShort":              content(4095, 2),
		"JustLong":               content(4096, 3),
		"Long":                   content(100_000, 4),
		"EncryptionInfo":         content(1077, 5),
		"\x06DataSpaces/Version": content(76, 6),
		"\x06DataSpaces/TransformInfo/StrongEncryptionTransform/\x06Primary": content(200, 7),
		"Größe":   content(300, 8),
		"a/b/c/d": content(5000, 9),
	}
}

func readAll(t testing.TB, s *cfb.Stream) []byte {
	t.Helper()
	out := make([]byte, s.Size())
	if n, err := s.ReadAt(out, 0); err != nil && err != io.EOF || int64(n) != s.Size() {
		t.Fatalf("read %d of %d bytes: %v", n, s.Size(), err)
	}
	return out
}

func TestStreamsAreReadBack(t *testing.T) {
	for name, build := range map[string]func(map[string][]byte) []byte{"sectors of 512 bytes": msbintest.Compound, "sectors of 4096 bytes": msbintest.CompoundV4} {
		streams := sample()
		data := build(streams)
		f, err := cfb.Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		rng := rand.New(rand.NewSource(1))
		for path, want := range streams {
			parts := splitPath(path)
			s, err := f.Open(parts...)
			if err != nil {
				t.Errorf("%s: %q: %v", name, path, err)
				continue
			}
			if got := readAll(t, s); !bytes.Equal(got, want) {
				t.Errorf("%s: %q: %d bytes read, not the %d written", name, path, len(got), len(want))
			}
			// Any piece of it, at any offset.
			for i := 0; i < 50 && len(want) > 0; i++ {
				off := rng.Intn(len(want))
				n := 1 + rng.Intn(min(len(want)-off, 9000))
				buf := make([]byte, n)
				if got, err := s.ReadAt(buf, int64(off)); err != nil || got != n || !bytes.Equal(buf, want[off:off+n]) {
					t.Errorf("%s: %q: %d bytes at %d: %d read, %v", name, path, n, off, got, err)
					break
				}
			}
			// Past the end there is nothing, and that is said.
			buf := make([]byte, 10)
			if n, err := s.ReadAt(buf, int64(len(want))-3); len(want) >= 3 && (n != 3 || err != io.EOF) {
				t.Errorf("%s: %q: reading over the end: %d, %v", name, path, n, err)
			}
			if !f.Has(parts...) {
				t.Errorf("%s: Has(%q) is false", name, path)
			}
		}
		// Names are found whatever their case, as the format has it; a
		// storage is not a stream; what is not there is not found.
		if _, err := f.Open("encryptioninfo"); err != nil {
			t.Errorf("%s: a name in another case: %v", name, err)
		}
		if _, err := f.Open("\x06DataSpaces"); !errors.Is(err, cfb.ErrNotFound) {
			t.Errorf("%s: a storage opened as a stream: %v", name, err)
		}
		if !f.Has("\x06DataSpaces") || f.Has("Absent") || f.Has("a", "x") || f.Has() {
			t.Errorf("%s: Has is wrong", name)
		}
		if _, err := f.Open("a", "b", "nothing"); !errors.Is(err, cfb.ErrNotFound) {
			t.Errorf("%s: a stream that is not there: %v", name, err)
		}
		if names := f.Names(); len(names) != 10 {
			t.Errorf("%s: names in the root: %q", name, names)
		}
	}
}

func splitPath(path string) []string {
	var parts []string
	for len(path) > 0 {
		i := bytes.IndexByte([]byte(path), '/')
		if i < 0 {
			return append(parts, path)
		}
		parts, path = append(parts, path[:i]), path[i+1:]
	}
	return parts
}

// A file whose allocation table is too long for the header to list is
// continued in sectors of the file.
func TestLargeFile(t *testing.T) {
	want := content(9<<20, 1)
	data := msbintest.Compound(map[string][]byte{"EncryptedPackage": want, "EncryptionInfo": []byte("info")})
	if binary.LittleEndian.Uint32(data[72:]) == 0 {
		t.Fatal("the file is not large enough to need the continuation")
	}
	f, err := cfb.Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	s, err := f.Open("EncryptedPackage")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readAll(t, s), want) {
		t.Error("the stream read is not the stream written")
	}
}

func TestNotACompoundFile(t *testing.T) {
	for name, data := range map[string][]byte{
		"empty":      nil,
		"a zip":      append([]byte("PK\x03\x04"), make([]byte, 600)...),
		"too short":  {0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1},
		"only zeros": make([]byte, 2048),
	} {
		if _, err := cfb.Open(bytes.NewReader(data), int64(len(data))); !errors.Is(err, cfb.ErrNotCompound) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// outcome opens a damaged file and reads every stream it can find. The
// file may be refused, or its streams may fail to open or to read; none
// of that may take long, read more than the file holds, or crash.
func outcome(t testing.TB, what string, data []byte) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f, err := cfb.Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			if !errors.Is(err, cfb.ErrDamaged) && !errors.Is(err, cfb.ErrNotCompound) && !errors.Is(err, cfb.ErrLimit) {
				t.Errorf("%s: an error of no known kind: %v", what, err)
			}
			return
		}
		// What opens, opens for its directory alone, and names the same.
		if d, err := cfb.OpenDirectory(bytes.NewReader(data), int64(len(data))); err != nil {
			t.Errorf("%s: opens, but not for its directory alone: %v", what, err)
		} else if got, want := strings.Join(d.Names(), "\x00"), strings.Join(f.Names(), "\x00"); got != want {
			t.Errorf("%s: opened for its directory alone it names %q, opened whole %q", what, got, want)
		}
		paths := [][]string{{"a", "b", "c", "d"}, {"\x06DataSpaces", "Version"}}
		for _, name := range f.Names() {
			paths = append(paths, []string{name})
			// A storage written out as a file of its own is a file this
			// reader opens, within the limit it was given.
			if !f.IsStorage(name) {
				continue
			}
			out, used, err := f.Export(int64(len(data)), name)
			if err != nil {
				if !errors.Is(err, cfb.ErrDamaged) && !errors.Is(err, cfb.ErrLimit) && !errors.Is(err, cfb.ErrNotFound) {
					t.Errorf("%s: export of %q: an error of no known kind: %v", what, name, err)
				}
				continue
			}
			if used > int64(len(data)) {
				t.Errorf("%s: export of %q read %d bytes of a file of %d", what, name, used, len(data))
			}
			if _, err := cfb.Open(bytes.NewReader(out), int64(len(out))); err != nil {
				t.Errorf("%s: the export of %q does not open: %v", what, name, err)
			}
		}
		for _, path := range paths {
			s, err := f.Open(path...)
			if err != nil {
				if !errors.Is(err, cfb.ErrDamaged) && !errors.Is(err, cfb.ErrNotFound) {
					t.Errorf("%s: %q: an error of no known kind: %v", what, path, err)
				}
				continue
			}
			if s.Size() > int64(len(data)) {
				t.Errorf("%s: %q: a stream of %d bytes in a file of %d", what, path, s.Size(), len(data))
				continue
			}
			buf := make([]byte, s.Size())
			if n, err := s.ReadAt(buf, 0); err != nil && err != io.EOF && !errors.Is(err, cfb.ErrDamaged) || int64(n) > s.Size() {
				t.Errorf("%s: %q: %d bytes, %v", what, path, n, err)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: not done after ten seconds", what)
	}
}

func TestDamagedFiles(t *testing.T) {
	sound := msbintest.Compound(sample())
	le := binary.LittleEndian
	change := func(edit func(b []byte)) []byte {
		b := append([]byte(nil), sound...)
		edit(b)
		return b
	}
	// The allocation table is the first sector after the header, and the
	// directory follows it (see the writer).
	fat := func(b []byte) []byte { return b[512:] }
	dir := func(b []byte) []byte { return b[512*(1+int(le.Uint32(b[48:]))):] }
	cases := map[string][]byte{
		"cut in the header":                   sound[:300],
		"cut after the header":                sound[:512],
		"cut in the allocation table":         sound[:700],
		"cut in the directory":                sound[:512*3+100],
		"cut in a stream":                     sound[:len(sound)-40_000],
		"the wrong byte order mark":           change(func(b []byte) { le.PutUint16(b[28:], 0xFEFF) }),
		"sectors of an impossible size":       change(func(b []byte) { le.PutUint16(b[30:], 31) }),
		"short sectors of an impossible size": change(func(b []byte) { le.PutUint16(b[32:], 0) }),
		"no allocation table":                 change(func(b []byte) { le.PutUint32(b[44:], 0) }),
		"a huge allocation table":             change(func(b []byte) { le.PutUint32(b[44:], 0x7FFFFFFF) }),
		"the table lists a sector not there":  change(func(b []byte) { le.PutUint32(b[76:], 0x00FFFFFF) }),
		"the directory starts nowhere":        change(func(b []byte) { le.PutUint32(b[48:], 0x00FFFFFF) }),
		"the directory chain loops":           change(func(b []byte) { n := le.Uint32(b[48:]); le.PutUint32(fat(b)[4*n:], n) }),
		"every chain loops": change(func(b []byte) {
			for i := 0; i < 128; i++ {
				le.PutUint32(fat(b)[4*i:], uint32(i))
			}
		}),
		"every chain leads to the first sector": change(func(b []byte) {
			for i := 0; i < 128; i++ {
				le.PutUint32(fat(b)[4*i:], 0)
			}
		}),
		"the table is all free": change(func(b []byte) {
			for i := 0; i < 128; i++ {
				le.PutUint32(fat(b)[4*i:], 0xFFFFFFFF)
			}
		}),
		"a huge short-stream table":             change(func(b []byte) { le.PutUint32(b[64:], 0x7FFFFFFF) }),
		"the short-stream table starts nowhere": change(func(b []byte) { le.PutUint32(b[60:], 0x00FFFFFF) }),
		"more table continuation than file":     change(func(b []byte) { le.PutUint32(b[72:], 0x7FFFFFFF); le.PutUint32(b[68:], 1) }),
		"no root entry":                         change(func(b []byte) { dir(b)[66] = 2 }),
		"the root's container is huge":          change(func(b []byte) { le.PutUint64(dir(b)[120:], 1<<40) }),
		"a stream larger than the file": change(func(b []byte) {
			for i := 1; i < 12; i++ {
				le.PutUint64(dir(b)[128*i+120:], 1<<33)
			}
		}),
		"a stream of negative size": change(func(b []byte) {
			for i := 1; i < 12; i++ {
				le.PutUint64(dir(b)[128*i+120:], 1<<63)
			}
		}),
		"streams start nowhere": change(func(b []byte) {
			for i := 1; i < 12; i++ {
				le.PutUint32(dir(b)[128*i+116:], 0x00FFFFFF)
			}
		}),
		"the entries point at each other": change(func(b []byte) {
			for i := 0; i < 12; i++ {
				le.PutUint32(dir(b)[128*i+68:], uint32((i+1)%12))
				le.PutUint32(dir(b)[128*i+72:], uint32((i+5)%12))
				le.PutUint32(dir(b)[128*i+76:], uint32((i+3)%12))
			}
		}),
		"the entries point at themselves": change(func(b []byte) {
			for i := 0; i < 12; i++ {
				for _, at := range []int{68, 72, 76} {
					le.PutUint32(dir(b)[128*i+at:], uint32(i))
				}
			}
		}),
		"names of impossible lengths": change(func(b []byte) {
			for i := 0; i < 12; i++ {
				le.PutUint16(dir(b)[128*i+64:], 0xFFFF)
			}
		}),
	}
	for what, data := range cases {
		outcome(t, what, data)
	}
	// Any byte of the structure changed to any value.
	rng := rand.New(rand.NewSource(7))
	small := msbintest.Compound(map[string][]byte{"EncryptionInfo": content(700, 1), "EncryptedPackage": content(9000, 2), "s/t": content(100, 3)})
	for i := 0; i < 3000; i++ {
		b := append([]byte(nil), small...)
		for k := 0; k <= rng.Intn(3); k++ {
			at := rng.Intn(512 * 4)
			if rng.Intn(2) == 0 {
				at = rng.Intn(len(b))
			}
			b[at] = byte(rng.Intn(256))
		}
		outcome(t, "a few bytes changed", b)
	}
	// And the file cut at every length.
	for n := 0; n < len(small); n += 37 {
		outcome(t, "cut short", small[:n])
	}
}

// What is kept of a file's structure is a small part of the file's size,
// whatever the header claims.
func TestMemoryIsBoundedByTheFile(t *testing.T) {
	data := msbintest.Compound(map[string][]byte{"EncryptedPackage": content(1<<20, 1)})
	// The header claims as many sectors of allocation table as the file
	// has sectors.
	binary.LittleEndian.PutUint32(data[44:], uint32(len(data)/512-1))
	allocs := testing.AllocsPerRun(5, func() {
		if _, err := cfb.Open(bytes.NewReader(data), int64(len(data))); err != nil {
			t.Fatal(err)
		}
	})
	var before, after runtimeStats
	before.read()
	for i := 0; i < 20; i++ {
		cfb.Open(bytes.NewReader(data), int64(len(data)))
	}
	after.read()
	per := (after.bytes - before.bytes) / 20
	if per > uint64(len(data))/16 || allocs > 200 {
		t.Errorf("opening a file of %d bytes allocates %d bytes in %v allocations", len(data), per, allocs)
	}
}

// reads counts what is read of a file, and how far into it.
type reads struct {
	r     io.ReaderAt
	bytes int64
	far   int64
}

func (r *reads) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.r.ReadAt(p, off)
	r.bytes += int64(n)
	r.far = max(r.far, off+int64(n))
	return n, err
}

// A file opened for its directory alone names what the file opened whole
// names, gives no stream, and is read only where its directory and the
// table's sectors for it are: of a large file, a small part. It opens
// where the rest of the file is gone.
func TestDirectoryAlone(t *testing.T) {
	builds := map[string]func(map[string][]byte) []byte{
		"sectors of 512 bytes":  msbintest.Compound,
		"sectors of 4096 bytes": msbintest.CompoundV4,
		"written by Write":      func(m map[string][]byte) []byte { return cfb.Write(tree(m)) },
	}
	for name, build := range builds {
		streams := sample()
		streams["Larger"] = content(3<<20, 11)
		data := build(streams)
		whole, err := cfb.Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		counted := &reads{r: bytes.NewReader(data)}
		f, err := cfb.OpenDirectory(counted, int64(len(data)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, want := strings.Join(f.Names(), "|"), strings.Join(whole.Names(), "|"); got != want {
			t.Errorf("%s: names %q, the file opened whole %q", name, got, want)
		}
		for path := range streams {
			parts := strings.Split(path, "/")
			if !f.Has(parts...) {
				t.Errorf("%s: %q is not found", name, path)
			}
			if _, err := f.Open(parts...); !errors.Is(err, cfb.ErrNotFound) {
				t.Errorf("%s: opening %q: %v, want a refusal", name, path, err)
			}
		}
		if counted.bytes > int64(len(data))/100 {
			t.Errorf("%s: %d bytes read of %d", name, counted.bytes, len(data))
		}
		// With everything past what was read gone, it still opens: that
		// is what is expanded of a compressed object.
		cut := append([]byte(nil), data[:counted.far]...)
		g, err := cfb.OpenDirectory(bytes.NewReader(cut), int64(len(data)))
		if err != nil {
			t.Errorf("%s: cut after the %d bytes that were read: %v", name, counted.far, err)
		} else if got, want := strings.Join(g.Names(), "|"), strings.Join(whole.Names(), "|"); got != want {
			t.Errorf("%s: cut, names %q, want %q", name, got, want)
		}
		t.Logf("%s: %d bytes read of %d, none past %d", name, counted.bytes, len(data), counted.far)
	}
}

func FuzzCFB(f *testing.F) {
	f.Add(msbintest.Compound(map[string][]byte{"EncryptionInfo": content(700, 1), "EncryptedPackage": content(9000, 2), "s/t": content(100, 3)}))
	f.Add(msbintest.Compound(map[string][]byte{"a": {}}))
	f.Add(msbintest.CompoundV4(map[string][]byte{"a/b/c/d": content(5000, 4), "x": content(10, 5)}))
	f.Add([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		outcome(t, "fuzz input", data)
	})
}

// A file with a wrong byte order mark, or whose directory's chain of
// sectors loops or leads off the file, is read as far as it is sound,
// and says what was wrong.
func TestReadsOnPastDamage(t *testing.T) {
	streams := map[string][]byte{"Big": bytes.Repeat([]byte("abcdefgh"), 1000), "Small": []byte("short")}
	good := msbintest.Compound(streams)
	le := binary.LittleEndian
	check := func(name string, data []byte, wantNote string) {
		t.Helper()
		f, err := cfb.Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			return
		}
		if !strings.Contains(f.Damaged, wantNote) {
			t.Errorf("%s: damaged says %q", name, f.Damaged)
		}
		s, err := f.Open("Big")
		if err != nil {
			t.Errorf("%s: %v", name, err)
			return
		}
		got := make([]byte, s.Size())
		if _, err := s.ReadAt(got, 0); (err != nil && err != io.EOF) || !bytes.Equal(got, streams["Big"]) {
			t.Errorf("%s: the stream is not read: %v", name, err)
		}
	}
	mark := append([]byte(nil), good...)
	mark[28], mark[29] = 0x20, 0xFF
	check("byte order mark", mark, "byte order mark 0xff20")

	// The directory's first sector is made to follow itself, and to lead
	// off the file.
	dir, fat := le.Uint32(good[48:]), le.Uint32(good[76:])
	at := (int64(fat)+1)*512 + 4*int64(dir)
	loop := append([]byte(nil), good...)
	le.PutUint32(loop[at:], dir)
	check("loop", loop, "directory")
	off := append([]byte(nil), good...)
	le.PutUint32(off[at:], 0x00FFFFFF)
	check("off the file", off, "directory")

	// A directory whose first sector is off the file has nothing sound.
	none := append([]byte(nil), good...)
	le.PutUint32(none[48:], 0x00FFFFFF)
	if _, err := cfb.Open(bytes.NewReader(none), int64(len(none))); !errors.Is(err, cfb.ErrDamaged) {
		t.Errorf("no directory: %v", err)
	}
}
