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
}

// Model is the bubbletea model. Copy semantics: Update returns a new value.
type Model struct {
	fetch    Fetcher
	resolver group.Resolver
	opt      group.Options
	interval time.Duration

	groups       []group.Group
	collapsed    map[string]bool
	cursor       int
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
	groups []group.Group
	err    error
}

type tickMsg struct{}

// New builds a model that polls fetch every interval.
func New(f Fetcher, r group.Resolver, opt group.Options, interval time.Duration) Model {
	return Model{fetch: f, resolver: r, opt: opt, interval: interval, collapsed: map[string]bool{}}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetchCmd(), tea.SetWindowTitle("pasture"))
}

func (m Model) fetchCmd() tea.Cmd {
	fetch, resolver, opt := m.fetch, m.resolver, m.opt
	return func() tea.Msg {
		s, err := fetch.Snapshot()
		if err != nil {
			return snapshotMsg{err: err}
		}
		return snapshotMsg{groups: group.Build(s, resolver, opt)}
	}
}

func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(m.interval, func(time.Time) tea.Msg { return tickMsg{} })
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case snapshotMsg:
		if msg.err != nil {
			m.disconnected = true
		} else {
			m.disconnected = false
			m.groups = msg.groups
		}
		m.clampCursor()
		return m, m.tickCmd()
	case tickMsg:
		return m, m.fetchCmd()
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		idx := msg.Y - 1 // row 0 is the title bar
		if idx < 0 || idx >= len(m.lines()) {
			return m, nil
		}
		m.cursor = idx
		return m.activate(idx)
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
		return m, m.fetchCmd()
	}
	return m, nil
}

// activate toggles a header or focuses a row's pane.
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
	paneID := g.Rows[l.row].PaneID
	fetch := m.fetch
	return m, func() tea.Msg {
		_ = fetch.FocusAgent(paneID) // a vanished pane simply disappears on the next poll
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

func (m *Model) clampCursor() {
	n := len(m.lines())
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}
