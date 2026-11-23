package gofastmsbin

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/crypt"
	"github.com/AOShei/go-fast-msbin/internal/detect"
	"github.com/AOShei/go-fast-msbin/internal/ppt"
)

// The records of a workbook that are not encrypted in an encrypted one
// ([MS-XLS] 2.2.10), and the one whose first field is not.
const (
	recBOF         = 0x0809
	recFilePass    = 0x002F
	recUsrExcl     = 0x0194
	recFileLock    = 0x0195
	recInterface   = 0x00E1
	recRRDInfo     = 0x0196
	recRRDHead     = 0x0138
	recBoundSheet  = 0x0085
	workbookBlock  = 1024 // the bytes of a workbook one key encrypts
	documentBlock  = 512  // and of a Word document
	word97Plain    = 0x44 // the start of a Word document that is never encrypted
	word6Plain     = 0x34
	maxFilePassGap = 64 // records looked through for the one that says how a workbook is encrypted
)

// errNoPassword is why a key could not be made: no password opened the
// file.
var errNoPassword = errors.New("no password opens the file")

// key makes the key of an encrypted file: the password the caller gave
// is tried, and for a workbook the one Excel uses of its own accord.
// with says which opened it, for the warning.
func (f *File) key(h crypt.Header, rotate uint) (k *crypt.Key, with string, err error) {
	if f.opts.Password != "" {
		if k, ok := h.Open(f.opts.Password, rotate); ok {
			return k, "the password given", nil
		}
	}
	if f.info.Format == detect.Xls {
		if k, ok := h.Open(crypt.DefaultPassword, rotate); ok {
			return k, "the password Excel uses when none is set", nil
		}
	}
	return nil, "", errNoPassword
}

// refused is the error for an encrypted file no password opened: a
// password is required, or the one given is wrong.
func (f *File) refused(scheme string) error {
	what := f.info.Version + " file, encrypted (" + scheme + ")"
	if f.opts.Password == "" {
		return fmt.Errorf("gofastmsbin: %s: no password given: %w", what, gofastcore.ErrPasswordRequired)
	}
	return fmt.Errorf("gofastmsbin: %s: the password given does not open it: %w", what, gofastcore.ErrWrongPassword)
}

// malformedCrypt is the error for a file that says it is encrypted and
// does not say how in a way that can be read.
func (f *File) malformedCrypt() error {
	return fmt.Errorf("gofastmsbin: %s file, encrypted: what it says of its encryption cannot be read: %w", f.info.Version, gofastcore.ErrUnsupported)
}

// decrypted notes that the file was encrypted and has been decrypted: in
// its properties, which say what the file was whoever's password opened
// it, and with a warning that names the scheme and the password.
func (f *File) decrypted(scheme, with string) {
	f.meta.Encrypted = true
	f.opened = append(f.opened, gofastcore.Warning{Code: WarnDecrypted, Severity: gofastcore.SeverityInfo,
		Message: fmt.Sprintf("encrypted (%s); decrypted with %s", scheme, with)})
}

// decryptBook decrypts a workbook's stream in place. The headers of the
// records are never encrypted, nor are a few records and the first field
// of the records that list the sheets; with RC4 the key changes every
// 1024 bytes of the stream, counted over all of it.
func (f *File) decryptBook() error {
	le := binary.LittleEndian
	b := f.book
	// The record that says how: it follows the first closely.
	var h crypt.Header
	found := false
	for at, n := 0, 0; at+4 <= len(b) && n < maxFilePassGap && !found; n++ {
		typ, size := le.Uint16(b[at:]), int(le.Uint16(b[at+2:]))
		body := b[at+4 : min(at+4+size, len(b))]
		if typ == recFilePass {
			found = true
			switch {
			case f.info.Version != detect.BIFF8 && len(body) >= 4:
				h = crypt.Header{Scheme: crypt.XOR, Key: le.Uint16(body), Hash: le.Uint16(body[2:])}
			case len(body) >= 6 && le.Uint16(body) == 0:
				h = crypt.Header{Scheme: crypt.XOR, Key: le.Uint16(body[2:]), Hash: le.Uint16(body[4:])}
			case len(body) >= 6 && le.Uint16(body) == 1:
				var err error
				if h, err = crypt.Parse(body[2:]); err != nil {
					return f.malformedCrypt()
				}
			default:
				return f.malformedCrypt()
			}
		}
		at += 4 + size
	}
	if !found {
		return f.malformedCrypt()
	}
	key, with, err := f.key(h, 2)
	if err != nil {
		return f.refused(h.Scheme)
	}
	var plain []byte
	if h.Scheme != crypt.XOR {
		plain = append([]byte(nil), b...)
		key.Stream(plain, workbookBlock)
	}
	for at := 0; at+4 <= len(b); {
		typ, size := le.Uint16(b[at:]), int(le.Uint16(b[at+2:]))
		from, to := at+4, min(at+4+size, len(b))
		at = to
		switch typ {
		case recBOF, recFilePass, recUsrExcl, recFileLock, recInterface, recRRDInfo, recRRDHead:
			continue
		case recBoundSheet:
			if to-from < 4 {
				continue
			}
			if plain == nil {
				var keep [4]byte
				copy(keep[:], b[from:])
				key.XorWorkbook(b[from:to], from)
				copy(b[from:], keep[:])
				continue
			}
			from += 4
		}
		if plain == nil {
			key.XorWorkbook(b[from:to], from)
		} else {
			copy(b[from:to], plain[from:to])
		}
	}
	f.decrypted(h.Scheme, with)
	return nil
}

// decryptWord decrypts the streams of a Word document in place: the
// document's own past its first bytes, the table stream, and the data
// stream.
func (f *File) decryptWord(table, data []byte) error {
	le := binary.LittleEndian
	w := f.word
	if len(w) < 32 {
		return f.malformedCrypt()
	}
	plain := word97Plain
	if f.info.Version != detect.Word97 {
		plain = word6Plain
	}
	plain = min(plain, len(w))
	if f.info.Encryption == crypt.XOR {
		// The key and the verifier, in one order or the other.
		a, b := le.Uint16(w[14:]), le.Uint16(w[16:])
		var key *crypt.Key
		var with string
		err := errNoPassword
		for _, h := range []crypt.Header{{Scheme: crypt.XOR, Key: b, Hash: a}, {Scheme: crypt.XOR, Key: a, Hash: b}} {
			if key, with, err = f.key(h, 7); err == nil {
				break
			}
		}
		if err != nil {
			return f.refused(crypt.XOR)
		}
		key.XorDocument(w[plain:], plain)
		key.XorDocument(table, 0)
		key.XorDocument(data, 0)
		f.decrypted(crypt.XOR, with)
		return nil
	}
	h, err := crypt.Parse(table)
	if err != nil {
		return f.malformedCrypt()
	}
	key, with, err := f.key(h, 0)
	if err != nil {
		return f.refused(h.Scheme)
	}
	start := append([]byte(nil), w[:plain]...)
	key.Stream(w, documentBlock)
	copy(w, start)
	key.Stream(table, documentBlock)
	key.Stream(data, documentBlock)
	f.decrypted(h.Scheme, with)
	return nil
}

// decryptDeck decrypts a presentation's objects and pictures in place.
func (f *File) decryptDeck() error {
	scheme, with := crypt.CryptoAPI, ""
	err := ppt.Decrypt(f.deck, f.pictures, f.currentEdit, func(header []byte) (*crypt.Key, error) {
		h, err := crypt.Parse(header)
		if err != nil {
			return nil, err
		}
		scheme = h.Scheme
		key, w, err := f.key(h, 0)
		with = w
		return key, err
	})
	switch {
	case errors.Is(err, errNoPassword):
		return f.refused(scheme)
	case err != nil:
		return f.malformedCrypt()
	}
	f.decrypted(scheme, with)
	return nil
}
