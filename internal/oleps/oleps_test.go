package oleps_test

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/AOShei/go-fast-msbin/internal/msbintest"
	"github.com/AOShei/go-fast-msbin/internal/oleps"
)

func typed(t uint16, body ...byte) msbintest.Raw {
	return append(binary.LittleEndian.AppendUint32(nil, uint32(t)), body...)
}

func u64(v uint64) []byte { return binary.LittleEndian.AppendUint64(nil, v) }

// Every type of value a property can have is read, or passed over.
func TestValueTypes(t *testing.T) {
	when := time.Date(1999, 12, 31, 12, 0, 0, 0, time.UTC)
	props := []msbintest.Prop{
		{ID: 2, Value: "text"},
		{ID: 3, Value: msbintest.Wide("wide ✓")},
		{ID: 4, Value: int32(-5)},
		{ID: 5, Value: int16(-6)},
		{ID: 6, Value: true},
		{ID: 7, Value: 2.5},
		{ID: 8, Value: when},
		{ID: 9, Value: time.Time{}},
		{ID: 10, Value: typed(0x13, 0xFF, 0xFF, 0xFF, 0xFF)},                                // unsigned
		{ID: 11, Value: typed(0x14, u64(1<<40)...)},                                         // 64 bits
		{ID: 12, Value: typed(0x15, u64(math.MaxUint64)...)},                                // unsigned, past int64
		{ID: 13, Value: typed(0x04, 0, 0, 0x20, 0x40)},                                      // float32 2.5
		{ID: 14, Value: typed(0x06, u64(123456)...)},                                        // currency 12.3456
		{ID: 15, Value: typed(0x07, u64(math.Float64bits(36525.5))...)},                     // a date: noon, 31 December 1999
		{ID: 16, Value: typed(0x41, 3, 0, 0, 0, 1, 2, 3, 0)},                                // a blob
		{ID: 17, Value: typed(0x0C, typed(0x03, 9, 0, 0, 0)...)},                            // a variant holding an integer
		{ID: 18, Value: msbintest.Strings{"one", "two"}},                                    // a vector of strings
		{ID: 19, Value: typed(0x1003, 2, 0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0)},                  // a vector of integers
		{ID: 20, Value: typed(0x08, 4, 0, 0, 0, 'b', 's', 't', 0)},                          // BSTR
		{ID: 21, Value: typed(0x07, u64(math.Float64bits(math.NaN()))...)},                  // no date
		{ID: 22, Value: typed(0x05, u64(math.Float64bits(math.Inf(1)))...)},                 // not a number
		{ID: 23, Value: typed(0x40, u64(math.MaxUint64)...)},                                // a date past the year 9999
		{ID: 24, Value: typed(0x100C, 1, 0, 0, 0, 0x1E, 0, 0, 0, 2, 0, 0, 0, 'v', 0, 0, 0)}, // a vector of variants
	}
	res := oleps.Read(msbintest.PropertyStream(msbintest.Section{Format: msbintest.FmtSummary, CodePage: 1252, Props: props}))
	if res.Damaged || len(res.Sets) != 1 || res.Sets[0].Kind != oleps.Summary || res.Sets[0].CodePage != 1252 {
		t.Fatalf("%+v", res)
	}
	want := map[uint32]string{
		2: "text", 3: "wide ✓", 4: "-5", 5: "-6", 6: "true", 7: "2.5", 8: "1999-12-31T12:00:00Z", 9: "",
		10: "4294967295", 11: "1099511627776", 12: "18446744073709551615", 13: "2.5", 14: "12.3456",
		15: "1999-12-31T12:00:00Z", 16: "", 17: "9", 18: "one\ntwo", 19: "", 20: "bst", 21: "", 22: "", 23: "", 24: "v",
	}
	if len(res.Sets[0].Properties) != len(want) {
		t.Fatalf("%d properties", len(res.Sets[0].Properties))
	}
	for _, p := range res.Sets[0].Properties {
		if got := p.Value.String(); got != want[p.ID] {
			t.Errorf("property %d: %q, want %q", p.ID, got, want[p.ID])
		}
	}
	if v, ok := res.Sets[0].Get(8); !ok || !v.Time.Equal(when) {
		t.Errorf("Get: %+v", v)
	}
}

// The names of a set whose code page is UTF-16 are in 16-bit units.
func TestUnicodeDictionary(t *testing.T) {
	res := oleps.Read(msbintest.PropertyStream(msbintest.Section{Format: msbintest.FmtUser, CodePage: 1200, Props: []msbintest.Prop{
		{ID: 2, Name: "Ünïcode", Value: int32(1)}, {ID: 3, Name: "second", Value: msbintest.Wide("x")}}}))
	if res.Damaged || len(res.Sets) != 1 || len(res.Sets[0].Properties) != 2 {
		t.Fatalf("%+v", res)
	}
	if p := res.Sets[0].Properties; p[0].Name != "Ünïcode" || p[1].Name != "second" || p[1].Value.Text != "x" {
		t.Errorf("%+v", p)
	}
}

func TestNotAPropertySet(t *testing.T) {
	for _, b := range [][]byte{nil, {1, 2, 3}, make([]byte, 100)} {
		if res := oleps.Read(b); !res.Damaged || len(res.Sets) != 0 {
			t.Errorf("%d bytes: %+v", len(b), res)
		}
	}
}

func TestFiletime(t *testing.T) {
	if got := oleps.Filetime(116444736000000000); !got.Equal(time.Unix(0, 0)) {
		t.Errorf("1970: %v", got)
	}
	if got := oleps.Filetime(1); got.Year() != 1601 {
		t.Errorf("1601: %v", got)
	}
	if !oleps.Filetime(0).IsZero() || !oleps.Filetime(math.MaxUint64).IsZero() {
		t.Error("a date not set, or past the year 9999, is given")
	}
}

func FuzzRead(f *testing.F) {
	f.Add(msbintest.PropertyStream(msbintest.Section{Format: msbintest.FmtSummary, CodePage: 1252, Props: []msbintest.Prop{
		{ID: 2, Value: "title"}, {ID: 12, Value: time.Unix(1e9, 0)}, {ID: 18, Value: msbintest.Strings{"a", "b"}}}}))
	f.Add(msbintest.PropertyStream(
		msbintest.Section{Format: msbintest.FmtDocSummary, CodePage: 1200, Props: []msbintest.Prop{{ID: 15, Value: msbintest.Wide("co")}}},
		msbintest.Section{Format: msbintest.FmtUser, CodePage: 932, Props: []msbintest.Prop{{ID: 2, Name: "n", Value: true}}}))
	f.Fuzz(func(t *testing.T, data []byte) {
		res := oleps.Read(data)
		total := 0
		for _, s := range res.Sets {
			for _, p := range s.Properties {
				total += len(p.Value.String()) + len(p.Name)
			}
		}
		// What is given is bounded by what was read: decoding makes a
		// byte three at most, and a value can be given through a name.
		if total > 4*len(data)+2048 {
			t.Fatalf("%d bytes of values from %d bytes", total, len(data))
		}
	})
}

// Word for the Macintosh writes the identifier of a set with its fields
// in the other byte order, and its text in the Mac Roman code page.
func TestMacintoshIdentifier(t *testing.T) {
	id := msbintest.FmtSummary
	id[0], id[1], id[2], id[3] = id[3], id[2], id[1], id[0]
	id[4], id[5], id[6], id[7] = id[5], id[4], id[7], id[6]
	res := oleps.Read(msbintest.PropertyStream(msbintest.Section{Format: id, CodePage: 10000, Props: []msbintest.Prop{{ID: 2, Value: "R\x8esum\x8e"}}}))
	if len(res.Sets) != 1 || res.Sets[0].Kind != oleps.Summary || res.Sets[0].Properties[0].Value.Text != "Résumé" {
		t.Errorf("%+v", res)
	}
}
