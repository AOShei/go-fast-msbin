package gofastmsbin_test

import (
	"bytes"
	"context"
	"fmt"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// A whole file as one document.
func ExampleExtract() {
	data := msbintest.BookFile(msbintest.Workbook(nil, msbintest.Sheet{Name: "Prices", Records: [][]byte{
		msbintest.Label(0, 0, 0, "Item"), msbintest.Label(0, 1, 0, "Price"),
		msbintest.Label(1, 0, 0, "Tea"), msbintest.Number(1, 1, 0, 2.5),
	}}))
	f, err := gofastmsbin.OpenBytes(data, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f.Close()
	doc, err := f.Document(context.Background())
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(doc.Source.Format, len(doc.Parts), doc.Parts[0].Name)
	for _, row := range doc.Parts[0].Blocks[0].Table.Rows {
		fmt.Println(row)
	}
	// Output:
	// xls 1 Prices
	// [Item Price]
	// [Tea 2.5]
}

// A part at a time: the sheets of a workbook or the slides of a
// presentation are read as they are asked for, and nothing is kept of the
// ones that were read.
func ExampleFile_Parts() {
	var sheets []msbintest.Sheet
	for _, name := range []string{"January", "February", "March"} {
		sheets = append(sheets, msbintest.Sheet{Name: name, Records: [][]byte{msbintest.Label(0, 0, 0, "Sales in "+name)}})
	}
	f, err := gofastmsbin.OpenBytes(msbintest.BookFile(msbintest.Workbook(nil, sheets...)), &gofastcore.Options{Workers: 1})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f.Close()
	for _, sheet := range f.Sheets() {
		fmt.Println(sheet.Number, sheet.Name)
	}
	for part, err := range f.Parts(context.Background()) {
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println(part.Number, part.Blocks[0].Table.Rows[0][0])
	}
	// Output:
	// 1 January
	// 2 February
	// 3 March
	// 1 Sales in January
	// 2 Sales in February
	// 3 Sales in March
}

// An encrypted file is opened with its password.
func ExampleExtract_password() {
	stream := msbintest.Workbook(nil, msbintest.Sheet{Name: "Private", Records: [][]byte{msbintest.Label(0, 0, 0, "for your eyes only")}})
	data := msbintest.BookFile(msbintest.EncryptWorkbook(stream, msbintest.CryptRC4, "sesame"))
	_, err := gofastmsbin.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), nil)
	fmt.Println(err)
	doc, err := gofastmsbin.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), &gofastcore.Options{Password: "sesame"})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(doc.Parts[0].Blocks[0].Table.Rows[0][0])
	fmt.Println(doc.Warnings[0].Code)
	// Output:
	// gofastmsbin: Excel 97-2003 (BIFF8) file, encrypted (RC4): no password given: gofastcore: password required
	// for your eyes only
	// input.decrypted
}
