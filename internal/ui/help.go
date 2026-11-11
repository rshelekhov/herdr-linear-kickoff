package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// "?": every key of the list, grouped. The footer shows only the main ones.

type helpGroup struct {
	title string
	keys  [][2]string
}

var helpLeft = []helpGroup{
	{"Issue", [][2]string{
		{"enter", "start"},
		{"w", "worktree only"},
		{"A", "start with…"},
		{"s", "status"},
		{"R", "choose the repo"},
		{"o / y", "browser / copy link"},
		{"g", "its pull request"},
	}},
	{"Preview", [][2]string{
		{"J/K", "scroll"},
		{"ctrl+d/u", "scroll a page"},
		{"→ / ←", "sub-issues / back"},
	}},
}

var helpRight = []helpGroup{
	{"List", [][2]string{
		{"↑↓ j/k", "move"},
		{"tab 1-4", "Mine / All / Backlog / Recent"},
		{"p", "workspace, team, project"},
		{"/  :", "search, ID or link"},
		{"n", "new issue"},
		{"r", "refresh"},
	}},
	{"Popup", [][2]string{
		{",", "settings"},
		{"q esc", "close"},
	}},
}

func helpColumn(groups []helpGroup) string {
	const keyW = 10
	var b strings.Builder
	for i, g := range groups {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(sBold.Render(g.title) + "\n")
		for _, k := range g.keys {
			b.WriteString(sAccent.Render(k[0]) + strings.Repeat(" ", max(1, keyW-lipgloss.Width(k[0]))) + k[1] + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m model) helpDialog() string {
	body := lipgloss.JoinHorizontal(lipgloss.Top, helpColumn(helpLeft), "     ", helpColumn(helpRight))
	body += "\n\n" + sDim.Render("any key — back")
	w := min(m.w-4, lipgloss.Width(body)+6)
	return dialog("Keys", body, w)
}
