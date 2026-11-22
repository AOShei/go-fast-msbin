package gofastmsbin_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
	t "github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// opens checks what an encrypted file gives: an error that says a
// password is required with none, one that says the password is wrong
// with a wrong one, and with the right one the document, with a warning
// that names the scheme.
func opens(tt *testing.T, what string, data []byte, password, scheme string) *gofastcore.Document {
	tt.Helper()
	if _, err := extract(data, nil); !errors.Is(err, gofastcore.ErrPasswordRequired) || !strings.Contains(err.Error(), scheme) {
		tt.Errorf("%s, no password: %v", what, err)
	}
	if _, err := extract(data, &gofastcore.Options{Password: "not the password"}); !errors.Is(err, gofastcore.ErrWrongPassword) {
		tt.Errorf("%s, a wrong password: %v", what, err)
	}
	doc, err := extract(data, &gofastcore.Options{Password: password, Images: gofastcore.ImagesMeta})
	if err != nil {
		tt.Fatalf("%s: %v", what, err)
	}
	if err := doc.Validate(); err != nil {
		tt.Fatalf("%s: %v", what, err)
	}
	if w, ok := warning(doc.Warnings, gofastmsbin.WarnDecrypted); !ok || w.Message != "encrypted ("+scheme+"); decrypted with the password given" {
		tt.Errorf("%s: warnings %+v", what, doc.Warnings)
	}
	return doc
}

// An encrypted workbook is read with its password, in each of the three
// schemes; one encrypted with the password Excel uses when none was set
// is read without being given one.
func TestEncryptedWorkbooks(tt *testing.T) {
	var cells [][]byte
	for row := uint16(0); row < 400; row++ {
		cells = append(cells, t.Label(row, 0, xfGeneral, "a row of the sheet"), t.Number(row, 1, xfGeneral, float64(row)))
	}
	stream := t.Workbook(styles, t.Sheet{Name: "First", Records: cells}, t.Sheet{Name: "Second", Records: [][]byte{t.Label(0, 0, xfGeneral, "on the second sheet")}})
	for scheme, name := range map[int]string{t.CryptXOR: "XOR obfuscation", t.CryptRC4: "RC4", t.CryptAPI: "RC4 CryptoAPI"} {
		doc := opens(tt, name, t.BookFile(t.EncryptWorkbook(stream, scheme, "sésame")), "sésame", name)
		if len(doc.Parts) != 2 || doc.Parts[0].Name != "First" || doc.Parts[1].Name != "Second" {
			tt.Fatalf("%s: %d parts", name, len(doc.Parts))
		}
		rows := doc.Parts[0].Blocks[0].Table.Rows
		if len(rows) != 400 || rows[399][0] != "a row of the sheet" || rows[399][1] != "399" {
			tt.Errorf("%s: %d rows, the last %q", name, len(rows), rows[len(rows)-1])
		}
		if got := doc.Parts[1].Blocks[0].Table.Rows[0][0]; got != "on the second sheet" {
			tt.Errorf("%s: the second sheet: %q", name, got)
		}
		// Excel's own password is tried without being asked for.
		doc, err := extract(t.BookFile(t.EncryptWorkbook(stream, scheme, "VelvetSweatshop")), nil)
		if err != nil {
			tt.Fatalf("%s, Excel's password: %v", name, err)
		}
		if w, _ := warning(doc.Warnings, gofastmsbin.WarnDecrypted); !strings.HasSuffix(w.Message, "the password Excel uses when none is set") || len(doc.Parts) != 2 {
			tt.Errorf("%s, Excel's password: %+v", name, doc.Warnings)
		}
	}
}

// An encrypted Word document is read with its password.
func TestEncryptedDocuments(tt *testing.T) {
	file := t.DocFile{Styles: docStyles, Reserve: 256, Main: []t.DocPara{
		{Text: "A heading", Style: 1}, {Text: strings.Repeat("The text of the document, which is long enough for several keys. ", 40)},
	}}
	for scheme, name := range map[int]string{t.CryptXOR: "XOR obfuscation", t.CryptRC4: "RC4", t.CryptAPI: "RC4 CryptoAPI"} {
		streams := t.WordStreams(file)
		t.EncryptWord(streams, scheme, "open sesame")
		doc := opens(tt, name, t.Compound(streams), "open sesame", name)
		b := doc.Parts[0].Blocks
		if len(b) != 2 || b[0].Kind != gofastcore.KindHeading || b[0].Text != "A heading" || !strings.HasSuffix(b[1].Text, "enough for several keys.") || len(b[1].Text) < 2000 {
			tt.Errorf("%s: %d blocks, %q", name, len(b), b[0].Text)
		}
	}
}

// An encrypted presentation is read with its password, its pictures with
// it.
func TestEncryptedPresentation(tt *testing.T) {
	group, pictures := t.PPTPictures(t.PNG(4, 3))
	deck := pptDeck{
		doc: [][]byte{group},
		slides: []pptSlide{{
			outline: t.PPTText(0, "Behind a password"),
			shapes: [][]byte{
				t.PPTShape{Top: 100, Left: 100, Right: 900, Bottom: 300, Placeholder: 13, OutlineRef: 1}.Bytes(),
				t.PPTShape{Top: 400, Left: 100, Right: 900, Bottom: 900, Props: []t.ArtProp{{ID: 0x0104, Value: 1}}}.Bytes(),
			},
			layout: 1,
		}},
	}
	var f t.PPTFile
	streams := f.Encrypt(deck.all(), pptDoc, pictures, "hello")
	doc := opens(tt, "presentation", t.Compound(streams), "hello", "RC4 CryptoAPI")
	wantLines(tt, "the slide", slideLines(&doc.Parts[0]), []string{"heading1 Behind a password", `image image/png 4x3 ""`})
}

// The encrypted files of the corpus whose passwords their test suites
// give are read with them, and say what their authors wrote in them.
func TestEncryptedCorpus(tt *testing.T) {
	entries, err := t.Corpus("testdata")
	if err != nil || len(entries) == 0 {
		tt.Skip("no corpus: python3 testdata/fetch.py")
	}
	known := map[string][3]string{ // the file: its password, its scheme, text it holds
		"password.xls":                     {"password", "RC4", "A ZIP bomb is a variant of mail-bombing"},
		"xor-encryption-abc.xls":           {"abc", "XOR obfuscation", ""},
		"testEXCEL_protected_passtika.xls": {"tika", "RC4", ""},
		"35897-type4.xls":                  {"freedom", "RC4 CryptoAPI", "hello there!"},
		"password_password_cryptoapi.doc":  {"password", "RC4 CryptoAPI", "This is a test"},
		"password_tika_binaryrc4.doc":      {"tika", "RC4", "This is an encrypted Word 2007 File."},
		"Password_Protected-hello.ppt":     {"hello", "RC4 CryptoAPI", "This is a protected document"},
		"Password_Protected-np-hello.ppt":  {"hello", "RC4 CryptoAPI", "This is a protected document"},
		"ppt_with_png_encrypted.ppt":       {"password", "RC4 CryptoAPI", "Here is a PNG image:"},
		"testPPT_protected_passtika.ppt":   {"tika", "RC4 CryptoAPI", "This is an encrypted PowerPoint 2007 slide."},
		"cryptoapi-proc2356.ppt":           {"crypto", "RC4 CryptoAPI", "Dominic Salemno"},
	}
	read := 0
	for _, e := range entries {
		name := e.Path[strings.LastIndexByte(e.Path, '/')+1:]
		want, ok := known[name]
		if !ok || e.HeldOut() {
			continue
		}
		if _, err := gofastmsbin.ExtractFile(context.Background(), e.Path, nil); !errors.Is(err, gofastcore.ErrPasswordRequired) {
			tt.Errorf("%s, no password: %v", name, err)
		}
		doc, err := gofastmsbin.ExtractFile(context.Background(), e.Path, &gofastcore.Options{Password: want[0]})
		if err != nil {
			tt.Errorf("%s: %v", name, err)
			continue
		}
		read++
		if w, _ := warning(doc.Warnings, gofastmsbin.WarnDecrypted); !strings.HasPrefix(w.Message, "encrypted ("+want[1]+")") {
			tt.Errorf("%s: %+v", name, doc.Warnings)
		}
		var text strings.Builder
		pictures := 0
		for _, p := range doc.Parts {
			for _, b := range p.Blocks {
				text.WriteString(b.Text + "\n")
				if b.Table != nil {
					for _, row := range b.Table.Rows {
						text.WriteString(strings.Join(row, " ") + "\n")
					}
				}
				if b.Image != nil && b.Image.Width > 0 {
					pictures++
				}
			}
		}
		if !strings.Contains(text.String(), want[2]) {
			tt.Errorf("%s: %q is not in its text", name, want[2])
		}
		if name == "ppt_with_png_encrypted.ppt" && pictures == 0 {
			tt.Errorf("%s: its picture was not decrypted", name)
		}
	}
	tt.Logf("%d encrypted files read with their passwords", read)
}
