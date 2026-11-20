package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ayumu-1212/herdr-pasture/internal/group"
	"github.com/ayumu-1212/herdr-pasture/internal/mobile"
	"github.com/ayumu-1212/herdr-pasture/internal/snapshot"
)

// The dock under test runs in w1:p5 of tab w1:t1, beside the agent w1:p1.
// Workspace w2 has a dock w2:p9 and a shell w2:p1 in its active tab w2:t1.
const (
	selfPane = "w1:p5"
	selfTab  = "w1:t1"
	token    = "pasture"
)

type world struct {
	width    int    // terminal columns
	focusTab string // the tab holding the server's focus
	zoomed   bool   // w1:t1 is zoomed
	focused  string // w1:t1's focused pane
}

func (w world) snap() snapshot.Snapshot {
	area := snapshot.Rect{Width: w.width, Height: 28}
	half := w.width / 3
	focused := w.focused
	if focused == "" {
		focused = "w1:p1"
	}
	return snapshot.Snapshot{
		FocusedTabID: w.focusTab,
		Panes: []snapshot.Pane{
			{PaneID: selfPane, TabID: selfTab, WorkspaceID: "w1", Tokens: map[string]string{token: "1"}},
			{PaneID: "w1:p1", TabID: selfTab, WorkspaceID: "w1"},
			{PaneID: "w2:p9", TabID: "w2:t1", WorkspaceID: "w2", Tokens: map[string]string{token: "2"}},
			{PaneID: "w2:p1", TabID: "w2:t1", WorkspaceID: "w2"},
		},
		Workspaces: []snapshot.Workspace{
			{WorkspaceID: "w1", ActiveTabID: selfTab},
			{WorkspaceID: "w2", ActiveTabID: "w2:t1"},
		},
		Layouts: []snapshot.Layout{
			{TabID: selfTab, Area: area, Zoomed: w.zoomed, FocusedPaneID: focused, Panes: []snapshot.LayoutPane{
				{PaneID: selfPane, Rect: snapshot.Rect{Width: half, Height: 28}},
				{PaneID: "w1:p1", Rect: snapshot.Rect{X: half, Width: w.width - half, Height: 28}},
			}},
			{TabID: "w2:t1", Area: area, FocusedPaneID: "w2:p9", Panes: []snapshot.LayoutPane{
				{PaneID: "w2:p9", Rect: snapshot.Rect{Width: half, Height: 28}},
				{PaneID: "w2:p1", Rect: snapshot.Rect{X: half, Width: w.width - half, Height: 28}},
			}},
		},
	}
}

func phoneGroups() []group.Group {
	return []group.Group{
		{Key: "/r/a", Label: "a", Rows: []group.Row{{Kind: group.RowAgent, PaneID: "w1:p1", TabID: selfTab, Title: "one"}}},
		{Key: "/r/b", Label: "b", Rows: []group.Row{
			{Kind: group.RowAgent, PaneID: "w3:p1", TabID: "w3:t1", Title: "far"},
			{Kind: group.RowWorkspace, WorkspaceID: "w2", Title: "w2"},
		}},
	}
}

type phoneRig struct {
	t     *testing.T
	f     *fakeFetcher
	m     Model
	marks mobile.Marks
}

func newPhoneRig(t *testing.T) *phoneRig {
	f := &fakeFetcher{}
	marks := mobile.Marks{Dir: t.TempDir()}
	m := New(f, mapResolver{}, group.Options{SelfToken: token}, time.Second).
		WithMobile(selfPane, selfTab, 64, marks)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	return &phoneRig{t: t, f: f, m: m, marks: marks}
}

// poll feeds one snapshot and runs whatever the model asked for, except the
// next tick.
func (r *phoneRig) poll(w world) {
	r.t.Helper()
	var cmd tea.Cmd
	r.m, cmd = r.m.Update(snapshotMsg{gen: r.m.gen, groups: phoneGroups(), snap: w.snap()})
	runSkippingTicks(cmd)
}

// runSkippingTicks runs cmd and, for a batch, each command in it, without
// waiting out a tea.Tick.
func runSkippingTicks(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				runSkippingTicks(c)
			}
		}
	case <-time.After(100 * time.Millisecond):
		// a tick: let it go
	}
}

func (r *phoneRig) zooms() string { return strings.Join(r.f.zooms, " ") }

func TestPhoneOpensTheSidebarWhenItsTabGainsTheFocus(t *testing.T) {
	r := newPhoneRig(t)
	r.poll(world{width: 45, focusTab: "w2:t1"})
	if r.zooms() != "" {
		t.Fatalf("a dock whose tab is not focused must stay put: %v", r.zooms())
	}
	r.poll(world{width: 45, focusTab: selfTab})
	if r.zooms() != "w1:p5:on" {
		t.Fatalf("zooms = %q, want the dock zoomed", r.zooms())
	}
	if back, ok := r.marks.Get(selfTab); !ok || back != "w1:p1" {
		t.Fatalf("mark = %q, %v; want the tab marked with the pane to go back to", back, ok)
	}
	// Still focused, still on the phone: no second zoom, even if the user
	// unzoomed by hand in between.
	r.poll(world{width: 45, focusTab: selfTab})
	if r.zooms() != "w1:p5:on" {
		t.Fatalf("zooms = %q, want exactly one", r.zooms())
	}
}

func TestPhoneLeavesAnAlreadyZoomedTabAlone(t *testing.T) {
	r := newPhoneRig(t)
	r.poll(world{width: 45, focusTab: selfTab, zoomed: true})
	if r.zooms() != "" {
		t.Fatalf("zooms = %q; the agent the user picked must stay on screen", r.zooms())
	}
}

func TestDesktopNeverZooms(t *testing.T) {
	r := newPhoneRig(t)
	r.poll(world{width: 120, focusTab: selfTab})
	r.poll(world{width: 120, focusTab: selfTab})
	if r.zooms() != "" {
		t.Fatalf("zooms = %q", r.zooms())
	}
}

func TestBackOnTheDesktopUndoesTheZoomPastureMade(t *testing.T) {
	r := newPhoneRig(t)
	r.poll(world{width: 45, focusTab: selfTab}) // opens: zoom on, marked
	r.poll(world{width: 120, focusTab: selfTab, zoomed: true, focused: selfPane})
	if r.zooms() != "w1:p5:on w1:p5:off" {
		t.Fatalf("zooms = %q", r.zooms())
	}
	if _, ok := r.marks.Get(selfTab); ok {
		t.Fatal("the mark must be cleared once the zoom is undone")
	}
}

func TestBackOnTheDesktopLeavesTheUsersOwnZoom(t *testing.T) {
	r := newPhoneRig(t)
	r.poll(world{width: 45, focusTab: "w2:t1", zoomed: true}) // zoomed, but not by pasture
	r.poll(world{width: 120, focusTab: selfTab, zoomed: true})
	if r.zooms() != "" {
		t.Fatalf("zooms = %q; an unmarked zoom is the user's", r.zooms())
	}
}

func TestAFreshDockUndoesAZoomLeftBehind(t *testing.T) {
	r := newPhoneRig(t)
	if err := r.marks.Set(selfTab, "w1:p1"); err != nil {
		t.Fatal(err)
	}
	r.poll(world{width: 120, focusTab: selfTab, zoomed: true})
	if r.zooms() != "w1:p1:off" {
		t.Fatalf("zooms = %q", r.zooms())
	}
}

func TestPhoneRowZoomsBeforeMoving(t *testing.T) {
	r := newPhoneRig(t)
	r.poll(world{width: 45, focusTab: selfTab})
	r.f.zooms, r.f.order = nil, ""
	_, cmd := r.m.activate(3) // "far", in w3:t1
	drain(cmd)
	if r.zooms() != "w3:p1:on" {
		t.Fatalf("zooms = %q", r.zooms())
	}
	if r.f.order != "zoom,tab,agent" {
		t.Fatalf("order = %q, want the zoom before the move", r.f.order)
	}
	if back, ok := r.marks.Get("w3:t1"); !ok || back != "w3:p1" {
		t.Fatalf("mark = %q, %v", back, ok)
	}
}

func TestPhoneWorkspaceRowZoomsItsActiveTabsPane(t *testing.T) {
	r := newPhoneRig(t)
	r.poll(world{width: 45, focusTab: selfTab})
	r.f.zooms, r.f.order = nil, ""
	_, cmd := r.m.activate(4) // workspace w2, whose focused pane is its dock
	drain(cmd)
	if r.zooms() != "w2:p1:on" {
		t.Fatalf("zooms = %q, want the shell, never the dock", r.zooms())
	}
	if r.f.order != "zoom,workspace" {
		t.Fatalf("order = %q", r.f.order)
	}
}

func TestDesktopRowDoesNotZoom(t *testing.T) {
	r := newPhoneRig(t)
	r.poll(world{width: 120, focusTab: selfTab})
	_, cmd := r.m.activate(3)
	drain(cmd)
	if r.zooms() != "" || r.f.order != "tab,agent" {
		t.Fatalf("zooms = %q order = %q", r.zooms(), r.f.order)
	}
}

func TestPhoneModeNeedsItsOwnIDs(t *testing.T) {
	f := &fakeFetcher{}
	m := New(f, mapResolver{}, group.Options{SelfToken: token}, time.Second).
		WithMobile("", "", 64, mobile.Marks{Dir: t.TempDir()})
	m, cmd := m.Update(snapshotMsg{gen: m.gen, groups: phoneGroups(), snap: world{width: 45, focusTab: selfTab}.snap()})
	runSkippingTicks(cmd)
	_, cmd = m.activate(3)
	drain(cmd)
	if len(f.zooms) != 0 {
		t.Fatalf("outside herdr there is no tab to zoom: %v", f.zooms)
	}
}
