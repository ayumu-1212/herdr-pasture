// Package ui is the bubbletea program shown inside the docked pane.
package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ayumu-1212/herdr-pasture/internal/group"
	"github.com/ayumu-1212/herdr-pasture/internal/mobile"
	"github.com/ayumu-1212/herdr-pasture/internal/snapshot"
)

// Fetcher is the slice of herdr.Client the UI depends on.
type Fetcher interface {
	Snapshot() (snapshot.Snapshot, error)
	FocusAgent(paneID string) error
	FocusWorkspace(workspaceID string) error
	FocusTab(tabID string) error
	Zoom(paneID string, on bool) error
}

// Store shares the folded-group set between pasture panes. Load errors leave
// the model's current state alone.
type Store interface {
	Load() (map[string]bool, error)
	Save(map[string]bool) error
}

// Model is the bubbletea model. Copy semantics: Update returns a new value.
type Model struct {
	fetch    Fetcher
	resolver group.Resolver
	opt      group.Options

	// interval is the delay between a poll's result and the next poll. The
	// tick is armed when a snapshot arrives rather than when one is
	// requested, so the effective period is interval + fetch duration. That
	// keeps a slow herdr from queueing overlapping subprocesses.
	interval time.Duration

	// gen identifies the live poll chain. Every fetch and tick carries the
	// generation it was armed in, and Update drops messages from any other
	// generation, so a manual refresh retires the chain already in flight
	// instead of running a second one alongside it forever.
	gen int

	groups       []group.Group
	collapsed    map[string]bool
	store        Store // nil: folds stay private to this pane
	cursor       int
	offset       int // index of the first list line drawn under the title bar
	width        int
	height       int
	disconnected bool

	// phone is the sidebar mode for a dock looked at from a phone; see
	// WithMobile. The zero value is off.
	phone phoneState
}

// phoneState is what the dock knows about being looked at on a phone. On a
// phone the dock is a sidebar: zoomed is open, another pane zoomed is closed.
type phoneState struct {
	selfPane, selfTab string
	threshold         int
	marks             mobile.Marks

	seen   bool // at least one snapshot showed this dock's tab
	phone  bool // this dock's tab is phone-sized
	active bool // phone, and this dock's tab has the focus
	last   snapshot.Snapshot
}

func (p phoneState) enabled() bool {
	return p.selfPane != "" && p.selfTab != "" && p.threshold > 0 && p.marks.Dir != ""
}

// line is one screen row below the title bar.
type line struct {
	header bool
	group  int // index into groups
	row    int // index into groups[group].Rows; -1 for headers
}

type snapshotMsg struct {
	gen    int
	groups []group.Group
	snap   snapshot.Snapshot
	err    error
}

type tickMsg struct{ gen int }

// New builds a model that polls fetch every interval.
func New(f Fetcher, r group.Resolver, opt group.Options, interval time.Duration) Model {
	return Model{
		fetch:     f,
		resolver:  r,
		opt:       opt,
		interval:  interval,
		collapsed: map[string]bool{},
	}
}

// WithStore makes the model read and write its folded groups through s, so
// panes in other workspaces stay in step.
func (m Model) WithStore(s Store) Model {
	m.store = s
	m.syncCollapsed()
	return m
}

// WithMobile turns on the phone sidebar for the dock running in selfPane of
// selfTab: when that tab is no wider than threshold columns, the dock opens by
// zooming itself and closes by zooming the row it is asked to go to. Tabs it
// zooms are recorded in marks so it can unzoom them once a desktop looks again.
// An empty id or a threshold of 0 leaves it off.
func (m Model) WithMobile(selfPane, selfTab string, threshold int, marks mobile.Marks) Model {
	m.phone = phoneState{selfPane: selfPane, selfTab: selfTab, threshold: threshold, marks: marks}
	return m
}

// syncCollapsed adopts the shared folded set, if there is one and it is readable.
func (m *Model) syncCollapsed() {
	if m.store == nil {
		return
	}
	if c, err := m.store.Load(); err == nil {
		m.collapsed = c
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetchCmd(), tea.SetWindowTitle("pasture"))
}

func (m Model) fetchCmd() tea.Cmd {
	fetch, resolver, opt, gen := m.fetch, m.resolver, m.opt, m.gen
	return func() tea.Msg {
		s, err := fetch.Snapshot()
		if err != nil {
			return snapshotMsg{gen: gen, err: err}
		}
		return snapshotMsg{gen: gen, groups: group.Build(s, resolver, opt), snap: s}
	}
}

func (m Model) tickCmd() tea.Cmd {
	gen := m.gen
	return tea.Tick(m.interval, func(time.Time) tea.Msg { return tickMsg{gen: gen} })
}

// Update advances the model. It returns the concrete Model rather than
// tea.Model so tests keep the type; Task 8 wraps it for tea.NewProgram.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampCursor()
		return m, nil
	case snapshotMsg:
		if msg.gen != m.gen {
			return m, nil // a retired chain's result: drop it, don't re-arm
		}
		var phoneCmd tea.Cmd
		if msg.err != nil {
			m.disconnected = true
		} else {
			m.disconnected = false
			m.groups = msg.groups
			phoneCmd = m.observe(msg.snap)
		}
		m.syncCollapsed() // pick up folds made in another pane
		m.clampCursor()
		return m, tea.Batch(phoneCmd, m.tickCmd())
	case tickMsg:
		if msg.gen != m.gen {
			return m, nil // a retired chain's tick: let it die here
		}
		return m, m.fetchCmd()
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.cursor--
		m.clampCursor()
	case "down", "j":
		m.cursor++
		m.clampCursor()
	case "enter":
		return m.activate(m.cursor)
	case " ":
		if ls := m.lines(); m.cursor < len(ls) && ls[m.cursor].header {
			return m.activate(m.cursor)
		}
	case "r":
		m.gen++ // retire the tick already pending from the last snapshot
		return m, m.fetchCmd()
	}
	return m, nil
}

func (m Model) handleMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scroll(-1)
		return m, nil
	case tea.MouseButtonWheelDown:
		m.scroll(1)
		return m, nil
	case tea.MouseButtonLeft:
		if msg.Y < 1 {
			return m, nil // row 0 is the title bar
		}
		screen := msg.Y - 1
		if screen >= m.visibleRows() {
			return m, nil // below the last drawn row
		}
		idx := screen + m.offset
		if idx >= len(m.lines()) {
			return m, nil
		}
		m.cursor = idx
		return m.activate(idx)
	}
	return m, nil
}

// activate toggles a header, focuses a row's pane, or focuses a workspace
// that has no agent pane of its own.
func (m Model) activate(idx int) (Model, tea.Cmd) {
	ls := m.lines()
	if idx < 0 || idx >= len(ls) {
		return m, nil
	}
	l := ls[idx]
	g := m.groups[l.group]
	if l.header {
		// A group holding nothing but one bare workspace prints its name twice,
		// once as the header and once as the row, and the header is the more
		// obvious thing to click. Collapsing it would only hide that one
		// duplicate line, so the header goes to the workspace instead.
		if row, ok := soleWorkspace(g); ok {
			return m, m.goCmd(row)
		}
		// Re-read first so a fold made elsewhere since the last poll is not
		// overwritten by this pane's stale copy.
		m.syncCollapsed()
		m.collapsed[g.Key] = !m.collapsed[g.Key]
		if m.store != nil {
			_ = m.store.Save(m.collapsed) // best effort: the fold still applies here
		}
		m.clampCursor()
		return m, nil
	}
	return m, m.goCmd(g.Rows[l.row])
}

// goCmd takes the user to row. On a phone it first zooms the pane the row
// leads to, which closes the sidebar. The zoom comes before the move on
// purpose: the destination's dock opens itself when its tab gains the focus
// unzoomed, and must find the tab already zoomed when it next looks.
func (m Model) goCmd(row group.Row) tea.Cmd {
	if !m.phone.enabled() || !m.phone.phone {
		return focusCmd(m.fetch, row)
	}
	tabID, paneID := row.TabID, row.PaneID
	if row.Kind == group.RowWorkspace {
		tabID, paneID = m.workspaceTarget(row.WorkspaceID)
	}
	fetch, marks := m.fetch, m.phone.marks
	return func() tea.Msg {
		if paneID != "" {
			if tabID != "" {
				_ = marks.Set(tabID, paneID)
			}
			_ = fetch.Zoom(paneID, true)
		}
		return focusCmd(fetch, row)()
	}
}

// workspaceTarget picks the pane a workspace row zooms on a phone: in the
// workspace's active tab, the focused pane unless that is a dock, else the
// largest pane that is not one. Empty strings when the last snapshot cannot say.
func (m Model) workspaceTarget(workspaceID string) (tabID, paneID string) {
	s := m.phone.last
	for _, w := range s.Workspaces {
		if w.WorkspaceID == workspaceID {
			tabID = w.ActiveTabID
		}
	}
	l, ok := s.Layout(tabID)
	if !ok {
		return "", ""
	}
	dockID := ""
	for _, p := range s.Panes {
		if p.TabID == tabID && m.opt.SelfToken != "" && p.Tokens[m.opt.SelfToken] != "" {
			dockID = p.PaneID
		}
	}
	paneID, ok = mobile.ReturnPane(l, dockID, l.FocusedPaneID)
	if !ok {
		return "", ""
	}
	return tabID, paneID
}

// observe reads this dock's own tab out of a fresh snapshot and reacts to the
// moment things change, never to a state that merely persists: a user who
// unzooms by hand on a phone is not overruled a second later.
//
//   - Its tab gains the focus on a phone while unzoomed: open the sidebar.
//   - The tab stops being phone-sized: undo the zoom pasture made, if any. The
//     first snapshot counts as coming from a phone, so a zoom left behind by an
//     earlier dock process is undone too.
func (m *Model) observe(s snapshot.Snapshot) tea.Cmd {
	p := &m.phone
	if !p.enabled() {
		return nil
	}
	l, ok := s.Layout(p.selfTab)
	if !ok {
		return nil // no geometry for our tab: decide nothing, remember nothing
	}
	p.last = s
	wasPhone, wasActive := p.phone || !p.seen, p.active
	p.seen = true
	p.phone = mobile.Phone(l, p.threshold)
	p.active = p.phone && s.FocusedTabID == p.selfTab
	fetch, marks, tabID, self := m.fetch, p.marks, p.selfTab, p.selfPane
	switch {
	case p.active && !wasActive && !l.Zoomed:
		back := ""
		if l.FocusedPaneID != self {
			back = l.FocusedPaneID
		}
		return func() tea.Msg {
			if back == "" {
				back, _ = marks.Get(tabID)
			}
			_ = marks.Set(tabID, back)
			_ = fetch.Zoom(self, true)
			return nil
		}
	case wasPhone && !p.phone:
		if _, marked := marks.Get(tabID); !marked {
			return nil
		}
		zoomed, focused := l.Zoomed, l.FocusedPaneID
		if focused == "" {
			focused = self
		}
		return func() tea.Msg {
			if zoomed {
				if err := fetch.Zoom(focused, false); err != nil {
					// Keep the mark: a dock started later undoes the zoom
					// on its first snapshot.
					return nil
				}
			}
			_ = marks.Clear(tabID)
			return nil
		}
	}
	return nil
}

// soleWorkspace reports the row of a group whose only content is one workspace
// with no agent running in it. Such a group shows its name on both the header
// and the row, so both should go to the workspace.
func soleWorkspace(g group.Group) (group.Row, bool) {
	if len(g.Rows) == 1 && g.Rows[0].Kind == group.RowWorkspace {
		return g.Rows[0], true
	}
	return group.Row{}, false
}

// focusCmd focuses whatever a row stands for. The row is captured by value:
// the command runs on its own goroutine and must not read the model.
func focusCmd(fetch Fetcher, row group.Row) tea.Cmd {
	return func() tea.Msg {
		// A row that has vanished simply disappears on the next poll, so none
		// of these errors is worth surfacing.
		if row.Kind == group.RowWorkspace {
			_ = fetch.FocusWorkspace(row.WorkspaceID)
			return nil
		}
		// The tab first, then the pane inside it. Focusing an agent alone moves
		// the server's focus but not the view of the client looking at the
		// session, so a pane in another workspace would light up while the user
		// went on staring at the workspace they were already on.
		if row.TabID != "" {
			_ = fetch.FocusTab(row.TabID)
		}
		_ = fetch.FocusAgent(row.PaneID)
		return nil
	}
}

// lines flattens groups into screen rows, skipping collapsed rows.
func (m Model) lines() []line {
	var out []line
	for gi, g := range m.groups {
		out = append(out, line{header: true, group: gi, row: -1})
		if m.collapsed[g.Key] {
			continue
		}
		for ri := range g.Rows {
			out = append(out, line{group: gi, row: ri})
		}
	}
	return out
}

// visibleRows is how many list lines fit under the title bar. Before the
// first WindowSizeMsg the pane size is unknown, so everything is drawn.
func (m Model) visibleRows() int {
	if m.height <= 0 {
		return len(m.lines())
	}
	if m.height < 2 {
		return 0
	}
	return m.height - 1
}

// scroll moves the viewport by delta lines, dragging the cursor along so it
// never leaves the visible window.
func (m *Model) scroll(delta int) {
	n := len(m.lines())
	vis := m.visibleRows()
	if vis <= 0 || n <= vis {
		return
	}
	m.offset += delta
	if m.offset > n-vis {
		m.offset = n - vis
	}
	if m.offset < 0 {
		m.offset = 0
	}
	if m.cursor < m.offset {
		m.cursor = m.offset
	}
	if m.cursor > m.offset+vis-1 {
		m.cursor = m.offset + vis - 1
	}
}

// clampCursor keeps the cursor on an existing line and scrolls the viewport
// so the cursor stays drawn.
func (m *Model) clampCursor() {
	n := len(m.lines())
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	vis := m.visibleRows()
	if vis <= 0 {
		m.offset = 0
		return
	}
	if maxOffset := n - vis; m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor > m.offset+vis-1 {
		m.offset = m.cursor - vis + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}
