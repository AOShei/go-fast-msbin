package gofastmsbin_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/cfb/cfbtest"
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
	if w, ok := warning(doc.Warnings, gofastcore.WarnInputDecrypted); !ok || w.Message != "encrypted ("+scheme+"); decrypted with the password given" {
		tt.Errorf("%s: warnings %+v", what, doc.Warnings)
	}
	if !doc.Metadata.Encrypted {
		tt.Errorf("%s: the properties do not say the file was encrypted", what)
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
		if w, _ := warning(doc.Warnings, gofastcore.WarnInputDecrypted); !strings.HasSuffix(w.Message, "the password Excel uses when none is set") || len(doc.Parts) != 2 || !doc.Metadata.Encrypted {
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
		doc := opens(tt, name, cfbtest.Compound(streams), "open sesame", name)
		b := doc.Parts[0].Blocks
		if len(b) != 2 || b[0].Kind != gofastcore.KindHeading || b[0].Text != "A heading" || !strings.HasSuffix(b[1].Text, "enough for several keys.") || len(b[1].Text) < 2000 {
			tt.Errorf("%s: %d blocks, %q", name, len(b), b[0].Text)
		}
	}
}

// A document of Word 6 or 95 knows one scheme, XOR obfuscation: it is
// read with its password.
func TestEncryptedWord6(tt *testing.T) {
	w := t.Word6Stream(0x0409, "A heading\rText in the code page: caf\xe9.\r")
	t.EncryptWord6(w, "open sesame")
	doc := opens(tt, "Word 6", cfbtest.Compound(map[string][]byte{"WordDocument": w}), "open sesame", "XOR obfuscation")
	wantLines(tt, "Word 6", lines(&doc.Parts[0]), []string{"paragraph A heading", "paragraph Text in the code page: caf\u00e9."})
}

// What this library reads of a Word document behind a password,
// LibreOffice reads too, with the same password and no other: the
// obfuscation and the encryption written here are those another program
// knows. LibreOffice is run only where it is asked for
// (GOFAST_MSBIN_LIBREOFFICE=1), contained by the script that runs it.
func TestEncryptedWordAgainstLibreOffice(tt *testing.T) {
	if os.Getenv("GOFAST_MSBIN_LIBREOFFICE") == "" {
		tt.Skip("set GOFAST_MSBIN_LIBREOFFICE=1 to run LibreOffice")
	}
	dir := tt.TempDir()
	const text = "A heading behind a password\rThe quick brown fox jumps over the lazy dog.\rCaf\xe9 na\xefve: accents kept.\r"
	want := "A heading behind a password\nThe quick brown fox jumps over the lazy dog.\nCaf\u00e9 na\u00efve: accents kept."
	files := map[string][]byte{}
	for name, scheme := range map[string]int{"xor97.doc": t.CryptXOR, "rc4.doc": t.CryptRC4} {
		streams := t.WordStreams(t.DocFile{Styles: docStyles, Reserve: 256, Main: []t.DocPara{
			{Text: "A heading behind a password"}, {Text: "The quick brown fox jumps over the lazy dog."}, {Text: "Caf\u00e9 na\u00efve: accents kept."},
		}})
		t.EncryptWord(streams, scheme, "secret word 1")
		files[name] = cfbtest.Compound(streams)
	}
	w := t.Word6Stream(0x0409, text)
	t.EncryptWord6(w, "secret word 1")
	files["xor6.doc"] = cfbtest.Compound(map[string][]byte{"WordDocument": w})
	args := []string{filepath.Join("testdata", "oracle_crypt.py"), filepath.Join(dir, "profile")}
	for name, data := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			tt.Fatal(err)
		}
		args = append(args, path+"=secret word 1", path+"=not it")
		// Here: the text with the password, and a refusal without it.
		doc, err := extract(data, &gofastcore.Options{Password: "secret word 1"})
		if err != nil {
			tt.Fatalf("%s: %v", name, err)
		}
		var got []string
		for _, b := range doc.Parts[0].Blocks {
			got = append(got, b.Text)
		}
		if strings.Join(got, "\n") != want {
			tt.Errorf("%s, here: %q", name, got)
		}
		if _, err := extract(data, &gofastcore.Options{Password: "not it"}); !errors.Is(err, gofastcore.ErrWrongPassword) {
			tt.Errorf("%s, here, a wrong password: %v", name, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) } // the script then stops LibreOffice
	cmd.WaitDelay = 20 * time.Second
	out, err := cmd.Output()
	if err != nil {
		tt.Skipf("LibreOffice could not be run: %v", err)
	}
	var read map[string]*string
	if err := json.Unmarshal(out, &read); err != nil {
		tt.Fatal(err)
	}
	for name := range files {
		if got := read[name+"=secret word 1"]; got == nil || *got != want {
			tt.Errorf("%s, LibreOffice with the password: %v", name, got)
		}
		if got := read[name+"=not it"]; got != nil {
			tt.Errorf("%s: LibreOffice opens it with a wrong password", name)
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
	// Its properties are in a stream of their own, encrypted set by set.
	f := t.PPTFile{Summary: map[string][]byte{
		"\x05SummaryInformation": t.PropertyStream(t.Section{Format: t.FmtSummary, CodePage: 1252,
			Props: []t.Prop{{ID: 2, Value: "Kept from view"}, {ID: 4, Value: "An author"}}}),
		"\x05DocumentSummaryInformation": t.PropertyStream(t.Section{Format: t.FmtDocSummary, CodePage: 1200,
			Props: []t.Prop{{ID: 15, Value: t.Wide("A company")}}}),
	}}
	streams := f.Encrypt(deck.all(), pptDoc, pictures, "hello")
	doc := opens(tt, "presentation", cfbtest.Compound(streams), "hello", "RC4 CryptoAPI")
	wantLines(tt, "the slide", slideLines(&doc.Parts[0]), []string{"heading1 Behind a password", `image image/png 4x3 ""`})
	if m := doc.Metadata; m.Title != "Kept from view" || len(m.Authors) != 1 || m.Authors[0] != "An author" || m.Extra["company"] != "A company" {
		tt.Errorf("the properties: %+v", m)
	}
	if _, ok := warning(doc.Warnings, gofastcore.WarnInputStreamDamaged); ok {
		tt.Errorf("warnings: %+v", doc.Warnings)
	}
	// A list of the sets that breaks off: what can be read, and a warning.
	whole := streams["EncryptedSummary"]
	streams["EncryptedSummary"] = whole[:len(whole)-30]
	doc = opens(tt, "presentation with a damaged list", cfbtest.Compound(streams), "hello", "RC4 CryptoAPI")
	if w, ok := warning(doc.Warnings, gofastcore.WarnInputStreamDamaged); !ok || w.Message != "the property stream EncryptedSummary" || doc.Metadata.Title != "" && doc.Metadata.Title != "Kept from view" {
		tt.Errorf("a damaged list: %+v, %+v", doc.Metadata, doc.Warnings)
	}
	// One that points outside the stream, or is noise: no more than that.
	for _, n := range []int{0, 5, 8, 40} {
		streams["EncryptedSummary"] = append([]byte(nil), whole[:n]...)
		doc = opens(tt, "presentation with a cut stream", cfbtest.Compound(streams), "hello", "RC4 CryptoAPI")
		if _, ok := warning(doc.Warnings, gofastcore.WarnInputStreamDamaged); !ok {
			tt.Errorf("a stream of %d bytes: %+v", n, doc.Warnings)
		}
	}
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
		if w, _ := warning(doc.Warnings, gofastcore.WarnInputDecrypted); !strings.HasPrefix(w.Message, "encrypted ("+want[1]+")") || !doc.Metadata.Encrypted {
			tt.Errorf("%s: encrypted %v, %+v", name, doc.Metadata.Encrypted, doc.Warnings)
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
		if name == "testPPT_protected_passtika.ppt" && (doc.Metadata.Title != "Encrypted" || doc.Metadata.Application != "Microsoft Office PowerPoint") {
			tt.Errorf("%s: its properties: %+v", name, doc.Metadata)
		}
		if name == "ppt_with_png_encrypted.ppt" && pictures == 0 {
			tt.Errorf("%s: its picture was not decrypted", name)
		}
	}
	tt.Logf("%d encrypted files read with their passwords", read)
}
