// Package detect says what a file is: which of the three formats, written
// by which generation of the program, and whether it is encrypted. It
// decides from the file's content, never from a name.
package detect

import (
	"bytes"
	"encoding/binary"
	"io"

	"github.com/AOShei/go-fast-msbin/internal/cfb"
)

// BIFF8 is the version of a workbook of Excel 97 to 2003.
const BIFF8 = "Excel 97-2003 (BIFF8)"

// Word97 is the version of a document of Word 97 to 2003.
const Word97 = "Word 97-2003"

// Formats, as gofastcore.Source.Format has them.
const (
	Doc = "doc"
	Xls = "xls"
	Ppt = "ppt"
)

// MIME gives the media type of a format.
func MIME(format string) string {
	switch format {
	case Doc:
		return "application/msword"
	case Xls:
		return "application/vnd.ms-excel"
	case Ppt:
		return "application/vnd.ms-powerpoint"
	}
	return ""
}

// Info is what a file says about itself.
type Info struct {
	// Format is doc, xls or ppt, or empty for a file that is none of them.
	Format string
	// Version names the generation of the format: "Word 97-2003",
	// "Word 6/95", "Excel 97-2003 (BIFF8)", "Excel 5/95 (BIFF5)",
	// "PowerPoint 97-2003", "PowerPoint 95 or older". It is empty where
	// the file does not say.
	Version string
	// Read reports whether this generation is one the library is to read.
	// Older ones are refused.
	Read bool
	// Encryption is empty for a file that is not encrypted, and otherwise
	// "XOR obfuscation", "RC4" or "RC4 CryptoAPI"; "encrypted" where the
	// file says it is and not how.
	Encryption string
	// Other says what a file that is none of the three formats is, in
	// words that fit after "the file is ".
	Other string
}

// Stream names in the root storage.
const (
	wordStream     = "WordDocument"
	workbookStream = "Workbook"
	bookStream     = "Book" // Excel 5 and 95
	pptStream      = "PowerPoint Document"
	currentUser    = "Current User"
	// DualStorage is the storage that holds the PowerPoint 97
	// presentation of a file saved for PowerPoint 95 and 97 both.
	DualStorage = "PP97_DUALSTORAGE"
)

// Compound says what the open compound file is.
func Compound(f *cfb.File) Info {
	switch {
	case f.Has(wordStream):
		return word(f)
	case f.Has(workbookStream):
		return excel(f, workbookStream)
	case f.Has(bookStream):
		return excel(f, bookStream)
	case f.Has(pptStream):
		return powerpoint(f)
	case f.Has("PP40"):
		return Info{Format: Ppt, Version: "PowerPoint 95 or older"}
	case f.Has("EncryptedPackage"), f.Has("\x06DataSpaces"):
		return Info{Other: "an encrypted Office Open XML package (.docx, .xlsx, .pptx)"}
	case f.Has("__properties_version1.0"):
		return Info{Other: "an Outlook message (.msg)"}
	case f.Has("VisioDocument"):
		return Info{Other: "a Visio drawing"}
	case f.Has("Quill"):
		return Info{Other: "a Publisher document"}
	}
	return Info{Other: "a compound file with no Word, Excel or PowerPoint document in it"}
}

func head(f *cfb.File, name string, n int, in ...string) []byte {
	s, err := f.Open(append(in, name)...)
	if err != nil {
		return nil
	}
	buf := make([]byte, min(int64(n), s.Size()))
	got, err := s.ReadAt(buf, 0)
	if err != nil && err != io.EOF {
		return nil
	}
	return buf[:got]
}

// word reads the start of the file information block ([MS-DOC] 2.5.1,
// FibBase): the identifier, the version number nFib and the flags.
func word(f *cfb.File) Info {
	in := Info{Format: Doc}
	b := head(f, wordStream, 32)
	if len(b) < 12 {
		return in
	}
	le := binary.LittleEndian
	ident, nFib, flags := le.Uint16(b), le.Uint16(b[2:]), le.Uint16(b[10:])
	switch {
	case ident == 0xA5EC && nFib > 104:
		in.Version, in.Read = "Word 97-2003", true
	case ident == 0xA5DC || nFib >= 101 && nFib <= 104:
		in.Version, in.Read = "Word 6/95", true
	case ident == 0xA5DB || ident == 0xA59B:
		in.Version = "Word 2 or older"
	}
	const fEncrypted, fObfuscated = 0x0100, 0x8000
	if flags&fEncrypted != 0 {
		in.Encryption = "encrypted"
		switch {
		case flags&fObfuscated != 0 || in.Version == "Word 6/95":
			in.Encryption = "XOR obfuscation"
		case in.Version == "Word 97-2003":
			// The table stream begins with the encryption header, whose
			// version says which scheme ([MS-DOC] 2.2.6.2, 2.2.6.3).
			table := "0Table"
			if flags&0x0200 != 0 {
				table = "1Table"
			}
			if t := head(f, table, 4); len(t) == 4 {
				in.Encryption = rc4Scheme(le.Uint16(t), le.Uint16(t[2:]))
			}
		}
	}
	return in
}

// rc4Scheme names the scheme of an encryption header by its version
// ([MS-OFFCRYPTO] 2.3.6.1, 2.3.5.1).
func rc4Scheme(major, minor uint16) string {
	switch {
	case major == 1 && minor == 1:
		return "RC4"
	case major >= 2 && major <= 4 && minor == 2:
		return "RC4 CryptoAPI"
	}
	return "encrypted"
}

// excel reads the first records of the workbook stream: the BOF record,
// whose version says which generation wrote it, and FILEPASS, which a
// file has when it is encrypted ([MS-XLS] 2.4.21, 2.4.117).
func excel(f *cfb.File, name string) Info {
	in := Info{Format: Xls}
	b := head(f, name, 4096)
	le := binary.LittleEndian
	if len(b) < 8 {
		return in
	}
	switch typ := le.Uint16(b); typ {
	case 0x0809:
		// The version's high byte says which generation; some programs
		// set bits of the low one.
		switch le.Uint16(b[4:]) >> 8 {
		case 0x06:
			in.Version, in.Read = BIFF8, true
		case 0x05:
			in.Version, in.Read = "Excel 5/95 (BIFF5)", true
		}
	case 0x0409:
		in.Version = "Excel 4 (BIFF4)"
	case 0x0209:
		in.Version = "Excel 3 (BIFF3)"
	case 0x0009:
		in.Version = "Excel 2 (BIFF2)"
	}
	// FILEPASS, if there is one, follows the BOF record closely, before
	// the first record of content.
	for at, steps := 0, 0; at+4 <= len(b) && steps < 64; steps++ {
		typ, size := le.Uint16(b[at:]), int(le.Uint16(b[at+2:]))
		body := b[at+4 : min(at+4+size, len(b))]
		if typ == 0x002F {
			in.Encryption = "encrypted"
			switch {
			case in.Version != "Excel 97-2003 (BIFF8)" || len(body) >= 2 && le.Uint16(body) == 0:
				in.Encryption = "XOR obfuscation"
			case len(body) >= 6 && le.Uint16(body) == 1:
				in.Encryption = rc4Scheme(le.Uint16(body[2:]), le.Uint16(body[4:]))
			}
			break
		}
		if typ == 0x000A || typ == 0x0085 { // EOF, BOUNDSHEET
			break
		}
		at += 4 + size
	}
	return in
}

// powerpoint reads the Current User stream ([MS-PPT] 2.3.2,
// CurrentUserAtom), whose token says whether the document is encrypted.
func powerpoint(f *cfb.File) Info {
	in := Info{Format: Ppt, Version: "PowerPoint 97-2003", Read: true}
	if f.Has("PP40") {
		return Info{Format: Ppt, Version: "PowerPoint 95 or older"}
	}
	// A presentation saved for PowerPoint 95 and 97 both: the one of
	// PowerPoint 97 is in a storage of its own, and is the one read. A
	// file with the streams of PowerPoint 95 and no such storage is of
	// PowerPoint 95.
	var dual []string
	switch {
	case f.Has(DualStorage, pptStream):
		dual = []string{DualStorage}
	case f.Has("Text_Content") && f.Has("PersistentStorage Directory"):
		return Info{Format: Ppt, Version: "PowerPoint 95 or older"}
	}
	b := head(f, currentUser, 32, dual...)
	if len(b) < 24 {
		return in
	}
	le := binary.LittleEndian
	if le.Uint32(b[12:]) == 0xF3D1C4DF {
		in.Encryption = "RC4 CryptoAPI"
	}
	if v := le.Uint16(b[22:]); v != 0 && v < 0x03F4 {
		in.Version, in.Read = "PowerPoint 95 or older", false
	}
	return in
}

// Bare says what a file that is not a compound file is, for the message
// of the refusal: older versions of the three formats were plain files,
// and files of other formats are often given these formats' names. It is
// empty for a file it does not recognise.
func Bare(start []byte) Info {
	le := binary.LittleEndian
	trimmed := bytes.TrimLeft(start, " \t\r\n\xef\xbb\xbf")
	lower := bytes.ToLower(trimmed[:min(len(trimmed), 256)])
	switch {
	case len(start) >= 4 && (le.Uint16(start) == 0x0009 || le.Uint16(start) == 0x0209 || le.Uint16(start) == 0x0409) && le.Uint16(start[2:]) <= 16:
		v := map[uint16]string{0x0009: "Excel 2 (BIFF2)", 0x0209: "Excel 3 (BIFF3)", 0x0409: "Excel 4 (BIFF4)"}[le.Uint16(start)]
		return Info{Format: Xls, Version: v}
	case len(start) >= 8 && le.Uint16(start) == 0x0809 && le.Uint16(start[2:]) >= 8 && (le.Uint16(start[4:]) == 0x0500 || le.Uint16(start[4:]) == 0x0600):
		return Info{Format: Xls, Version: "Excel workbook stream outside a compound file"}
	case len(start) >= 2 && (le.Uint16(start) == 0xA5DB || le.Uint16(start) == 0xA59B):
		return Info{Format: Doc, Version: "Word 2 or older"}
	case bytes.HasPrefix(start, []byte("PK\x03\x04")):
		return Info{Other: "a zip archive, as an Office Open XML file (.docx, .xlsx, .pptx) or an OpenDocument file is"}
	case bytes.HasPrefix(trimmed, []byte(`{\rtf`)):
		return Info{Other: "an RTF document"}
	case bytes.HasPrefix(lower, []byte("<?xml")), bytes.HasPrefix(lower, []byte("<html")), bytes.HasPrefix(lower, []byte("<!doctype")):
		return Info{Other: "an XML or HTML document"}
	case bytes.HasPrefix(start, []byte("%PDF-")):
		return Info{Other: "a PDF document"}
	}
	return Info{}
}
