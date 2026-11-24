//go:build xlsscratchpoison

package xls

import "testing"

// With the tag, what a buffer held reads '#' once the buffer is taken up
// again, to the whole of its capacity.
func TestPoisonOverwritesHeldViews(t *testing.T) {
	buf := append(make([]byte, 0, 16), "a held view"...)
	held := buf
	buf = append(reuse(buf), "new"...)
	if string(held) != "new########" || string(buf[:cap(buf)]) != "new#############" {
		t.Errorf("held %q, the buffer %q", held, buf[:cap(buf)])
	}
	runs := append(make([]run, 0, 4), run{1, 2}, run{3, 4})
	heldRuns := runs
	runs = reuseRuns(runs)
	if len(runs) != 0 || heldRuns[0] != (run{0xFFFF, 0xFFFF}) || heldRuns[1] != (run{0xFFFF, 0xFFFF}) {
		t.Errorf("held runs %v", heldRuns)
	}
}
