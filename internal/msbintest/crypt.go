package msbintest

import (
	"crypto/md5"
	"crypto/rc4"
	"crypto/sha1"
	"encoding/binary"
	"sort"
	"unicode/utf16"
)

// This file encrypts the files the tests write, in the three schemes of
// the binary Office formats ([MS-OFFCRYPTO] 2.3), on its own from the
// published description. The schemes are weak; they are written here
// only to test that they are read.

// Schemes of encryption.
const (
	CryptXOR = iota
	CryptRC4
	CryptAPI
)

func widePassword(password string) []byte {
	var b []byte
	for _, u := range utf16.Encode([]rune(password)) {
		b = binary.LittleEndian.AppendUint16(b, u)
	}
	return b
}

// cipherMaker gives the cipher of each block of an encryption, and the
// header a file keeps of it.
type cipherMaker struct {
	header []byte
	block  func(n uint32) *rc4.Cipher
}

// rc4Maker sets up RC4 encryption with a password: the encryption of
// Office 97, or with a key of the CryptoAPI.
func rc4Maker(scheme int, password string) cipherMaker {
	salt := []byte("0123456789abcdef")
	verifier := []byte("the verifier 16b")
	var m cipherMaker
	if scheme == CryptRC4 {
		h0 := md5.Sum(widePassword(password))
		var buf []byte
		for i := 0; i < 16; i++ {
			buf = append(append(buf, h0[:5]...), salt...)
		}
		h1 := md5.Sum(buf)
		m.block = func(n uint32) *rc4.Cipher {
			key := md5.Sum(binary.LittleEndian.AppendUint32(append([]byte(nil), h1[:5]...), n))
			c, _ := rc4.NewCipher(key[:])
			return c
		}
		hash := md5.Sum(verifier)
		enc := append(append([]byte(nil), verifier...), hash[:]...)
		m.block(0).XORKeyStream(enc, enc)
		m.header = Cat(U16(1, 1), salt, enc)
		return m
	}
	h0 := sha1.Sum(append(append([]byte(nil), salt...), widePassword(password)...))
	m.block = func(n uint32) *rc4.Cipher {
		key := sha1.Sum(binary.LittleEndian.AppendUint32(append([]byte(nil), h0[:]...), n))
		c, _ := rc4.NewCipher(key[:16])
		return c
	}
	hash := sha1.Sum(verifier)
	enc := append(append([]byte(nil), verifier...), hash[:]...)
	m.block(0).XORKeyStream(enc, enc)
	name := utf16le("Microsoft Enhanced Cryptographic Provider v1.0\x00")
	head := Cat(U32(4, 0, 0x6801, 0x8004, 128, 1, 0, 0), name)
	m.header = Cat(U16(4, 2), U32(4, uint32(len(head))), head, U32(16), salt, enc[:16], U32(20), enc[16:])
	return m
}

// stream encrypts a stream in place, each block of the given size with
// its own key.
func (m cipherMaker) stream(data []byte, size int) {
	for n := 0; n*size < len(data); n++ {
		part := data[n*size : min((n+1)*size, len(data))]
		m.block(uint32(n)).XORKeyStream(part, part)
	}
}

// XOR obfuscation: the key and the verifier a password gives, and the
// sixteen bytes data is combined with.
func xorPassword(password string, rotate uint) (key, hash uint16, array [16]byte) {
	// Of each character the low byte, or the high one where the low is
	// zero.
	var p []byte
	for _, u := range utf16.Encode([]rune(password)) {
		if b := byte(u); b != 0 {
			p = append(p, b)
		} else {
			p = append(p, byte(u>>8))
		}
	}
	rot := func(v uint16) uint16 { return v<<1 | v>>15 }
	base, end := uint16(0x8000), uint16(0xFFFF)
	for i := len(p) - 1; i >= 0; i-- {
		c := p[i] & 0x7F
		for bit := 0; bit < 8; bit++ {
			if base = rot(base); base&1 != 0 {
				base ^= 0x1020
			}
			if c&1 != 0 {
				key ^= base
			}
			c >>= 1
			if end = rot(end); end&1 != 0 {
				end ^= 0x1020
			}
		}
	}
	key ^= end
	hash = uint16(len(p)) ^ 0xCE4B
	for i, b := range p {
		n := uint((i + 1) % 15)
		hash ^= (uint16(b)<<n | uint16(b)>>(15-n)) & 0x7FFF
	}
	fill := []byte{0xBB, 0xFF, 0xFF, 0xBA, 0xFF, 0xFF, 0xB9, 0x80, 0x00, 0xBE, 0x0F, 0x00, 0xBF, 0x0F, 0x00}
	copy(array[:], append(append([]byte(nil), p...), fill...))
	for i := range array {
		b := array[i] ^ byte(key>>(8*uint(i&1)))
		array[i] = b<<rotate | b>>(8-rotate)
	}
	return key, hash, array
}

// EncryptWorkbook encrypts a workbook stream with a password: the record
// that says how goes after the first, the sheets' places move by its
// size, and the data of the records is encrypted, but for the records
// and the field the format leaves as they are.
func EncryptWorkbook(stream []byte, scheme int, password string) []byte {
	le := binary.LittleEndian
	var pass []byte
	var m cipherMaker
	var array [16]byte
	if scheme == CryptXOR {
		key, hash, a := xorPassword(password, 2)
		array = a
		pass = Rec(0x002F, U16(0, key, hash)...)
	} else {
		m = rc4Maker(scheme, password)
		pass = Rec(0x002F, Cat(U16(1), m.header)...)
	}
	first := 4 + int(le.Uint16(stream[2:]))
	out := Cat(stream[:first], pass, stream[first:])
	// The places of the sheets, in the records that list them.
	for at := 0; at+4 <= len(out); {
		typ, size := le.Uint16(out[at:]), int(le.Uint16(out[at+2:]))
		if typ == 0x0085 {
			le.PutUint32(out[at+4:], le.Uint32(out[at+4:])+uint32(len(pass)))
		}
		if typ == 0x000A {
			break
		}
		at += 4 + size
	}
	enc := append([]byte(nil), out...)
	if scheme != CryptXOR {
		m.stream(enc, 1024)
	}
	for at := 0; at+4 <= len(out); {
		typ, size := le.Uint16(out[at:]), int(le.Uint16(out[at+2:]))
		from, to := at+4, at+4+size
		at = to
		switch typ {
		case 0x0809, 0x002F, 0x0194, 0x0195, 0x00E1, 0x0196, 0x0138:
			continue
		case 0x0085:
			from += 4
		}
		if scheme != CryptXOR {
			copy(out[from:to], enc[from:to])
			continue
		}
		// The place in the key counts from the start of the record's
		// data, whatever of it is left as it is.
		for j := from; j < to; j++ {
			b := out[j] ^ array[(j+size)&15]
			out[j] = b>>3 | b<<5
		}
	}
	return out
}

// EncryptWord encrypts the streams of a Word document written with room
// at the start of its table stream (DocFile.Reserve of 256): the flags
// of the document say so, and its first bytes stay as they are.
func EncryptWord(streams map[string][]byte, scheme int, password string) {
	le := binary.LittleEndian
	w, table := streams["WordDocument"], streams["1Table"]
	flags := le.Uint16(w[10:]) | 0x0100
	if scheme == CryptXOR {
		key, hash, array := xorPassword(password, 7)
		le.PutUint16(w[10:], flags|0x8000)
		le.PutUint16(w[14:], hash)
		le.PutUint16(w[16:], key)
		mix := func(data []byte, from int) {
			for i := from; i < len(data); i++ {
				if c := data[i] ^ array[i&15]; data[i] != 0 && c != 0 {
					data[i] = c
				}
			}
		}
		mix(w, 0x44)
		mix(table, 0)
		return
	}
	m := rc4Maker(scheme, password)
	le.PutUint16(w[10:], flags)
	le.PutUint32(w[14:], uint32(len(m.header)))
	start := append([]byte(nil), w[:0x44]...)
	m.stream(w, 512)
	copy(w, start)
	m.stream(table, 512)
	copy(table, m.header)
}

// EncryptWord6 obfuscates the stream of a document of Word 6 or 95, which
// knows no other scheme: its flags say it is encrypted, the key and the
// verifier of the password stand in its first bytes, and the bytes after
// the first 0x34 are combined with the password's array.
func EncryptWord6(w []byte, password string) {
	le := binary.LittleEndian
	key, hash, array := xorPassword(password, 7)
	le.PutUint16(w[10:], le.Uint16(w[10:])|0x0100)
	le.PutUint16(w[14:], hash)
	le.PutUint16(w[16:], key)
	for i := 0x34; i < len(w); i++ {
		if c := w[i] ^ array[i&15]; w[i] != 0 && c != 0 {
			w[i] = c
		}
	}
}

// Encrypt encrypts the presentation: every object with the key of its
// number, and an object more that says how. It returns the streams, the
// stream of pictures encrypted with them.
func (f *PPTFile) Encrypt(objects map[uint32][]byte, doc uint32, pictures []byte, password string) map[string][]byte {
	le := binary.LittleEndian
	m := rc4Maker(CryptAPI, password)
	session := uint32(0)
	enc := map[uint32][]byte{}
	for id, o := range objects {
		c := append([]byte(nil), o...)
		m.block(id).XORKeyStream(c, c)
		enc[id] = c
		session = max(session, id)
	}
	session++
	enc[session] = PPTContainer(0x2F14, 1, m.header)
	f.Edit(doc, enc)
	// The edit names the object that says how the rest is encrypted.
	edit := f.stream[f.last:]
	f.stream = append(f.stream[:f.last:f.last], PPTAtom(0x0FF5, 0, Cat(edit[8:], U32(session))...)...)
	streams := f.Streams()
	le.PutUint32(streams["Current User"][12:], 0xF3D1C4DF)
	// A picture: its header, its identifier, the byte before it and its
	// bytes, each encrypted from the start of the first key's run.
	pics := append([]byte(nil), pictures...)
	for b := pics; len(b) >= 8; {
		size := int(le.Uint32(b[4:]))
		body := b[8 : 8+size]
		m.block(0).XORKeyStream(b[:8], b[:8])
		m.block(0).XORKeyStream(body[:16], body[:16])
		m.block(0).XORKeyStream(body[16:17], body[16:17])
		m.block(0).XORKeyStream(body[17:], body[17:])
		b = b[8+size:]
	}
	streams["Pictures"] = pics
	if len(f.Summary) > 0 {
		streams["EncryptedSummary"] = encryptedSummary(m, f.Summary)
	}
	return streams
}

// encryptedSummary writes the stream that holds the property sets of an
// encrypted file ([MS-OFFCRYPTO] 2.3.5.4): each set encrypted with a key
// of its own, then the list that says where each is; the list, and the
// eight bytes at the start that say where the list is, each encrypted
// with the first key.
func encryptedSummary(m cipherMaker, sets map[string][]byte) []byte {
	names := make([]string, 0, len(sets))
	for name := range sets {
		names = append(names, name)
	}
	sort.Strings(names)
	data := make([]byte, 8)
	list := U32(uint32(len(names)))
	for i, name := range names {
		c := append([]byte(nil), sets[name]...)
		block := uint16(i + 1)
		m.block(uint32(block)).XORKeyStream(c, c)
		wide := utf16le(name)
		list = Cat(list, U32(uint32(len(data)), uint32(len(c))), U16(block), []byte{byte(len(wide) / 2), 1}, U32(0), wide, U16(0))
		data = append(data, c...)
	}
	m.block(0).XORKeyStream(list, list)
	binary.LittleEndian.PutUint32(data, uint32(len(data)))
	binary.LittleEndian.PutUint32(data[4:], uint32(len(list)))
	m.block(0).XORKeyStream(data[:8], data[:8])
	return append(data, list...)
}
