// Package crypt opens the encryption of the binary Office formats
// ([MS-OFFCRYPTO] 2.3): XOR obfuscation, the RC4 encryption of Office 97,
// and RC4 with a key made by the CryptoAPI. A password is verified
// against what the file keeps for that before anything is decrypted
// with it.
//
// These schemes are weak, and are read here because documents kept in
// them still exist; nothing is written with them.
package crypt

import (
	"crypto/md5"
	"crypto/rc4"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"unicode/utf16"
)

var le = binary.LittleEndian

// The schemes.
const (
	XOR       = "XOR obfuscation"
	RC4       = "RC4"
	CryptoAPI = "RC4 CryptoAPI"
)

// DefaultPassword is the password Excel encrypts a workbook with when
// its structure is protected and no password to open it was set
// ([MS-XLS] 2.2.10).
const DefaultPassword = "VelvetSweatshop"

// maxPassword bounds the password, as the formats do.
const maxPassword = 255

// ErrHeader is returned for a header of encryption that cannot be read,
// or that names an algorithm other than RC4.
var ErrHeader = errors.New("crypt: the encryption header cannot be read")

// Header is what a file keeps of its encryption: the scheme, and what a
// password is verified against.
type Header struct {
	Scheme string
	// XOR obfuscation: the key and the verifier made of the password.
	Key, Hash uint16
	// RC4: the salt, and the verifier and its hash, both encrypted.
	Salt, Verifier, VerifierHash []byte
	// KeyBits is the length of a CryptoAPI key.
	KeyBits int
}

// Parse reads the header of an RC4 encryption, which begins with its
// version: 1.1 for the encryption of Office 97, and 2.2 to 4.2 for the
// CryptoAPI ([MS-OFFCRYPTO] 2.3.6.1, 2.3.5.1).
func Parse(b []byte) (Header, error) {
	if len(b) < 4 {
		return Header{}, ErrHeader
	}
	major, minor := le.Uint16(b), le.Uint16(b[2:])
	switch {
	case major == 1 && minor == 1:
		if len(b) < 52 {
			return Header{}, ErrHeader
		}
		return Header{Scheme: RC4, Salt: b[4:20], Verifier: b[20:36], VerifierHash: b[36:52]}, nil
	case major >= 2 && major <= 4 && minor == 2:
		// Flags, the size of the header, the header (flags, the size of
		// what is extra, the algorithm, the hash, the size of the key,
		// the provider and its name), then the verifier.
		if len(b) < 12 {
			return Header{}, ErrHeader
		}
		size := int(le.Uint32(b[8:]))
		if size < 32 || size > len(b)-12 {
			return Header{}, ErrHeader
		}
		head, rest := b[12:12+size], b[12+size:]
		const algRC4, algSHA1 = 0x6801, 0x8004
		if alg := le.Uint32(head[8:]); alg != algRC4 {
			return Header{}, ErrHeader
		}
		if hash := le.Uint32(head[12:]); hash != algSHA1 && hash != 0 {
			return Header{}, ErrHeader
		}
		bits := int(le.Uint32(head[16:]))
		if bits == 0 {
			bits = 40
		}
		if bits < 40 || bits > 128 || bits%8 != 0 {
			return Header{}, ErrHeader
		}
		if len(rest) < 4+16+16+4+20 || le.Uint32(rest) != 16 || le.Uint32(rest[36:]) != 20 {
			return Header{}, ErrHeader
		}
		return Header{Scheme: CryptoAPI, KeyBits: bits, Salt: rest[4:20], Verifier: rest[20:36], VerifierHash: rest[40:60]}, nil
	}
	return Header{}, ErrHeader
}

// Key decrypts what a file encrypted.
type Key struct {
	scheme string
	base   []byte // what the key of each block is made from
	bytes  int    // the length of a CryptoAPI key
	xor    [16]byte
}

// Scheme names the scheme of the key.
func (k *Key) Scheme() string { return k.scheme }

// wide writes a password in UTF-16.
func wide(password string) []byte {
	u := utf16.Encode([]rune(password))
	if len(u) > maxPassword {
		u = u[:maxPassword]
	}
	b := make([]byte, 2*len(u))
	for i, v := range u {
		le.PutUint16(b[2*i:], v)
	}
	return b
}

// Open makes the key the password gives, and reports whether it is the
// file's: whether the verifier the file keeps decrypts to what its hash
// says. For XOR obfuscation rotate is how far the bytes of the key are
// turned: 2 in a workbook, 7 in a Word document.
func (h *Header) Open(password string, rotate uint) (*Key, bool) {
	k := &Key{scheme: h.Scheme}
	switch h.Scheme {
	case XOR:
		data := narrow(password)
		key, hash := xorKey(data), xorHash(data)
		if key != h.Key || hash != h.Hash {
			return nil, false
		}
		k.xor = xorArray(data, key, rotate)
		return k, true
	case RC4:
		// The hash of the password, cut to five bytes and joined with
		// the salt sixteen times over, hashed again.
		h0 := md5.Sum(wide(password))
		buf := make([]byte, 0, 16*21)
		for i := 0; i < 16; i++ {
			buf = append(append(buf, h0[:5]...), h.Salt...)
		}
		h1 := md5.Sum(buf)
		k.base = h1[:5]
		var plain [32]byte
		c := k.cipher(0)
		c.XORKeyStream(plain[:16], h.Verifier)
		c.XORKeyStream(plain[16:], h.VerifierHash)
		sum := md5.Sum(plain[:16])
		return k, subtle.ConstantTimeCompare(sum[:], plain[16:]) == 1
	case CryptoAPI:
		sum := sha1.Sum(append(append([]byte(nil), h.Salt...), wide(password)...))
		k.base, k.bytes = sum[:], h.KeyBits/8
		var plain [36]byte
		c := k.cipher(0)
		c.XORKeyStream(plain[:16], h.Verifier)
		c.XORKeyStream(plain[16:], h.VerifierHash)
		check := sha1.Sum(plain[:16])
		return k, subtle.ConstantTimeCompare(check[:], plain[16:]) == 1
	}
	return nil, false
}

// cipher returns the cipher of a block: the key of each block is made
// from the password's and the number of the block.
func (k *Key) cipher(block uint32) *rc4.Cipher {
	var n [4]byte
	le.PutUint32(n[:], block)
	var key []byte
	if k.scheme == RC4 {
		sum := md5.Sum(append(append([]byte(nil), k.base...), n[:]...))
		key = sum[:]
	} else {
		sum := sha1.Sum(append(append([]byte(nil), k.base...), n[:]...))
		key = sum[:k.bytes]
		if k.bytes == 5 {
			// A key of 40 bits is used as one of 128, the rest zero.
			key = append(key, make([]byte, 11)...)
		}
	}
	c, err := rc4.NewCipher(key)
	if err != nil {
		panic(err) // the key is of a length RC4 takes
	}
	return c
}

// Stream decrypts a stream in place whose blocks of the given size are
// each encrypted with the key of their number, from the start of the
// stream: 512 bytes in a Word document, 1024 in a workbook.
func (k *Key) Stream(data []byte, blockSize int) {
	for n := 0; n*blockSize < len(data); n++ {
		part := data[n*blockSize : min((n+1)*blockSize, len(data))]
		k.cipher(uint32(n)).XORKeyStream(part, part)
	}
}

// Block decrypts in place data that is encrypted on its own with the key
// of one block, as an object of a presentation is with the key of its
// number. It returns the cipher, for what follows the data in the same
// run.
func (k *Key) Block(data []byte, block uint32) *rc4.Cipher {
	c := k.cipher(block)
	c.XORKeyStream(data, data)
	return c
}

// XorWorkbook decrypts in place the data of a record of a workbook that
// is obfuscated ([MS-XLS] 2.2.10): the place in the key is that of the
// data in the stream, plus the size of the record.
func (k *Key) XorWorkbook(body []byte, at int) {
	i := (at + len(body)) & 15
	for j, b := range body {
		body[j] = (b<<3 | b>>5) ^ k.xor[i]
		i = (i + 1) & 15
	}
}

// XorDocument decrypts in place a part of a stream of a Word document
// that is obfuscated, which begins at the given place of the stream. A
// byte that is zero, or that would become zero, is as it is.
func (k *Key) XorDocument(data []byte, at int) {
	i := at & 15
	for j, b := range data {
		if c := b ^ k.xor[i]; b != 0 && c != 0 {
			data[j] = c
		}
		i = (i + 1) & 15
	}
}

// narrow gives the bytes of a password for XOR obfuscation: of each
// character the low byte, or the high one where the low is zero; fifteen
// at most.
func narrow(password string) []byte {
	var out []byte
	for _, u := range utf16.Encode([]rune(password)) {
		if len(out) == 15 {
			break
		}
		if b := byte(u); b != 0 {
			out = append(out, b)
		} else {
			out = append(out, byte(u>>8))
		}
	}
	return out
}

func rotl16(v uint16, n uint) uint16 { return v<<n | v>>(16-n) }

// xorKey is the key a password gives ([MS-OFFCRYPTO] 2.3.7.2).
func xorKey(password []byte) uint16 {
	if len(password) == 0 {
		return 0
	}
	key, base, end := uint16(0), uint16(0x8000), uint16(0xFFFF)
	for i := len(password) - 1; i >= 0; i-- {
		c := password[i] & 0x7F
		for bit := 0; bit < 8; bit++ {
			if base = rotl16(base, 1); base&1 != 0 {
				base ^= 0x1020
			}
			if c&1 != 0 {
				key ^= base
			}
			c >>= 1
			if end = rotl16(end, 1); end&1 != 0 {
				end ^= 0x1020
			}
		}
	}
	return key ^ end
}

// xorHash is the verifier a password gives ([MS-OFFCRYPTO] 2.3.7.1).
func xorHash(password []byte) uint16 {
	hash := uint16(len(password))
	if len(password) > 0 {
		hash ^= 0xCE4B
	}
	for i, b := range password {
		// The character, turned within fifteen bits.
		c, n := uint16(b), uint((i+1)%15)
		c = (c<<n | c>>(15-n)) & 0x7FFF
		hash ^= c
	}
	return hash
}

// xorArray is the sixteen bytes data is obfuscated with: the password,
// filled up with the bytes the format gives, each combined with a byte
// of the key and turned.
func xorArray(password []byte, key uint16, rotate uint) [16]byte {
	fill := [15]byte{0xBB, 0xFF, 0xFF, 0xBA, 0xFF, 0xFF, 0xB9, 0x80, 0x00, 0xBE, 0x0F, 0x00, 0xBF, 0x0F, 0x00}
	var out [16]byte
	copy(out[:], password)
	copy(out[len(password):], fill[:])
	pair := [2]byte{byte(key), byte(key >> 8)}
	for i := range out {
		b := out[i] ^ pair[i&1]
		out[i] = b<<rotate | b>>(8-rotate)
	}
	return out
}
