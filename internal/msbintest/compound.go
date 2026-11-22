// Package msbintest is the test harness of go-fast-msbin: it builds the
// files the library reads, so that each behaviour has a small file with
// exactly known content.
package msbintest

import (
	"encoding/binary"
	"sort"
	"strings"
	"unicode/utf16"
)

// This file writes compound files. It is copied from go-fast-ooxml
// (internal/ooxmltest), where it was written from the published
// description of the format (MS-CFB) on its own, apart from the reader.
// The library has a writer too (internal/cfb, Write); this one is kept
// apart from it, so that the reader is tested against two writers.

// Compound returns a compound file holding the given streams. A name
// with slashes puts the stream in storages: "a/b" is stream b in storage
// a. Streams under 4096 bytes are kept as short streams, as the format
// has it.
func Compound(streams map[string][]byte) []byte { return compound(streams, 9) }

// CompoundV4 is Compound in the format's later version, with sectors of
// 4096 bytes.
func CompoundV4(streams map[string][]byte) []byte { return compound(streams, 12) }

func compound(streams map[string][]byte, shift int) []byte {
	sector := 1 << shift
	perSector := sector / 4
	const (
		short      = 64
		cutoff     = 4096
		free       = 0xFFFFFFFF
		endOfChain = 0xFFFFFFFE
		fatSector  = 0xFFFFFFFD
		difSector  = 0xFFFFFFFC
		none       = 0xFFFFFFFF
	)
	// The directory: the root, then every storage and stream, each
	// storage's children in name order, linked from one to the next.
	type node struct {
		name     string
		kind     byte
		data     []byte
		children map[string]*node
		index    int
		start    uint32
	}
	root := &node{name: "Root Entry", kind: 5, children: map[string]*node{}}
	for path, data := range streams {
		at := root
		parts := strings.Split(path, "/")
		for i, name := range parts {
			child := at.children[name]
			if child == nil {
				child = &node{name: name, kind: 1, children: map[string]*node{}}
				at.children[name] = child
			}
			if i == len(parts)-1 {
				child.kind, child.data = 2, data
			}
			at = child
		}
	}
	var entries []*node
	var number func(n *node)
	number = func(n *node) {
		n.index = len(entries)
		entries = append(entries, n)
		names := make([]string, 0, len(n.children))
		for name := range n.children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			number(n.children[name])
		}
	}
	number(root)
	sorted := func(n *node) []*node {
		out := make([]*node, 0, len(n.children))
		for _, c := range n.children {
			out = append(out, c)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].index < out[j].index })
		return out
	}

	// Short streams go into one container, a short sector at a time.
	var container []byte
	var miniFAT []uint32
	for _, n := range entries {
		if n.kind != 2 || len(n.data) == 0 || len(n.data) >= cutoff {
			continue
		}
		n.start = uint32(len(miniFAT))
		count := (len(n.data) + short - 1) / short
		for i := 0; i < count; i++ {
			next := uint32(len(miniFAT) + 1)
			if i == count-1 {
				next = endOfChain
			}
			miniFAT = append(miniFAT, next)
		}
		container = append(container, n.data...)
		container = append(container, make([]byte, count*short-len(n.data))...)
	}

	// The sectors, in order: the allocation table's own, the list of
	// those beyond the 109 the header holds, the directory, the short
	// table, the container, then the long streams.
	sectorsOf := func(n int) int { return (n + sector - 1) / sector }
	dirSectors := sectorsOf(len(entries) * 128)
	miniFATSectors := sectorsOf(len(miniFAT) * 4)
	data := dirSectors + miniFATSectors + sectorsOf(len(container))
	for _, n := range entries {
		if n.kind == 2 && len(n.data) >= cutoff {
			data += sectorsOf(len(n.data))
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
		fat[i] = free
	}
	next := 0
	take := func(n int, mark uint32) uint32 {
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
		if n == 0 {
			return endOfChain
		}
		return first
	}
	firstFAT := take(fatSectors, fatSector)
	firstDIF := take(difSectors, difSector)
	firstDir := take(dirSectors, 0)
	firstMiniFAT := take(miniFATSectors, 0)
	root.start = take(sectorsOf(len(container)), 0)
	root.data = container
	for _, n := range entries {
		if n.kind == 2 && len(n.data) >= cutoff {
			n.start = take(sectorsOf(len(n.data)), 0)
		}
	}

	le := binary.LittleEndian
	out := make([]byte, sector*(1+next))
	at := func(n uint32) []byte { return out[sector*(1+int(n)):] }
	head := out[:512]
	copy(head, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
	le.PutUint16(head[24:], 0x3E)
	le.PutUint16(head[26:], 3)
	if shift == 12 {
		le.PutUint16(head[26:], 4)
		le.PutUint32(head[40:], uint32(dirSectors)) // this version states it
	}
	le.PutUint16(head[28:], 0xFFFE)
	le.PutUint16(head[30:], uint16(shift))
	le.PutUint16(head[32:], 6)
	le.PutUint32(head[44:], uint32(fatSectors))
	le.PutUint32(head[48:], firstDir)
	le.PutUint32(head[56:], cutoff)
	le.PutUint32(head[60:], firstMiniFAT)
	le.PutUint32(head[64:], uint32(miniFATSectors))
	le.PutUint32(head[68:], firstDIF)
	le.PutUint32(head[72:], uint32(difSectors))
	for i := 0; i < 109; i++ {
		v := uint32(free)
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
			v, which := uint32(free), 109+i*(perSector-1)+k
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
	for i, v := range miniFAT {
		le.PutUint32(at(firstMiniFAT)[4*i:], v)
	}
	for i := len(miniFAT); i < miniFATSectors*perSector; i++ {
		le.PutUint32(at(firstMiniFAT)[4*i:], free)
	}
	if len(container) > 0 {
		copy(at(root.start), container)
	}
	for _, n := range entries {
		if n.kind == 2 && len(n.data) >= cutoff {
			copy(at(n.start), n.data)
		}
	}
	dir := at(firstDir)
	for i, n := range entries {
		e := dir[128*i : 128*i+128]
		units := utf16.Encode([]rune(n.name))
		for k, u := range units {
			le.PutUint16(e[2*k:], u)
		}
		le.PutUint16(e[64:], uint16(2*len(units)+2))
		e[66], e[67] = n.kind, 1
		child := uint32(none)
		if kids := sorted(n); len(kids) > 0 {
			child = uint32(kids[0].index)
		}
		le.PutUint32(e[68:], none) // no left sibling: see below
		le.PutUint32(e[76:], child)
		le.PutUint32(e[116:], n.start)
		le.PutUint64(e[120:], uint64(len(n.data)))
	}
	// Each storage's children are a chain: every child's right sibling
	// is the next in name order. That is a search tree, if a lopsided
	// one.
	for _, n := range entries {
		kids := sorted(n)
		for k, c := range kids {
			right := uint32(none)
			if k+1 < len(kids) {
				right = uint32(kids[k+1].index)
			}
			le.PutUint32(dir[128*c.index+72:], right)
		}
	}
	le.PutUint32(dir[72:], none) // the root has no sibling
	for i := len(entries); i < dirSectors*(sector/128); i++ {
		e := dir[128*i : 128*i+128]
		le.PutUint32(e[68:], none)
		le.PutUint32(e[72:], none)
		le.PutUint32(e[76:], none)
	}
	return out
}

// Encryption says how Encrypt encrypts a package.
