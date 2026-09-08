package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ayumu-1212/herdr-pasture/internal/group"
)

// sized returns a loaded model resized to w x h.
func sized(t *testing.T, w, h int) Model {
	t.Helper()
	m := loaded(&fakeFetcher{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

func viewLines(m Model) []string { return strings.Split(m.View(), "\n") }

func TestViewNeverExceedsPaneHeightAndKeepsTitleFirst(t *testing.T) {
	// 5 list lines in a 3-row pane: the view must still be exactly 3 lines,
	// otherwise bubbletea's renderer drops the top line (the title bar) and
	// every mouse Y is off by one.
	m := sized(t, 30, 3)
	ls := viewLines(m)
	if len(ls) != 3 {
		t.Fatalf("view has %d lines, want 3: %q", len(ls), m.View())
	}
	if !strings.Contains(ls[0], "herdr-pasture") {
		t.Fatalf("line 0 = %q, want the title bar", ls[0])
	}
}

func TestViewScrollsToKeepTheCursorVisible(t *testing.T) {
	m := sized(t, 30, 3)
	for i := 0; i < 4; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // cursor -> line 4 ("three")
	}
	ls := viewLines(m)
	if len(ls) != 3 {
		t.Fatalf("view has %d lines, want 3: %q", len(ls), m.View())
	}
	if !strings.Contains(ls[0], "herdr-pasture") {
		t.Fatalf("line 0 = %q, want the title bar", ls[0])
	}
	body := strings.Join(ls[1:], "\n")
	if !strings.Contains(body, "three") {
		t.Fatalf("cursor row scrolled out of view: %q", body)
	}
	if strings.Contains(body, "one") {
		t.Fatalf("view did not scroll, still showing the first rows: %q", body)
	}
}

func TestViewRendersDisconnectedHeader(t *testing.T) {
	m := sized(t, 40, 20)
	if strings.Contains(m.View(), "disconnected") {
		t.Fatal("connected model must not say disconnected")
	}
	m, _ = m.Update(snapshotMsg{err: errors.New("socket down")})
	ls := viewLines(m)
	if !strings.Contains(ls[0], "herdr-pasture") || !strings.Contains(ls[0], "disconnected") {
		t.Fatalf("header = %q", ls[0])
	}
}

func TestViewRendersIconsBranchAndFocus(t *testing.T) {
	m := New(&fakeFetcher{}, mapResolver{}, group.Options{}, time.Second)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m, _ = m.Update(snapshotMsg{groups: []group.Group{{
		Key: "/r/a", Label: "a",
		Rows: []group.Row{
			{PaneID: "w1:p1", Title: "working row", Status: "working"},
			{PaneID: "w1:p2", Title: "odd row", Status: "no-such-status"},
			{PaneID: "w1:p3", Title: "focused row", Status: "done", Branch: "feat/x", Focused: true},
		},
	}}})
	v := m.View()
	if !strings.Contains(v, statusIcons["working"]) {
		t.Fatalf("missing working icon: %q", v)
	}
	if !strings.Contains(v, statusIcons["unknown"]) {
		t.Fatalf("unknown status must fall back to the %q icon: %q", statusIcons["unknown"], v)
	}
	if !strings.Contains(v, "[feat/x]") {
		t.Fatalf("missing branch prefix: %q", v)
	}
	if !strings.Contains(v, "focused row") {
		t.Fatalf("focus styling must not eat the label: %q", v)
	}
}

func TestViewLinesFitTheWidth(t *testing.T) {
	m := New(&fakeFetcher{}, mapResolver{}, group.Options{}, time.Second)
	const width = 24
	m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 20})
	m, _ = m.Update(snapshotMsg{groups: []group.Group{{
		Key:   "/r/very-long-repository-name",
		Label: "very-long-repository-name",
		Rows: []group.Row{
			{PaneID: "w1:p1", Title: "a title far wider than the pane", Status: "idle"},
			{PaneID: "w1:p2", Title: "日本語のとても長いタイトル", Status: "done", Branch: "feature/long-branch"},
		},
	}}})
	for i, l := range viewLines(m) {
		if got := lipgloss.Width(l); got > width {
			t.Fatalf("line %d is %d cells wide, want <= %d: %q", i, got, width, l)
		}
	}
}

func TestViewRendersAWorkspaceRow(t *testing.T) {
	f := &fakeFetcher{}
	m := New(f, mapResolver{}, group.Options{}, time.Second)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
	m, _ = m.Update(snapshotMsg{gen: m.gen, groups: []group.Group{{
		Key: "/r/a", Label: "a",
		Rows: []group.Row{{Kind: group.RowWorkspace, WorkspaceID: "w2", Title: "bare"}},
	}}})
	out := m.View()
	if !strings.Contains(out, "bare") {
		t.Fatalf("workspace label missing: %q", out)
	}
	if !strings.Contains(out, workspaceIcon) {
		t.Fatalf("workspace icon missing: %q", out)
	}
}
