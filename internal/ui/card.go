package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
)

// More of the issue in the preview: its cycle, due date and estimate on the
// meta line, and the pull requests Linear has linked to it.

// planMeta: "◷ Cycle 12 · due Mar 12 · 3 pts", the parts that are set.
func planMeta(is linear.Issue, now time.Time) []string {
	var out []string
	if c := is.Cycle; c != nil {
		name := "Cycle " + strconv.Itoa(c.Number)
		if c.Name != "" {
			name = c.Name
		}
		out = append(out, sDim.Render("◷ "+name))
	}
	if d, err := time.Parse("2006-01-02", is.DueDate); err == nil {
		label := "due " + d.Format("Jan 2")
		if d.Year() != now.Year() {
			label = "due " + d.Format("Jan 2 2006")
		}
		closed := is.State.Type == "completed" || is.State.Type == "canceled"
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		if !closed && d.Before(today) {
			out = append(out, sErr.Render(label+" · overdue"))
		} else {
			out = append(out, sDim.Render(label))
		}
	}
	if e := is.Estimate; e != nil && *e > 0 {
		out = append(out, sDim.Render(strings.TrimSuffix(fmt.Sprintf("%.1f", *e), ".0")+" pts"))
	}
	return out
}

// prs: the issue's linked pull requests, once the full issue is loaded.
func (m model) prs(is linear.Issue) []linear.PR {
	if full, ok := m.details[is.Identifier]; ok {
		return full.PullRequests()
	}
	return is.PullRequests()
}

// mainPR is the one g opens: the first still open, else the first.
func mainPR(prs []linear.PR) (linear.PR, bool) {
	for _, p := range prs {
		if p.Status == "open" || p.Status == "draft" {
			return p, true
		}
	}
	if len(prs) > 0 {
		return prs[0], true
	}
	return linear.PR{}, false
}

func prStatus(s string) string {
	switch s {
	case "open":
		return sOK.Render("open")
	case "merged":
		return lipgloss.NewStyle().Foreground(cAccent).Render("merged")
	case "draft", "closed":
		return sDim.Render(s)
	}
	return ""
}

func (m model) prBlock(is linear.Issue, w int) string {
	prs := m.prs(is)
	if len(prs) == 0 {
		return ""
	}
	head := "Pull request"
	if len(prs) > 1 {
		head += "s"
	}
	lines := []string{sBold.Render(head) + sDim.Render(" · g opens")}
	for _, p := range prs {
		row := sDim.Render(fmt.Sprintf("#%-5d", p.Number)) + " "
		st := prStatus(p.Status)
		row += st + strings.Repeat(" ", max(0, 6-lipgloss.Width(st))) + "  " + p.Title
		lines = append(lines, ansi.Truncate(row, w, "…"))
	}
	return strings.Join(lines, "\n")
}
