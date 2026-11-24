//go:build xlsscratchpoison

package xls

// With the xlsscratchpoison tag, a buffer that is written again and
// again (a string being read, a value being formatted) is overwritten
// with '#' to its whole capacity each time it is taken up: see reuse. A
// reader that keeps a view of what the buffer held then reads '#', where
// it would otherwise read whatever was written there next, which may be
// the same bytes. The tests are run with it in CI.
const poison = true

func fill(b []byte) {
	for i := range b {
		b[i] = '#'
	}
}

func fillRuns(r []run) {
	for i := range r {
		r[i] = run{0xFFFF, 0xFFFF}
	}
}
