package gofastmsbin_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
)

func extract(data []byte, opts *gofastcore.Options) (*gofastcore.Document, error) {
	return gofastmsbin.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
}

// fib is the start of a Word file information block: identifier, version
// and flags.
func fib(ident, nFib, flags uint16) []byte {
	b := make([]byte, 68)
	binary.LittleEndian.PutUint16(b, ident)
	binary.LittleEndian.PutUint16(b[2:], nFib)
	binary.LittleEndian.PutUint16(b[10:], flags)
	return b
}

// record is one record of a workbook stream.
func record(typ uint16, body ...byte) []byte {
	b := binary.LittleEndian.AppendUint16(nil, typ)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(body)))
	return append(b, body...)
}

func u16s(v ...uint16) []byte {
	var b []byte
	for _, x := range v {
		b = binary.LittleEndian.AppendUint16(b, x)
	}
	return b
}

// currentUser is a presentation's Current User stream with the given
// token and file version.
func currentUser(token uint32, version uint16) []byte {
	b := make([]byte, 28)
	binary.LittleEndian.PutUint32(b[8:], 0x14)
	binary.LittleEndian.PutUint32(b[12:], token)
	binary.LittleEndian.PutUint16(b[22:], version)
	return b
}

// A file is told apart by its content: one of a generation that is read
// gives a document, and any other a refusal that says what the file is.
func TestFilesAreToldApart(t *testing.T) {
	bof8 := record(0x0809, u16s(0x0600, 0x0005, 0, 0, 0, 0, 0, 0)...)
	bof5 := record(0x0809, u16s(0x0500, 0x0005, 0, 0)...)
	for name, c := range map[string]struct {
		data []byte
		want string
		err  error
	}{
		"word 97":            {msbintest.Compound(map[string][]byte{"WordDocument": fib(0xA5EC, 0x00C1, 0x0200), "1Table": {}}), "doc: the document's text cannot be found", nil},
		"word 97, xor":       {msbintest.Compound(map[string][]byte{"WordDocument": fib(0xA5EC, 0x00C1, 0x8100)}), "Word 97-2003 file, encrypted (XOR obfuscation)", gofastcore.ErrUnsupported},
		"word 97, rc4":       {msbintest.Compound(map[string][]byte{"WordDocument": fib(0xA5EC, 0x00C1, 0x0300), "1Table": u16s(1, 1)}), "encrypted (RC4)", gofastcore.ErrUnsupported},
		"word 97, cryptoapi": {msbintest.Compound(map[string][]byte{"WordDocument": fib(0xA5EC, 0x00C1, 0x0100), "0Table": u16s(3, 2)}), "encrypted (RC4 CryptoAPI)", gofastcore.ErrUnsupported},
		"word 95":            {msbintest.Compound(map[string][]byte{"WordDocument": fib(0xA5DC, 104, 0)}), "the file information block is cut short", gofastcore.ErrMalformed},
		"excel 97":           {msbintest.Compound(map[string][]byte{"Workbook": bof8}), "xls: the workbook lists no sheets", nil},
		"excel 95":           {msbintest.Compound(map[string][]byte{"Book": bof5}), "xls: the workbook lists no sheets", nil},
		"excel both":         {msbintest.Compound(map[string][]byte{"Book": bof5, "Workbook": bof8}), "xls: the workbook lists no sheets", nil},
		"excel 97, xor":      {msbintest.Compound(map[string][]byte{"Workbook": append(bof8, record(0x002F, u16s(0, 1, 2)...)...)}), "encrypted (XOR obfuscation)", gofastcore.ErrUnsupported},
		"excel 97, rc4":      {msbintest.Compound(map[string][]byte{"Workbook": append(bof8, record(0x002F, u16s(1, 1, 1)...)...)}), "encrypted (RC4)", gofastcore.ErrUnsupported},
		"excel 97, capi":     {msbintest.Compound(map[string][]byte{"Workbook": append(bof8, record(0x002F, u16s(1, 4, 2)...)...)}), "encrypted (RC4 CryptoAPI)", gofastcore.ErrUnsupported},
		"powerpoint 97":      {msbintest.Compound(map[string][]byte{"PowerPoint Document": {1}, "Current User": currentUser(0xE391C05F, 0x03F4)}), "ppt: the presentation's slides cannot be found", nil},
		"powerpoint 95, new": {msbintest.Compound(map[string][]byte{"PowerPoint Document": {1}, "Text_Content": {1}, "PersistentStorage Directory": {1}}), "PowerPoint 95 or older file: this generation of the format is not read", gofastcore.ErrUnsupported},
		"powerpoint, capi":   {msbintest.Compound(map[string][]byte{"PowerPoint Document": {1}, "Current User": currentUser(0xF3D1C4DF, 0x03F4)}), "encrypted (RC4 CryptoAPI)", gofastcore.ErrUnsupported},
		"powerpoint 95":      {msbintest.Compound(map[string][]byte{"PowerPoint Document": {1}, "PP40": {1}}), "PowerPoint 95 or older file: this generation of the format is not read", gofastcore.ErrUnsupported},
		"encrypted package":  {msbintest.Compound(map[string][]byte{"EncryptedPackage": {1}, "EncryptionInfo": {1}}), "an encrypted Office Open XML package", gofastcore.ErrUnsupported},
		"outlook message":    {msbintest.Compound(map[string][]byte{"__properties_version1.0": {1}}), "an Outlook message", gofastcore.ErrUnsupported},
		"other compound":     {msbintest.Compound(map[string][]byte{"Contents": {1}}), "no Word, Excel or PowerPoint document in it", gofastcore.ErrUnsupported},
		"excel 4":            {append(record(0x0409, u16s(4, 0x10, 0)...), make([]byte, 600)...), "Excel 4 (BIFF4) file: this generation of the format is not read", gofastcore.ErrUnsupported},
		"zip":                {append([]byte("PK\x03\x04"), make([]byte, 600)...), "a zip archive", gofastcore.ErrUnsupported},
		"rtf":                {[]byte(`{\rtf1 hello}`), "an RTF document", gofastcore.ErrUnsupported},
		"html":               {[]byte("\n<html><body>a table saved as .xls</body></html>"), "an XML or HTML document", gofastcore.ErrUnsupported},
		"text":               {[]byte("just text"), "not a compound file", gofastcore.ErrUnsupported},
		"empty":              {nil, "not a compound file", gofastcore.ErrUnsupported},
		"cut in the header":  {msbintest.Compound(map[string][]byte{"WordDocument": fib(0xA5EC, 0x00C1, 0)})[:100], "cut off inside its header", gofastcore.ErrMalformed},
		"no directory":       {msbintest.Compound(map[string][]byte{"WordDocument": fib(0xA5EC, 0x00C1, 0)})[:512], "cannot be read", gofastcore.ErrMalformed},
	} {
		doc, err := extract(c.data, nil)
		if c.err == nil {
			// A file of a generation that is read gives a document, whose
			// part says which generation it is.
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			if err := doc.Validate(); err != nil {
				t.Errorf("%s: %v", name, err)
			}
			got := doc.Source.Format + ": "
			for _, w := range doc.Parts[0].Warnings {
				if w.Code == gofastcore.WarnPartFailed {
					got += w.Message
				}
			}
			if !strings.HasPrefix(got, c.want) && !strings.Contains(got, strings.Replace(c.want, ": ", ": the content of a ", 1)) {
				t.Errorf("%s: %s, want %q", name, got, c.want)
			}
			continue
		}
		if doc != nil || !errors.Is(err, c.err) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v (document: %v), want %q", name, err, doc != nil, c.want)
		}
	}
}

func TestInputOverTheLimit(t *testing.T) {
	data := msbintest.Compound(map[string][]byte{"Workbook": make([]byte, 5000)})
	_, err := extract(data, &gofastcore.Options{Limits: gofastcore.Limits{MaxInputBytes: 1000}})
	if !errors.Is(err, gofastcore.ErrLimitExceeded) {
		t.Errorf("%v", err)
	}
	if _, err := extract(data, &gofastcore.Options{Limits: gofastcore.Limits{MaxInputBytes: -1}}); !errors.Is(err, gofastcore.ErrUnsupported) && err != nil {
		t.Errorf("no limit: %v", err)
	}
}

func TestFormats(t *testing.T) {
	if got := strings.Join((&gofastmsbin.Extractor{}).Formats(), " "); got != "doc xls ppt" {
		t.Errorf("formats: %s", got)
	}
}
