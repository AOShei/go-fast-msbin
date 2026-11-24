//go:build !xlsscratchpoison

package xls

// poison is set by the xlsscratchpoison tag: see poison.go.
const poison = false

func fill(b []byte)    {}
func fillRuns(r []run) {}
