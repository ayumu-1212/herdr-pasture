// Package collapse persists which sidebar groups are folded, so every pasture
// pane (one per tab, each its own process) shows the same state.
package collapse

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

// FileStore keeps the folded group keys in one JSON file. The file is tiny and
// rewritten whole; a torn read is impossible because Save renames a finished
// temp file over it.
type FileStore struct{ Path string }

// NewFileStore stores its state as collapsed.json under dir.
func NewFileStore(dir string) FileStore {
	return FileStore{Path: filepath.Join(dir, "collapsed.json")}
}

// Load returns the folded keys. A missing file is an empty set, not an error.
func (s FileStore) Load() (map[string]bool, error) {
	out := map[string]bool{}
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var keys []string
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, err
	}
	for _, k := range keys {
		out[k] = true
	}
	return out, nil
}

// Save writes the keys whose value is true.
func (s FileStore) Save(set map[string]bool) error {
	keys := make([]string, 0, len(set))
	for k, v := range set {
		if v {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	data, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), "collapsed-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		if werr != nil {
			return werr
		}
		return cerr
	}
	if err := os.Rename(tmp.Name(), s.Path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
