package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	appName           = "herdr-pasture"
	disconnectedLabel = "disconnected"
	cursorMarker      = "›"
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true)
	dimStyle     = lipgloss.NewStyle().Faint(true)
	headerStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	focusedStyle = lipgloss.NewStyle().Reverse(true)
	statusStyles = map[string]lipgloss.Style{
		"working": lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		"done":    lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		"blocked": lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		"unknown": lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
	}
	statusIcons = map[string]string{
		"working": "◐",
		"idle":    "○",
		"done":    "✓",
		"blocked": "●",
		"unknown": "?",
	}
)

// View implements tea.Model. It emits at most m.height lines with no trailing
// newline: a taller view makes bubbletea's renderer drop lines from the top,
// which would silently discard the title bar and shift every mouse Y by one.
func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 22 // no WindowSizeMsg yet
	}

	ls := m.lines()
	start := m.offset
	if start > len(ls) {
		start = len(ls)
	}
	end := start + m.visibleRows()
	if end > len(ls) {
		end = len(ls)
	}

	out := make([]string, 0, end-start+1)
	out = append(out, m.titleBar(width))
	for i := start; i < end; i++ {
		l := ls[i]
		g := m.groups[l.group]
		marker := " "
		if i == m.cursor {
			marker = cursorMarker
		}
		var text string
		if l.header {
			chevron := "▾"
			if m.collapsed[g.Key] {
				chevron = "▸"
			}
			text = headerStyle.Render(truncate(chevron+" "+g.Label, width-2))
		} else {
			r := g.Rows[l.row]
			icon := statusIcons[r.Status]
			if icon == "" {
				icon = statusIcons["unknown"]
			}
			if st, ok := statusStyles[r.Status]; ok {
				icon = st.Render(icon)
			}
			label := r.Title
			if r.Branch != "" {
				label = "[" + r.Branch + "] " + label
			}
			label = truncate(label, width-6)
			if r.Focused {
				label = focusedStyle.Render(label)
			}
			text = "  " + icon + " " + label
		}
		out = append(out, marker+text)
	}
	return strings.Join(out, "\n")
}

// titleBar builds the header as plain text, truncates it, and only then
// styles the pieces, so truncate never has to reason about escape sequences.
func (m Model) titleBar(width int) string {
	if !m.disconnected {
		return titleStyle.Render(truncate(appName, width))
	}
	plain := truncate(appName+" "+disconnectedLabel, width)
	if rest, ok := strings.CutPrefix(plain, appName); ok {
		return titleStyle.Render(appName) + dimStyle.Render(rest)
	}
	return titleStyle.Render(plain) // too narrow to keep both parts
}

// truncate cuts s to at most width display cells, appending "…" when cut.
// s must be unstyled.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}
