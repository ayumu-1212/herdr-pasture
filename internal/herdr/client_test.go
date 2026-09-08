package herdr

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// FakeRunner records argv and replies from a queue of canned outputs.
type FakeRunner struct {
	Calls   [][]string
	Outputs []string
	Err     error
}

func (f *FakeRunner) Run(args ...string) ([]byte, error) {
	f.Calls = append(f.Calls, args)
	if f.Err != nil {
		return nil, f.Err
	}
	if len(f.Outputs) == 0 {
		return []byte("{}"), nil
	}
	out := f.Outputs[0]
	f.Outputs = f.Outputs[1:]
	return []byte(out), nil
}

func TestSnapshotDecodes(t *testing.T) {
	r := &FakeRunner{Outputs: []string{`{"result":{"snapshot":{"focused_pane_id":"w1:p1","panes":[]}}}`}}
	s, err := New(r).Snapshot()
	if err != nil || s.FocusedPaneID != "w1:p1" {
		t.Fatalf("got %+v, %v", s, err)
	}
	if !reflect.DeepEqual(r.Calls[0], []string{"api", "snapshot"}) {
		t.Fatalf("argv = %v", r.Calls[0])
	}
}

func TestSnapshotPropagatesRunnerError(t *testing.T) {
	r := &FakeRunner{Err: errors.New("socket down")}
	if _, err := New(r).Snapshot(); err == nil {
		t.Fatal("expected error")
	}
}

func TestSplitBuildsArgvAndReturnsNewPane(t *testing.T) {
	r := &FakeRunner{Outputs: []string{`{"result":{"pane":{"pane_id":"w1:p7"}}}`}}
	id, err := New(r).Split("w1:p1", 0.25, "/work", map[string]string{"B": "2", "A": "1"})
	if err != nil || id != "w1:p7" {
		t.Fatalf("got %q, %v", id, err)
	}
	want := []string{"pane", "split", "w1:p1", "--direction", "right", "--ratio", "0.25", "--no-focus", "--cwd", "/work", "--env", "A=1", "--env", "B=2"}
	if !reflect.DeepEqual(r.Calls[0], want) {
		t.Fatalf("argv = %v", r.Calls[0])
	}
}

func TestSplitPropagatesDecodeError(t *testing.T) {
	r := &FakeRunner{Outputs: []string{`{}`}}
	if _, err := New(r).Split("w1:p1", 0.25, "", nil); err == nil {
		t.Fatal("expected error when response has no pane_id")
	}
}

func TestSplitOmitsEmptyCwd(t *testing.T) {
	r := &FakeRunner{Outputs: []string{`{"result":{"pane":{"pane_id":"w1:p7"}}}`}}
	New(r).Split("w1:p1", 0.3, "", nil)
	if strings.Contains(strings.Join(r.Calls[0], " "), "--cwd") {
		t.Fatalf("argv should not contain --cwd: %v", r.Calls[0])
	}
}

func TestSimpleCommandsArgv(t *testing.T) {
	r := &FakeRunner{}
	c := New(r)
	c.FocusAgent("w2:p3")
	c.Swap("w1:p7", "w1:p1")
	c.RunInPane("w1:p7", "/bin/pasture ui")
	c.Rename("w1:p7", "pasture")
	c.Close("w1:p7")
	c.ReportToken("w1:p7", "pasture", "4242")
	c.Layout("w1:p1")
	c.PaneList()
	want := [][]string{
		{"agent", "focus", "w2:p3"},
		{"pane", "swap", "--source-pane", "w1:p7", "--target-pane", "w1:p1"},
		{"pane", "run", "w1:p7", "/bin/pasture ui"},
		{"pane", "rename", "w1:p7", "pasture"},
		{"pane", "close", "w1:p7"},
		{"pane", "report-metadata", "w1:p7", "--source", "pasture", "--token", "pasture=4242"},
		{"pane", "layout", "--pane", "w1:p1"},
		{"pane", "list"},
	}
	if !reflect.DeepEqual(r.Calls, want) {
		t.Fatalf("argv:\n got %v\nwant %v", r.Calls, want)
	}
}
