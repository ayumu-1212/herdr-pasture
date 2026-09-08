package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true)
	dimStyle     = lipgloss.NewStyle().Faint(true)
	headerStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	focusedStyle = lipgloss.NewStyle().Reverse(true)
	cursorMarker = "›"
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

// View implements tea.Model.
func (m Model) View() string {
	width := m.width
	if width < 22 {
		width = 22
	}
	var b strings.Builder
	title := "herdr-pasture"
	if m.disconnected {
		title += " " + dimStyle.Render("disconnected")
	}
	b.WriteString(titleStyle.Render(truncate(title, width)))
	b.WriteString("\n")

	ls := m.lines()
	for i, l := range ls {
		if m.height > 0 && i+1 >= m.height {
			break
		}
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
		b.WriteString(marker + text + "\n")
	}
	return b.String()
}

// truncate cuts s to at most width display cells, appending "…" when cut.
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
