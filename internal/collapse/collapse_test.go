package collapse

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	s := NewFileStore(filepath.Join(t.TempDir(), "nested"))
	if got, err := s.Load(); err != nil || len(got) != 0 {
		t.Fatalf("missing file: %v, %v", got, err)
	}
	if err := s.Save(map[string]bool{"/a": true, "/b": false, "/c": true}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || len(got) != 2 || !got["/a"] || !got["/c"] {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestLoadCorruptFileErrors(t *testing.T) {
	s := NewFileStore(t.TempDir())
	if err := os.WriteFile(s.Path, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("want an error for a corrupt file")
	}
}
