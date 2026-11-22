package warn

import (
	"strings"
	"sync"
	"testing"

	"github.com/AOShei/go-fast-core"
)

// Every bound has a message, which names the bound's number and a unit.
func TestEveryCapHasAMessage(t *testing.T) {
	for c := Cap(0); c < numCaps; c++ {
		info := caps[c]
		if info.what == "" || info.one == "" || info.many == "" {
			t.Errorf("cap %d has no message", c)
		}
		if !strings.ContainsAny(info.what, "0123456789") {
			t.Errorf("cap %d does not say its bound: %q", c, info.what)
		}
	}
}

// The warnings come once per bound, in the order the bounds were met,
// with the counts added up, and a unit in the singular for one.
func TestLostSaysEachBoundOnce(t *testing.T) {
	var l Lost
	l.Add(CapProjects, 1)
	l.Add(CapModules, 2)
	l.Add(CapProjects, 2)
	l.Add(CapObjects, 0) // nothing left out: nothing said
	var o Lost
	o.Add(CapCustomProps, 1)
	l.Merge(&o)
	ws := l.Warnings()
	want := []string{
		"VBA projects past 16 were not read (3 projects)",
		"modules of a VBA project past 4096 were not read (2 modules)",
		"custom document properties past 256 were not read (1 property)",
	}
	if len(ws) != len(want) {
		t.Fatalf("%v", ws)
	}
	for i, w := range ws {
		if w.Message != want[i] || w.Code != gofastcore.WarnLimitExceeded || w.Severity != gofastcore.SeverityError {
			t.Errorf("%d: %+v", i, w)
		}
	}
	var p gofastcore.Part
	if !l.Say(&p) || len(p.Warnings) != 3 || l.Any() || l.Say(&p) {
		t.Errorf("said %v, then %v", p.Warnings, l.Any())
	}
	var none *Lost
	none.Add(CapCustomProps, 1) // a nil Lost counts nothing
	if none.Any() || none.Warnings() != nil {
		t.Error("a nil Lost said something")
	}
}

// What parts share may be read by any of them at once.
func TestLostConcurrently(t *testing.T) {
	var l Lost
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				l.Add(CapProperties, 1)
			}
		}()
	}
	wg.Wait()
	if n := l.Count(CapProperties); n != 8000 {
		t.Errorf("%d", n)
	}
}

// A warning that could repeat is one, with how many and the first eight.
func TestNamesMessage(t *testing.T) {
	var n Names
	if n.Message("part", "parts") != "" {
		t.Error("said with nothing counted")
	}
	n.AddOnce("a")
	if got := n.Message("part", "parts"); got != "1 part (a)" {
		t.Errorf("%q", got)
	}
	for _, s := range []string{"a", "b", "a", "c", "d", "e", "f", "g", "h", "i", "j"} {
		n.AddOnce(s)
	}
	if got := n.Message("part", "parts"); got != "10 parts (a, b, c, d, e, f, g, h, and 2 more)" {
		t.Errorf("%q", got)
	}
	for range 3 {
		n.Add("x") // every one counted, each name listed once
	}
	if got := n.Message("reference", "references"); got != "3 references (x, and 2 more)" {
		t.Errorf("%q", got)
	}
}
