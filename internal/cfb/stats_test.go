package cfb_test

import (
	"crypto/sha256"
	"runtime"
)

// runtimeStats is the number of bytes allocated so far.
type runtimeStats struct{ bytes uint64 }

func (s *runtimeStats) read() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	s.bytes = m.TotalAlloc
}

func sha(b []byte) [32]byte { return sha256.Sum256(b) }
