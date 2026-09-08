package ui

import (
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ayumu-1212/herdr-pasture/internal/group"
	"github.com/ayumu-1212/herdr-pasture/internal/snapshot"
)

type fakeFetcher struct {
	snap    snapshot.Snapshot
	err     error
	focused []string
}

func (f *fakeFetcher) Snapshot() (snapshot.Snapshot, error) { return f.snap, f.err }
func (f *fakeFetcher) FocusAgent(id string) error {
	f.focused = append(f.focused, id)
	return nil
}

type mapResolver map[string]group.RepoInfo

func (m mapResolver) Resolve(cwd string) group.RepoInfo { return m[cwd] }

func twoGroups() []group.Group {
	return []group.Group{
		{Key: "/r/a", Label: "a", Rows: []group.Row{{PaneID: "w1:p1", Title: "one", Status: "idle"}, {PaneID: "w1:p2", Title: "two", Status: "working"}}},
		{Key: "/r/b", Label: "b", Rows: []group.Row{{PaneID: "w2:p1", Title: "three", Status: "done"}}},
	}
}

func loaded(f *fakeFetcher) Model {
	m := New(f, mapResolver{}, group.Options{}, time.Second)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 20})
	m, _ = m.Update(snapshotMsg{groups: twoGroups()})
	return m
}

// drain runs a returned tea.Cmd once so side effects (FocusAgent) happen.
func drain(cmd tea.Cmd) {
	if cmd != nil {
		cmd()
	}
}

func TestLinesFlattenGroupsAndRespectCollapse(t *testing.T) {
	m := loaded(&fakeFetcher{})
	if got := len(m.lines()); got != 5 {
		t.Fatalf("lines = %d, want 5 (2 headers + 3 rows)", got)
	}
	m.collapsed["/r/a"] = true
	ls := m.lines()
	if len(ls) != 3 || !ls[0].header || !ls[1].header || ls[2].header {
		t.Fatalf("collapsed lines = %+v", ls)
	}
}

func TestClickOnRowFocusesPane(t *testing.T) {
	f := &fakeFetcher{}
	m := loaded(f)
	// screen row 0 = title bar, 1 = header "a", 2 = row one, 3 = row two
	_, cmd := m.Update(tea.MouseMsg{Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	drain(cmd)
	if len(f.focused) != 1 || f.focused[0] != "w1:p2" {
		t.Fatalf("focused = %v", f.focused)
	}
}

func TestClickOnHeaderTogglesCollapse(t *testing.T) {
	f := &fakeFetcher{}
	m := loaded(f)
	m, cmd := m.Update(tea.MouseMsg{Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	drain(cmd)
	if !m.collapsed["/r/a"] || len(f.focused) != 0 {
		t.Fatalf("collapsed=%v focused=%v", m.collapsed, f.focused)
	}
	if len(m.lines()) != 3 {
		t.Fatalf("lines after collapse = %d", len(m.lines()))
	}
}

func TestClickOutsideListIsIgnored(t *testing.T) {
	f := &fakeFetcher{}
	m := loaded(f)
	_, cmd := m.Update(tea.MouseMsg{Y: 15, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	drain(cmd)
	_, cmd = m.Update(tea.MouseMsg{Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	drain(cmd)
	if len(f.focused) != 0 {
		t.Fatalf("focused = %v", f.focused)
	}
}

func TestKeyboardNavigationAndEnter(t *testing.T) {
	f := &fakeFetcher{}
	m := loaded(f)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // cursor on header "b"
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // row three
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // clamped
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(cmd)
	if len(f.focused) != 1 || f.focused[0] != "w2:p1" {
		t.Fatalf("focused = %v", f.focused)
	}
}

func TestSnapshotErrorMarksDisconnectedAndKeepsGroups(t *testing.T) {
	m := loaded(&fakeFetcher{})
	m, _ = m.Update(snapshotMsg{err: errors.New("socket down")})
	if !m.disconnected || len(m.groups) != 2 {
		t.Fatalf("disconnected=%v groups=%d", m.disconnected, len(m.groups))
	}
	m, _ = m.Update(snapshotMsg{groups: twoGroups()})
	if m.disconnected {
		t.Fatal("should reconnect")
	}
}

func TestCursorClampsWhenRowsDisappear(t *testing.T) {
	m := loaded(&fakeFetcher{})
	m.cursor = 4
	m, _ = m.Update(snapshotMsg{groups: twoGroups()[:1]})
	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want 2 (last line)", m.cursor)
	}
}

func TestTruncateHonorsDisplayWidth(t *testing.T) {
	if got := truncate("abcdefgh", 5); got != "abcd…" {
		t.Fatalf("got %q", got)
	}
	if got := truncate("日本語のタイトル", 7); got != "日本語…" {
		t.Fatalf("got %q", got)
	}
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("got %q", got)
	}
}
