package cfb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"unicode/utf16"
)

// Node is a storage or a stream of a compound file to be written. A node
// that says it is a storage, or that has Children, is a storage; any
// other is a stream holding Data.
type Node struct {
	Name     string
	Storage  bool
	CLSID    [16]byte // of a storage: the class of the object it holds
	Data     []byte
	Children []*Node
}

// Write returns a compound file whose root storage holds the children of
// root, with root's CLSID. It writes the format's version 3, with sectors
// of 512 bytes, as the Office programs do. Names longer than the format's
// 31 UTF-16 units are cut there.
//
// It is how a document embedded as a storage of another file (an object in
// a Word file's ObjectPool) is made a file of its own.
func Write(root *Node) []byte {
	const (
		sector    = 512
		perSector = sector / 4
		short     = 64
		cutoff    = 4096
		fatSector = 0xFFFFFFFD
		difSector = 0xFFFFFFFC
	)
	type item struct {
		node               *Node
		units              []uint16
		kind               byte
		left, right, child uint32
		start              uint32
		size               int
	}
	// The directory: the root, then every node, breadth first. The
	// children of a storage are a search tree ordered as the format
	// orders names: shorter first, then by their upper-case code units.
	items := []*item{{node: root, kind: kindRoot, left: noStream, right: noStream, child: noStream}}
	for at := 0; at < len(items); at++ {
		parent := items[at]
		if parent.kind == kindStream {
			continue
		}
		kids := make([]*item, 0, len(parent.node.Children))
		for _, c := range parent.node.Children {
			if c == nil {
				continue
			}
			units := utf16.Encode([]rune(c.Name))
			if len(units) > 31 {
				units = units[:31]
			}
			kind := byte(kindStream)
			if c.Storage || len(c.Children) > 0 {
				kind = kindStorage
			}
			kids = append(kids, &item{node: c, units: units, kind: kind, left: noStream, right: noStream, child: noStream})
		}
		sort.SliceStable(kids, func(i, j int) bool { return lessName(kids[i].units, kids[j].units) })
		first := len(items)
		items = append(items, kids...)
		// A balanced tree over the sorted children.
		var build func(lo, hi int) uint32
		build = func(lo, hi int) uint32 {
			if lo >= hi {
				return noStream
			}
			mid := (lo + hi) / 2
			kids[mid].left, kids[mid].right = build(lo, mid), build(mid+1, hi)
			return uint32(first + mid)
		}
		parent.child = build(0, len(kids))
	}

	// Short streams go into one container, a short sector at a time.
	var container []byte
	var miniFAT []uint32
	for _, it := range items {
		if it.kind != kindStream {
			continue
		}
		it.size = len(it.node.Data)
		it.start = endOfChain
		if it.size == 0 || it.size >= cutoff {
			continue
		}
		it.start = uint32(len(miniFAT))
		count := (it.size + short - 1) / short
		for i := 0; i < count; i++ {
			next := uint32(len(miniFAT) + 1)
			if i == count-1 {
				next = endOfChain
			}
			miniFAT = append(miniFAT, next)
		}
		container = append(container, it.node.Data...)
		container = append(container, make([]byte, count*short-it.size)...)
	}

	// The sectors, in order: the allocation table's own, the list of
	// those beyond the 109 the header holds, the directory, the short
	// table, the container, then the long streams.
	sectorsOf := func(n int) int { return (n + sector - 1) / sector }
	dirSectors := sectorsOf(len(items) * 128)
	miniFATSectors := sectorsOf(len(miniFAT) * 4)
	data := dirSectors + miniFATSectors + sectorsOf(len(container))
	for _, it := range items {
		if it.kind == kindStream && it.size >= cutoff {
			data += sectorsOf(it.size)
		}
	}
	fatSectors, difSectors := 0, 0
	for {
		need := (data + fatSectors + difSectors + perSector - 1) / perSector
		dif := 0
		if need > 109 {
			dif = (need - 109 + perSector - 2) / (perSector - 1)
		}
		if need == fatSectors && dif == difSectors {
			break
		}
		fatSectors, difSectors = need, dif
	}
	fat := make([]uint32, fatSectors*perSector)
	for i := range fat {
		fat[i] = freeSector
	}
	next := 0
	take := func(n int, mark uint32) uint32 {
		if n == 0 {
			return endOfChain
		}
		first := uint32(next)
		for i := 0; i < n; i++ {
			switch {
			case mark != 0:
				fat[next] = mark
			case i == n-1:
				fat[next] = endOfChain
			default:
				fat[next] = uint32(next + 1)
			}
			next++
		}
		return first
	}
	firstFAT := take(fatSectors, fatSector)
	firstDIF := take(difSectors, difSector)
	firstDir := take(dirSectors, 0)
	firstMiniFAT := take(miniFATSectors, 0)
	items[0].start, items[0].size = take(sectorsOf(len(container)), 0), len(container)
	for _, it := range items {
		if it.kind == kindStream && it.size >= cutoff {
			it.start = take(sectorsOf(it.size), 0)
		}
	}

	le := binary.LittleEndian
	out := make([]byte, sector*(1+next))
	at := func(n uint32) []byte { return out[sector*(1+int(n)):] }
	head := out[:sector]
	copy(head, signature)
	le.PutUint16(head[24:], 0x3E)
	le.PutUint16(head[26:], 3)
	le.PutUint16(head[28:], 0xFFFE)
	le.PutUint16(head[30:], 9)
	le.PutUint16(head[32:], 6)
	le.PutUint32(head[44:], uint32(fatSectors))
	le.PutUint32(head[48:], firstDir)
	le.PutUint32(head[56:], cutoff)
	le.PutUint32(head[60:], firstMiniFAT)
	le.PutUint32(head[64:], uint32(miniFATSectors))
	le.PutUint32(head[68:], firstDIF)
	le.PutUint32(head[72:], uint32(difSectors))
	for i := 0; i < 109; i++ {
		v := uint32(freeSector)
		if i < fatSectors {
			v = firstFAT + uint32(i)
		}
		le.PutUint32(head[76+4*i:], v)
	}
	// The table sectors past the first 109 are listed in sectors that
	// each end with the number of the next.
	for i := 0; i < difSectors; i++ {
		sec := at(firstDIF + uint32(i))
		for k := 0; k < perSector-1; k++ {
			v, which := uint32(freeSector), 109+i*(perSector-1)+k
			if which < fatSectors {
				v = firstFAT + uint32(which)
			}
			le.PutUint32(sec[4*k:], v)
		}
		link := uint32(endOfChain)
		if i < difSectors-1 {
			link = firstDIF + uint32(i) + 1
		}
		le.PutUint32(sec[sector-4:], link)
	}
	for i, v := range fat {
		le.PutUint32(at(firstFAT)[4*i:], v)
	}
	if miniFATSectors > 0 {
		table := at(firstMiniFAT)
		for i := 0; i < miniFATSectors*perSector; i++ {
			v := uint32(freeSector)
			if i < len(miniFAT) {
				v = miniFAT[i]
			}
			le.PutUint32(table[4*i:], v)
		}
	}
	if len(container) > 0 {
		copy(at(items[0].start), container)
	}
	dir := at(firstDir)
	for i, it := range items {
		e := dir[128*i : 128*i+128]
		units := it.units
		if i == 0 {
			units = utf16.Encode([]rune("Root Entry"))
		}
		for k, u := range units {
			le.PutUint16(e[2*k:], u)
		}
		le.PutUint16(e[64:], uint16(2*len(units)+2))
		e[66], e[67] = it.kind, 1 // every node black: a valid colouring is not needed to read it
		le.PutUint32(e[68:], it.left)
		le.PutUint32(e[72:], it.right)
		le.PutUint32(e[76:], it.child)
		if it.kind != kindStream {
			copy(e[80:96], it.node.CLSID[:])
		}
		le.PutUint32(e[116:], it.start)
		le.PutUint64(e[120:], uint64(it.size))
		if it.kind == kindStream && it.size >= cutoff {
			copy(at(it.start), it.node.Data)
		}
	}
	for i := len(items); i < dirSectors*(sector/128); i++ {
		e := dir[128*i : 128*i+128]
		le.PutUint32(e[68:], noStream)
		le.PutUint32(e[72:], noStream)
		le.PutUint32(e[76:], noStream)
	}
	return out
}

// lessName orders two names as the directory of a compound file does:
// the shorter first, and names of one length by their code units in upper
// case.
func lessName(a, b []uint16) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	for i := range a {
		x, y := upperUnit(a[i]), upperUnit(b[i])
		if x != y {
			return x < y
		}
	}
	return false
}

// upperUnit is the simple upper-case form of a UTF-16 code unit, for the
// letters whose upper case is 32 below them (ASCII and Latin-1). The
// order of other letters only has to be the same for every name written.
func upperUnit(u uint16) uint16 {
	if u >= 'a' && u <= 'z' || u >= 0xE0 && u <= 0xFE && u != 0xF7 {
		return u - 32
	}
	return u
}

// CLSID returns the class identifier of the storage at the given path
// from the root, or of the root if the path is empty: sixteen bytes that
// say what kind of object the storage holds. It is all zeros where the
// file states none.
func (f *File) CLSID(path ...string) [16]byte {
	at := 0
	if len(path) > 0 {
		i, ok := f.lookup(path)
		if !ok {
			return [16]byte{}
		}
		at = i
	}
	return f.entries[at].clsid
}

// IsStorage reports whether the path names a storage.
func (f *File) IsStorage(path ...string) bool {
	i, ok := f.lookup(path)
	return ok && f.entries[i].kind == kindStorage
}

// ErrDeep is wrapped, beside ErrLimit, by the error of Export for a
// storage whose storages nest too deep.
var ErrDeep = errors.New("storages nested more than 64 deep")

// maxExportDepth bounds how deeply the storages of an exported storage
// nest. Office writes two or three levels.
const maxExportDepth = 64

// Export returns the storage at the given path as a compound file of its
// own: its streams and storages, with their content, under a new root
// that has the storage's class identifier. limit bounds the bytes of
// stream content read; a storage that holds more is refused with
// ErrLimit, as is one whose storages nest more than 64 deep. The second
// result is the number of content bytes read.
func (f *File) Export(limit int64, path ...string) ([]byte, int64, error) {
	i, ok := f.lookup(path)
	if !ok || f.entries[i].kind != kindStorage {
		return nil, 0, fmt.Errorf("%w: storage %s", ErrNotFound, joinPath(path))
	}
	f.indexOnce.Do(f.buildIndex)
	var used int64
	var build func(at, depth int) (*Node, error)
	build = func(at, depth int) (*Node, error) {
		e := &f.entries[at]
		n := &Node{Name: e.name, Storage: true, CLSID: e.clsid}
		if depth > maxExportDepth {
			return nil, fmt.Errorf("%w: %w", ErrLimit, ErrDeep)
		}
		// The index gives each entry to one storage only, so this walk
		// visits every entry at most once whatever the file says.
		kids := make([]int, 0, len(f.index[at]))
		for _, k := range f.index[at] {
			kids = append(kids, k)
		}
		sort.Ints(kids)
		for _, k := range kids {
			c := &f.entries[k]
			if c.kind == kindStorage {
				sub, err := build(k, depth+1)
				if err != nil {
					return nil, err
				}
				n.Children = append(n.Children, sub)
				continue
			}
			child := &Node{Name: c.name}
			if c.size > 0 {
				if c.size > f.size {
					return nil, fmt.Errorf("%w: a stream of %d bytes in a file of %d", ErrDamaged, c.size, f.size)
				}
				if limit >= 0 && used+c.size > limit {
					return nil, fmt.Errorf("%w: the storage holds more than %d bytes", ErrLimit, limit)
				}
				s, err := f.stream(c.start, c.size, c.size < f.miniCutoff)
				if err != nil {
					return nil, err
				}
				child.Data = make([]byte, s.Size())
				if _, err := s.ReadAt(child.Data, 0); err != nil && err != io.EOF {
					return nil, err
				}
				used += s.Size()
			}
			n.Children = append(n.Children, child)
		}
		return n, nil
	}
	root, err := build(i, 0)
	if err != nil {
		return nil, used, err
	}
	return Write(root), used, nil
}

func joinPath(path []string) string {
	out := ""
	for i, p := range path {
		if i > 0 {
			out += "/"
		}
		out += p
	}
	return out
}
