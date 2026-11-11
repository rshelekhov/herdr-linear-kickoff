package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
)

// Starting an issue that's closed, someone else's or already in review is
// probably a slip: it's asked about first. The worktree alone (w) isn't: it
// changes nothing in Linear and starts no agent.

// startWarning says why starting this issue needs a yes, or "".
func (m model) startWarning(is linear.Issue) string {
	switch {
	case is.State.Type == "completed" || is.State.Type == "canceled":
		return is.Identifier + " is " + is.State.Name + "."
	case is.Assignee != nil && m.viewer.ID != "" && is.Assignee.ID != m.viewer.ID:
		return is.Identifier + " is assigned to " + is.Assignee.Name + ": its status and assignee stay as they are."
	case is.State.Type == "started" && strings.Contains(strings.ToLower(is.State.Name), "review"):
		return is.Identifier + " is already " + is.State.Name + "."
	}
	return ""
}

// confirmStart asks before an unusual start; true when it did.
func (m *model) confirmStart(is linear.Issue) bool {
	why := m.startWarning(is)
	if why == "" {
		return false
	}
	m.confIssue, m.confWhy = is, why
	// A choice made for this start (A) is kept for after the yes.
	m.confAgent, m.confLaunch = m.agentCmd, m.agentLaunch
	m.screen = scConfirm
	return true
}

func (m model) onConfirmKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y", "enter":
		m.screen = scList
		m.agentCmd, m.agentLaunch = m.confAgent, m.confLaunch
		m.confirmed = true
		cmd := m.begin(m.confIssue, false)
		m.confirmed = false
		return m, cmd
	case "n", "esc", "q":
		m.screen = scList
	}
	return m, nil
}

func (m model) confirmDialog() string {
	const w = 60
	body := ansi.Wordwrap(m.confWhy, w-4, " ") + "\n\n" +
		"Start it anyway: the worktree, and the agent on it?\n\n" +
		sDim.Render("y start · n cancel")
	return dialog("Start "+m.confIssue.Identifier+"?", body, w)
}
