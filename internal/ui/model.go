// Package ui is the bubbletea program shown inside the docked pane.
package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ayumu-1212/herdr-pasture/internal/group"
	"github.com/ayumu-1212/herdr-pasture/internal/snapshot"
)

// Fetcher is the slice of herdr.Client the UI depends on.
type Fetcher interface {
	Snapshot() (snapshot.Snapshot, error)
	FocusAgent(paneID string) error
	FocusWorkspace(workspaceID string) error
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
	cursor       int
	offset       int // index of the first list line drawn under the title bar
	width        int
	height       int
	disconnected bool
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
	err    error
}

type tickMsg struct{ gen int }

// New builds a model that polls fetch every interval.
func New(f Fetcher, r group.Resolver, opt group.Options, interval time.Duration) Model {
	return Model{fetch: f, resolver: r, opt: opt, interval: interval, collapsed: map[string]bool{}}
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
		return snapshotMsg{gen: gen, groups: group.Build(s, resolver, opt)}
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
		if msg.err != nil {
			m.disconnected = true
		} else {
			m.disconnected = false
			m.groups = msg.groups
		}
		m.clampCursor()
		return m, m.tickCmd()
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
		m.collapsed[g.Key] = !m.collapsed[g.Key]
		m.clampCursor()
		return m, nil
	}
	row := g.Rows[l.row]
	fetch := m.fetch
	return m, func() tea.Msg {
		// A row that has vanished simply disappears on the next poll, so
		// neither error is worth surfacing.
		if row.Kind == group.RowWorkspace {
			_ = fetch.FocusWorkspace(row.WorkspaceID)
		} else {
			_ = fetch.FocusAgent(row.PaneID)
		}
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
