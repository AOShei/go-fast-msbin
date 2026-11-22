package msbintest

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Entry is one file of the corpus, as testdata/corpus.json lists it.
type Entry struct {
	Name    string `json:"name"`
	Format  string `json:"format"`
	Set     string `json:"set"` // dev or heldout
	Path    string `json:"path"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Licence string `json:"licence"`
}

// HeldOut reports whether the file is one of those that are only scored:
// a test may count what the library makes of it, and nobody looks at the
// file or at its output to see why.
func (e Entry) HeldOut() bool { return e.Set != "dev" }

// Corpus reads the manifest in the given testdata directory and returns
// the entries whose files are present, with Path made a path that can be
// opened. It returns nothing where the corpus was not downloaded.
func Corpus(testdata string) ([]Entry, error) {
	raw, err := os.ReadFile(filepath.Join(testdata, "corpus.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var all []Entry
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	var present []Entry
	for _, e := range all {
		e.Path = filepath.Join(testdata, filepath.FromSlash(e.Path))
		if info, err := os.Stat(e.Path); err == nil && info.Size() == e.Size {
			present = append(present, e)
		}
	}
	return present, nil
}
