//go:build race

package gofastmsbin_test

// underRace says the tests were built with the race detector, which makes
// a program allocate more than it otherwise would.
const underRace = true
