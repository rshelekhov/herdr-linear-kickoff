package ui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/herdr"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/sidebar"
)

// "s": change the issue's status, laid out like Linear's status menu: states
// by type then position, a search field, and number keys (1–9, triage 0).

type (
	statesMsg struct {
		teamID string
		states []linear.State
		err    error
	}
	statusSetMsg struct {
		id      string // identifier
		issueID string // UUID: the sidebar cache is keyed by it
		state   linear.State
		err     error
	}
)

func newStatusInput() textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "Change status…"
	in.Width = 30
	return in
}

// openStatus shows the status picker for the issue in the preview.
func (m *model) openStatus() tea.Cmd {
	is, ok := m.current()
	if !ok || m.client == nil {
		return nil
	}
	m.stIssue, m.stErr, m.stBusy = is, "", false
	m.screen = scStatus
	m.stIn.SetValue("")
	focus := m.stIn.Focus()
	if _, ok := m.teamStates[is.Team.ID]; ok {
		m.stCur = m.stCurrentIndex()
		return focus
	}
	m.stCur = 0
	c, team := m.client, is.Team.ID
	return tea.Batch(focus, m.tag(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		s, err := c.TeamStates(ctx, team)
		return statesMsg{team, s, err}
	}))
}

// stVisible: the team's states matching the search, in Linear's menu order.
func (m model) stVisible() []linear.State {
	q := strings.ToLower(strings.TrimSpace(m.stIn.Value()))
	var out []linear.State
	for _, s := range m.teamStates[m.stIssue.Team.ID] {
		if q == "" || strings.Contains(strings.ToLower(s.Name), q) {
			out = append(out, s)
		}
	}
	return out
}

// stCurrentIndex is the issue's current state in the visible list.
func (m model) stCurrentIndex() int {
	for i, s := range m.stVisible() {
		if s.ID == m.stIssue.State.ID {
			return i
		}
	}
	return 0
}

// stateKeys numbers the team's states like Linear: 1–9 in order, triage 0.
func stateKeys(states []linear.State) map[string]string {
	keys := map[string]string{}
	n := 0
	for _, s := range states {
		if s.Type == "triage" {
			keys[s.ID] = "0"
			continue
		}
		if n++; n <= 9 {
			keys[s.ID] = string(rune('0' + n))
		}
	}
	return keys
}

func (m model) onStatusKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	vis := m.stVisible()
	switch k.String() {
	case "esc":
		m.screen = scList
		m.stIn.Blur()
		return m, nil
	case "up", "ctrl+p":
		m.stCur = max(0, m.stCur-1)
		return m, nil
	case "down", "ctrl+n":
		m.stCur = min(len(vis)-1, m.stCur+1)
		return m, nil
	case "enter":
		if m.stCur < len(vis) {
			return m.setStatus(vis[m.stCur])
		}
		return m, nil
	}
	if s := k.String(); len(s) == 1 && s[0] >= '0' && s[0] <= '9' && m.stIn.Value() == "" {
		for _, st := range m.teamStates[m.stIssue.Team.ID] {
			if stateKeys(m.teamStates[m.stIssue.Team.ID])[st.ID] == s {
				return m.setStatus(st)
			}
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.stIn, cmd = m.stIn.Update(k)
	m.stCur = min(m.stCurrentIndex(), max(0, len(m.stVisible())-1))
	if m.stIn.Value() != "" {
		m.stCur = 0 // searching: the best match is first
	}
	return m, cmd
}

// setStatus sends the change (nothing for the current status).
func (m model) setStatus(to linear.State) (tea.Model, tea.Cmd) {
	if m.stBusy {
		return m, nil
	}
	if to.ID == m.stIssue.State.ID {
		m.screen = scList
		m.stIn.Blur()
		return m, nil
	}
	m.stBusy, m.stErr = true, ""
	c, id, issueID := m.client, m.stIssue.Identifier, m.stIssue.ID
	return m, m.tag(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := c.UpdateIssue(ctx, issueID, linear.IssueUpdate{StateID: to.ID})
		return statusSetMsg{id, issueID, to, err}
	})
}

// statusSet applies a status change locally, then refreshes the lists from
// Linear: the issue may now belong to another group or tab, or to none.
func (m *model) statusSet(msg statusSetMsg) tea.Cmd {
	// The dialog may have been closed, or opened on another issue, while
	// Linear answered: only close it if it's still this issue's.
	ours := m.screen == scStatus && m.stIssue.Identifier == msg.id
	if ours {
		m.stBusy = false
	}
	if msg.err != nil {
		if ours {
			m.stErr = errText(msg.err)
			return nil
		}
		return m.flashNote(msg.id + ": status not changed — " + errText(msg.err))
	}
	if ours {
		m.screen = scList
		m.stIn.Blur()
	}
	set := func(is *linear.Issue) {
		if is.Identifier == msg.id {
			is.State = msg.state
		}
	}
	for i := range m.issues {
		set(&m.issues[i])
	}
	if d, ok := m.details[msg.id]; ok {
		set(&d)
		m.details[msg.id] = d
	}
	clear(m.fetched) // every tab may have changed: reload on visit
	return tea.Batch(m.flashNote(msg.id+" → "+msg.state.Name), m.fetchIssues(),
		m.sidebarState(msg.issueID, msg.state.Name))
}

func (m model) statusDialog() string {
	const w = 44
	is := m.stIssue
	all, loaded := m.teamStates[is.Team.ID]
	keys := stateKeys(all)
	var b strings.Builder
	b.WriteString(m.stIn.View() + "\n\n")
	switch {
	case !loaded && m.stErr != "":
		b.WriteString(sErr.Render(m.stErr) + "\n")
	case !loaded:
		b.WriteString(sDim.Render("loading statuses…") + "\n")
	}
	inner := w - 6 // border and padding
	for i, s := range m.stVisible() {
		// Every segment carries base, so the selection spans the whole row.
		base := lipgloss.NewStyle()
		if i == m.stCur {
			base = sSel
		}
		check := " "
		if s.ID == is.State.ID {
			check = "✓"
		}
		key := keys[s.ID]
		gap := max(1, inner-len([]rune(s.Name))-len([]rune(check))-len(key)-3)
		b.WriteString(base.Render(" ") + stateDotOn(s, base) + base.Render(" "+s.Name+strings.Repeat(" ", gap)+check+" ") +
			base.Foreground(cDim).Render(key) + base.Render(" ") + "\n")
	}
	if loaded && len(m.stVisible()) == 0 {
		b.WriteString(sDim.Render("no status matches") + "\n")
	}
	b.WriteString("\n")
	switch {
	case m.stBusy:
		b.WriteString(sDim.Render("saving…"))
	case loaded && m.stErr != "":
		b.WriteString(sErr.Render(m.stErr) + "\n" + sDim.Render("enter retry · esc cancel"))
	default:
		b.WriteString(sDim.Render("type to filter · 1–9 · enter · esc"))
	}
	return dialog(is.Identifier+" · status", b.String(), w)
}

// sidebarState carries a status change to the sidebar tokens of the
// workspaces on that issue's worktrees.
func (m model) sidebarState(issueID, state string) tea.Cmd {
	if !m.d.Cfg.Labels || m.d.StateDir == "" {
		return nil
	}
	dir, plugin := m.d.StateDir, m.d.PluginID
	return func() tea.Msg {
		known := false
		if _, err := sidebar.Update(dir, func(st *sidebar.Store) { known = st.SetState(issueID, state) }); err != nil || !known {
			return nil
		}
		if wss, err := herdr.Workspaces(); err == nil {
			_ = sidebar.Sync(context.Background(), dir, wss, nil, false, func(ws string, tok map[string]string) error {
				return herdr.ReportTokens(ws, "plugin:"+plugin, tok)
			})
		}
		return nil
	}
}
