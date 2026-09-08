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
	snap      snapshot.Snapshot
	err       error
	focused   []string
	focusedWS []string
}

func (f *fakeFetcher) Snapshot() (snapshot.Snapshot, error) { return f.snap, f.err }
func (f *fakeFetcher) FocusAgent(id string) error {
	f.focused = append(f.focused, id)
	return nil
}

func (f *fakeFetcher) FocusWorkspace(id string) error {
	f.focusedWS = append(f.focusedWS, id)
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

// countingFetcher records how many times the poll loop shells out.
type countingFetcher struct{ calls int }

func (c *countingFetcher) Snapshot() (snapshot.Snapshot, error) {
	c.calls++
	return snapshot.Snapshot{}, nil
}
func (c *countingFetcher) FocusAgent(string) error     { return nil }
func (c *countingFetcher) FocusWorkspace(string) error { return nil }

// TestRefreshKeepsExactlyOnePollChain drives the model the way bubbletea's
// event loop does — run every pending cmd, feed the messages back — and counts
// Snapshot calls. A steady chain costs one fetch per two rounds (snapshotMsg →
// tick → tickMsg → fetch), so a second chain armed by "r" shows up as roughly
// double the subprocesses.
func TestRefreshKeepsExactlyOnePollChain(t *testing.T) {
	f := &countingFetcher{}
	m := New(f, mapResolver{}, group.Options{}, time.Millisecond)

	var pending []tea.Cmd
	push := func(c tea.Cmd) {
		if c != nil {
			pending = append(pending, c)
		}
	}
	step := func(msg tea.Msg) {
		var c tea.Cmd
		m, c = m.Update(msg)
		push(c)
	}

	push(m.fetchCmd()) // the fetch Init arms
	// The user hits "r" while that first fetch is still in flight.
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	const rounds = 10
	for i := 0; i < rounds; i++ {
		batch := pending
		pending = nil
		for _, c := range batch {
			if msg := c(); msg != nil {
				step(msg)
			}
		}
	}
	if want := rounds/2 + 2; f.calls > want {
		t.Fatalf("Snapshot called %d times in %d rounds, want at most %d: the poll chain multiplied", f.calls, rounds, want)
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

// oneAgentSnapshot has one agent pane, one non-agent pane and one pasture
// pane, so a fetch exercises Build's filtering as well as its grouping.
func oneAgentSnapshot() snapshot.Snapshot {
	return snapshot.Snapshot{
		Panes: []snapshot.Pane{
			{PaneID: "w1:p1", WorkspaceID: "w1", Cwd: "/w/a", Agent: "claude", AgentStatus: "working", Title: "hello"},
			{PaneID: "w1:p2", WorkspaceID: "w1", Cwd: "/w/a", Title: "not an agent"},
			{PaneID: "w1:p3", WorkspaceID: "w1", Cwd: "/w/a", Agent: "claude", Title: "pasture itself", Tokens: map[string]string{"pasture": "1"}},
		},
		Workspaces:    []snapshot.Workspace{{WorkspaceID: "w1", Number: 1}},
		FocusedPaneID: "w1:p1",
	}
}

func TestInitFetchesTheFirstSnapshotThroughTheFetcher(t *testing.T) {
	f := &fakeFetcher{snap: oneAgentSnapshot()}
	m := New(f, mapResolver{"/w/a": {Root: "/r/a"}}, group.Options{SelfToken: "pasture"}, time.Second)
	batch, ok := m.Init()().(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init returned %T, want tea.BatchMsg", m.Init()())
	}
	var got snapshotMsg
	found := false
	for _, c := range batch {
		if s, ok := c().(snapshotMsg); ok {
			got, found = s, true
		}
	}
	if !found {
		t.Fatal("Init never fetched a snapshot")
	}
	if got.err != nil {
		t.Fatalf("err = %v", got.err)
	}
	if len(got.groups) != 1 || got.groups[0].Key != "/r/a" || got.groups[0].Label != "a" {
		t.Fatalf("groups = %+v, want one group keyed by the resolver's root", got.groups)
	}
	rows := got.groups[0].Rows
	if len(rows) != 1 || rows[0].PaneID != "w1:p1" || rows[0].Title != "hello" || !rows[0].Focused {
		t.Fatalf("rows = %+v, want only the agent pane, marked focused", rows)
	}
}

func TestPollChainAlternatesSnapshotAndTick(t *testing.T) {
	f := &fakeFetcher{snap: oneAgentSnapshot()}
	m := New(f, mapResolver{"/w/a": {Root: "/r/a"}}, group.Options{}, time.Millisecond)
	m, cmd := m.Update(snapshotMsg{gen: m.gen, groups: twoGroups()})
	if cmd == nil {
		t.Fatal("a snapshot must re-arm the tick")
	}
	tick, ok := cmd().(tickMsg)
	if !ok {
		t.Fatalf("snapshot armed %T, want tickMsg", cmd())
	}
	if tick.gen != m.gen {
		t.Fatalf("tick gen = %d, model gen = %d", tick.gen, m.gen)
	}
	m, cmd = m.Update(tick)
	if cmd == nil {
		t.Fatal("a tick must fetch")
	}
	snap, ok := cmd().(snapshotMsg)
	if !ok {
		t.Fatalf("tick armed %T, want snapshotMsg", cmd())
	}
	if snap.gen != m.gen {
		t.Fatalf("snapshot gen = %d, model gen = %d", snap.gen, m.gen)
	}
	if len(snap.groups) != 1 || snap.groups[0].Key != "/r/a" {
		t.Fatalf("groups = %+v", snap.groups)
	}
}

func TestFailedPollStillArmsTheTick(t *testing.T) {
	m := loaded(&fakeFetcher{})
	_, cmd := m.Update(snapshotMsg{gen: m.gen, err: errors.New("socket down")})
	if cmd == nil {
		t.Fatal("a failed poll must still re-arm the tick, or polling stops forever")
	}
}

func TestRefreshRetiresThePreviousPollChain(t *testing.T) {
	f := &fakeFetcher{}
	m := loaded(f) // a tick for generation `old` is pending
	old := m.gen

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd == nil {
		t.Fatal("r must fetch")
	}
	if m.gen == old {
		t.Fatal("r must retire the pending chain")
	}
	if _, c := m.Update(tickMsg{gen: old}); c != nil {
		t.Fatal("the retired chain's tick must be dropped, not re-armed")
	}

	first := m.gen
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if _, c := m.Update(tickMsg{gen: first}); c != nil {
		t.Fatal("two r presses left two live poll chains")
	}
	if _, c := m.Update(tickMsg{gen: m.gen}); c == nil {
		t.Fatal("the current chain must keep polling")
	}
}

func TestStaleSnapshotIsDroppedWithoutClobberingState(t *testing.T) {
	m := loaded(&fakeFetcher{})
	stale := m.gen
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})

	fresh, cmd := m.Update(snapshotMsg{gen: stale, groups: twoGroups()[:1]})
	if cmd != nil {
		t.Fatal("a stale snapshot must not arm a second chain")
	}
	if len(fresh.groups) != 2 {
		t.Fatalf("stale snapshot clobbered fresh groups: %d", len(fresh.groups))
	}
	broken, _ := m.Update(snapshotMsg{gen: stale, err: errors.New("stale")})
	if broken.disconnected {
		t.Fatal("a stale error must not mark the model disconnected")
	}
}

func TestClickUsesTheScrollOffset(t *testing.T) {
	f := &fakeFetcher{}
	m := loaded(f)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 3}) // 2 rows visible
	for i := 0; i < 4; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // scrolls to lines 3..4
	}
	// screen row 1 = header "b", screen row 2 = row "three"
	_, cmd := m.Update(tea.MouseMsg{Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	drain(cmd)
	if len(f.focused) != 1 || f.focused[0] != "w2:p1" {
		t.Fatalf("focused = %v", f.focused)
	}
	// screen row 3 is past the fold even though line 3+offset exists
	_, cmd = m.Update(tea.MouseMsg{Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	drain(cmd)
	if len(f.focused) != 1 {
		t.Fatalf("a click past the fold was handled: %v", f.focused)
	}
}

func TestWheelScrollsTheViewport(t *testing.T) {
	f := &fakeFetcher{}
	m := loaded(f)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 3})
	if m.offset != 0 {
		t.Fatalf("offset = %d, want 0", m.offset)
	}
	m, _ = m.Update(tea.MouseMsg{Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if m.offset != 1 {
		t.Fatalf("offset = %d after wheel down, want 1", m.offset)
	}
	m, _ = m.Update(tea.MouseMsg{Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if m.offset != 0 {
		t.Fatalf("offset = %d after wheel up, want 0", m.offset)
	}
	if len(f.focused) != 0 {
		t.Fatalf("the wheel must not focus a pane: %v", f.focused)
	}
}

func TestNonPressMouseEventsAreIgnored(t *testing.T) {
	f := &fakeFetcher{}
	m := loaded(f)
	for _, ev := range []tea.MouseMsg{
		{Y: 2, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft},
		{Y: 2, Action: tea.MouseActionMotion, Button: tea.MouseButtonNone},
		{Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonRight},
	} {
		_, cmd := m.Update(ev)
		drain(cmd)
	}
	if len(f.focused) != 0 {
		t.Fatalf("focused = %v", f.focused)
	}
}

func TestQuitKeys(t *testing.T) {
	m := loaded(&fakeFetcher{})
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("q")}, {Type: tea.KeyCtrlC}} {
		_, cmd := m.Update(k)
		if cmd == nil {
			t.Fatalf("%v did not quit", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%v returned %T, want tea.QuitMsg", k, cmd())
		}
	}
}

func TestSpaceTogglesHeadersOnly(t *testing.T) {
	f := &fakeFetcher{}
	m := loaded(f)
	space := tea.KeyMsg{Type: tea.KeySpace}

	m, cmd := m.Update(space) // cursor 0 is the header "a"
	drain(cmd)
	if !m.collapsed["/r/a"] {
		t.Fatal("space on a header must collapse it")
	}
	m, _ = m.Update(space) // expand again
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, cmd = m.Update(space) // cursor 1 is a row
	drain(cmd)
	if len(f.focused) != 0 {
		t.Fatalf("space on a row must not focus: %v", f.focused)
	}
	if m.collapsed["/r/a"] {
		t.Fatal("space on a row must not collapse its group")
	}
}

func TestCursorStopsAtTheTop(t *testing.T) {
	m := loaded(&fakeFetcher{})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", m.cursor)
	}
}

func TestEnterOnAWorkspaceRowFocusesTheWorkspace(t *testing.T) {
	f := &fakeFetcher{}
	m := New(f, mapResolver{}, group.Options{}, time.Second)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 20})
	m, _ = m.Update(snapshotMsg{gen: m.gen, groups: []group.Group{{
		Key: "/r/a", Label: "a",
		Rows: []group.Row{{Kind: group.RowWorkspace, WorkspaceID: "w2", Title: "bare"}},
	}}})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // cursor onto the row
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(cmd)
	if len(f.focusedWS) != 1 || f.focusedWS[0] != "w2" {
		t.Fatalf("focusedWS = %v", f.focusedWS)
	}
	if len(f.focused) != 0 {
		t.Fatalf("must not focus a pane: %v", f.focused)
	}
}

func TestEnterOnAnAgentRowStillFocusesThePane(t *testing.T) {
	f := &fakeFetcher{}
	m := New(f, mapResolver{}, group.Options{}, time.Second)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 20})
	m, _ = m.Update(snapshotMsg{gen: m.gen, groups: []group.Group{{
		Key: "/r/a", Label: "a",
		Rows: []group.Row{{Kind: group.RowAgent, PaneID: "w1:p1", Title: "one"}},
	}}})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(cmd)
	if len(f.focused) != 1 || f.focused[0] != "w1:p1" {
		t.Fatalf("focused = %v", f.focused)
	}
	if len(f.focusedWS) != 0 {
		t.Fatalf("must not focus a workspace: %v", f.focusedWS)
	}
}

// A group holding nothing but one bare workspace prints its name twice, on the
// header and on the row, and the header is the more obvious click target.
// Collapsing there hides the single duplicate line and looks like the click did
// nothing, which is what "clicking a workspace does not go there" turned out to
// be. The header navigates instead.
func TestHeaderOfAWorkspaceOnlyGroupFocusesTheWorkspace(t *testing.T) {
	f := &fakeFetcher{}
	m := New(f, mapResolver{}, group.Options{}, time.Second)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 20})
	m, _ = m.Update(snapshotMsg{gen: m.gen, groups: []group.Group{{
		Key: "/r/a", Label: "polala",
		Rows: []group.Row{{Kind: group.RowWorkspace, WorkspaceID: "w3", Title: "polala"}},
	}}})
	// The cursor starts on the header.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(cmd)
	if len(f.focusedWS) != 1 || f.focusedWS[0] != "w3" {
		t.Fatalf("focusedWS = %v", f.focusedWS)
	}
	if next.collapsed["/r/a"] {
		t.Fatal("the group must not collapse")
	}
}

// A header with agents under it still collapses; navigating there would take
// away the only way to fold a busy repository.
func TestHeaderWithAgentsStillCollapses(t *testing.T) {
	f := &fakeFetcher{}
	m := New(f, mapResolver{}, group.Options{}, time.Second)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 20})
	m, _ = m.Update(snapshotMsg{gen: m.gen, groups: []group.Group{{
		Key: "/r/a", Label: "a",
		Rows: []group.Row{
			{Kind: group.RowAgent, PaneID: "w1:p1", Title: "one"},
			{Kind: group.RowWorkspace, WorkspaceID: "w9", Title: "bare"},
		},
	}}})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(cmd)
	if !next.collapsed["/r/a"] {
		t.Fatal("the group should have collapsed")
	}
	if len(f.focusedWS) != 0 || len(f.focused) != 0 {
		t.Fatalf("nothing should be focused: ws=%v panes=%v", f.focusedWS, f.focused)
	}
}
