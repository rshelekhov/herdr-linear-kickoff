package ui

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
)

// "n": a new issue. Title (enter creates it right there), description, then
// team, project, status, priority and assignee. The team and project start as
// the list's scope — only a default: any team or project can be picked here.
// A choice opens a searchable list on enter (or typing); ←→ step through it.
// Once created the issue is selected, where enter starts it.

const (
	nfTitle = iota
	nfDesc
	nfTeam
	nfProject
	nfStatus
	nfPriority
	nfAssignee
	nfSubmit // the Create button
	nfFields
)

var nfLabels = [nfFields]string{"Title", "Description", "Team", "Project", "Status", "Priority", "Assignee", ""}

var prioNames = []string{"No priority", "Urgent", "High", "Medium", "Low"}

type newForm struct {
	opts  *linear.NewIssueOptions // nil while loading
	err   string
	busy  bool
	field int
	title textinput.Model
	desc  textarea.Model
	// The choices, as indexes into nfChoices(field).
	team, project, status, prio, who int
	statusSet                        bool // the status was picked: a team's states arriving don't reset it
	// The open list of a choice: its search and cursor.
	listing bool
	listIn  textinput.Model
	listCur int
}

type (
	newOptsMsg struct {
		opts linear.NewIssueOptions
		err  error
	}
	peopleMsg struct {
		teamID string
		people []linear.Person
		err    error
	}
	createdMsg struct {
		issue linear.Issue
		err   error
	}
)

func (m *model) openNewIssue() tea.Cmd {
	if m.client == nil {
		return nil
	}
	ti := textinput.New()
	ti.Placeholder = "what needs doing — enter creates it"
	ti.CharLimit = 255
	ti.Prompt = ""
	ta := textarea.New()
	ta.Placeholder = "details, in markdown (optional)"
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.SetHeight(4)
	ta.CharLimit = 10000
	li := textinput.New()
	li.Prompt = "/ "
	li.Placeholder = "search"
	m.nf = &newForm{title: ti, desc: ta, listIn: li}
	m.screen = scNew
	cmds := []tea.Cmd{m.nf.title.Focus()}
	if m.newOpts != nil {
		m.nf.opts = m.newOpts
		cmds = append(cmds, m.nfDefaults())
	} else {
		c := m.client
		cmds = append(cmds, m.tag(func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			o, err := c.NewIssueOptions(ctx)
			return newOptsMsg{o, err}
		}))
	}
	return tea.Batch(cmds...)
}

// nfDefaults: the team and project the list is narrowed to, else the
// selected issue's team; Todo; me.
func (m *model) nfDefaults() tea.Cmd {
	f, o := m.nf, m.nf.opts
	teamID := ""
	switch m.scope.Kind {
	case "team":
		teamID = m.scope.ID
	case "project":
		for _, p := range o.Projects {
			if p.ID == m.scope.ID && len(p.Teams) > 0 {
				teamID = p.Teams[0]
			}
		}
	}
	if teamID == "" {
		if is, ok := m.current(); ok {
			teamID = is.Team.ID
		}
	}
	f.team = max(0, slices.IndexFunc(o.Teams, func(t linear.TeamOptions) bool { return t.ID == teamID }))
	cmd := m.nfTeamChanged()
	if m.scope.Kind == "project" {
		for i, p := range m.nfProjects() {
			if p.ID == m.scope.ID {
				f.project = i
			}
		}
	}
	return cmd
}

// nfTeamChanged resets what depends on the team and loads its states and
// people if they aren't yet.
func (m *model) nfTeamChanged() tea.Cmd {
	f := m.nf
	f.project, f.who, f.statusSet = 0, 0, false
	m.nfDefaultStatus()
	t, ok := m.nfTeam()
	if !ok {
		return nil
	}
	var cmds []tea.Cmd
	c := m.client
	if _, ok := m.teamStates[t.ID]; !ok {
		cmds = append(cmds, m.tag(func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			s, err := c.TeamStates(ctx, t.ID)
			return statesMsg{t.ID, s, err}
		}))
	}
	if _, ok := m.teamPeople[t.ID]; !ok {
		cmds = append(cmds, m.tag(func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			p, err := c.TeamMembers(ctx, t.ID)
			return peopleMsg{t.ID, p, err}
		}))
	}
	return tea.Batch(cmds...)
}

// nfDefaultStatus: Todo, else Backlog, unless one was picked.
func (m *model) nfDefaultStatus() {
	f := m.nf
	if f.statusSet {
		return
	}
	f.status = 0
	states := m.nfStates()
	for _, want := range []string{"unstarted", "backlog"} {
		if i := slices.IndexFunc(states, func(s linear.State) bool { return s.Type == want }); i >= 0 {
			f.status = i
			return
		}
	}
}

func (m model) nfTeam() (linear.TeamOptions, bool) {
	f := m.nf
	if f.opts == nil || f.team >= len(f.opts.Teams) {
		return linear.TeamOptions{}, false
	}
	return f.opts.Teams[f.team], true
}

// nfStates: the team's states but canceled; nil while loading.
func (m model) nfStates() []linear.State {
	t, _ := m.nfTeam()
	var out []linear.State
	for _, s := range m.teamStates[t.ID] {
		if s.Type != "canceled" {
			out = append(out, s)
		}
	}
	return out
}

// nfProjects: "no project", then the team's open projects.
func (m model) nfProjects() []linear.ProjectOption {
	out := []linear.ProjectOption{{Name: "No project"}}
	t, ok := m.nfTeam()
	if !ok {
		return out
	}
	for _, p := range m.nf.opts.Projects {
		if slices.Contains(p.Teams, t.ID) {
			out = append(out, p)
		}
	}
	return out
}

// nfPeople: me, unassigned, then the team's other members.
func (m model) nfPeople() []linear.Person {
	out := []linear.Person{{ID: m.nf.opts.Me, Name: "Me"}, {Name: "Unassigned"}}
	t, _ := m.nfTeam()
	for _, p := range m.teamPeople[t.ID] {
		if p.ID != m.nf.opts.Me {
			out = append(out, p)
		}
	}
	return out
}

// nfChoices: a choice field's options, as shown.
func (m model) nfChoices(field int) []string {
	f := m.nf
	if f.opts == nil {
		return nil
	}
	var out []string
	switch field {
	case nfTeam:
		for _, t := range f.opts.Teams {
			out = append(out, t.Key+" · "+t.Name)
		}
	case nfProject:
		for _, p := range m.nfProjects() {
			out = append(out, p.Name)
		}
	case nfStatus:
		for _, s := range m.nfStates() {
			out = append(out, stateDot(s)+" "+s.Name)
		}
	case nfPriority:
		out = prioNames
	case nfAssignee:
		for _, p := range m.nfPeople() {
			out = append(out, p.Name)
		}
	}
	return out
}

func (m model) nfChosen(field int) int {
	f := m.nf
	return map[int]int{nfTeam: f.team, nfProject: f.project, nfStatus: f.status, nfPriority: f.prio, nfAssignee: f.who}[field]
}

// nfChoose sets a choice field to option i.
func (m *model) nfChoose(field, i int) tea.Cmd {
	f := m.nf
	switch field {
	case nfTeam:
		if i != f.team {
			f.team = i
			return m.nfTeamChanged()
		}
	case nfProject:
		f.project = i
	case nfStatus:
		f.status, f.statusSet = i, true
	case nfPriority:
		f.prio = i
	case nfAssignee:
		f.who = i
	}
	return nil
}

func (m *model) nfFocus(field int) tea.Cmd {
	f := m.nf
	f.field = (field + nfFields) % nfFields
	f.title.Blur()
	f.desc.Blur()
	switch f.field {
	case nfTitle:
		return f.title.Focus()
	case nfDesc:
		return f.desc.Focus()
	}
	return nil
}

// nfListed: the open list's options matching its search, as indexes.
func (m model) nfListed() []int {
	q := strings.ToLower(strings.TrimSpace(m.nf.listIn.Value()))
	var out []int
	for i, c := range m.nfChoices(m.nf.field) {
		if q == "" || strings.Contains(strings.ToLower(ansi.Strip(c)), q) {
			out = append(out, i)
		}
	}
	return out
}

func (m *model) nfOpenList(seed string) tea.Cmd {
	f := m.nf
	if len(m.nfChoices(f.field)) == 0 {
		return nil
	}
	f.listing = true
	f.listIn.Placeholder = "search " + strings.ToLower(nfLabels[f.field])
	f.listIn.SetValue(seed)
	f.listIn.CursorEnd()
	f.listCur = 0
	if seed == "" { // start on the current choice
		f.listCur = max(0, slices.Index(m.nfListed(), m.nfChosen(f.field)))
	}
	if !nfSearchable(f.field) {
		return nil
	}
	return f.listIn.Focus()
}

// nfSearchable: lists long enough to search (statuses and priorities aren't).
func nfSearchable(field int) bool {
	return field == nfTeam || field == nfProject || field == nfAssignee
}

func (m model) onNewKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.nf
	if f.busy {
		return m, nil
	}
	if f.listing {
		return m.onNewListKey(k)
	}
	switch k.String() {
	case "esc":
		m.screen, m.nf = scList, nil
		return m, nil
	case "ctrl+s":
		return m, m.nfCreate()
	case "tab":
		return m, m.nfFocus(f.field + 1)
	case "shift+tab":
		return m, m.nfFocus(f.field - 1)
	}
	switch f.field {
	case nfTitle:
		switch k.String() {
		case "enter":
			return m, m.nfCreate()
		case "down":
			return m, m.nfFocus(nfDesc)
		case "up":
			return m, m.nfFocus(nfSubmit)
		}
		var cmd tea.Cmd
		f.title, cmd = f.title.Update(k)
		f.err = ""
		return m, cmd
	case nfDesc:
		// The arrows move in the text; up from its first line and down
		// from its last leave it.
		switch k.String() {
		case "up":
			if f.desc.Line() == 0 {
				return m, m.nfFocus(nfTitle)
			}
		case "down":
			if f.desc.Line() >= f.desc.LineCount()-1 {
				return m, m.nfFocus(nfTeam)
			}
		}
		var cmd tea.Cmd
		f.desc, cmd = f.desc.Update(k)
		return m, cmd
	}
	if f.field == nfSubmit {
		switch k.String() {
		case "enter", " ":
			return m, m.nfCreate()
		case "up", "k":
			return m, m.nfFocus(nfAssignee)
		case "down", "j":
			return m, m.nfFocus(nfTitle)
		}
		return m, nil
	}
	n := len(m.nfChoices(f.field)) // a choice
	s := k.String()
	if nfSearchable(f.field) && len(s) == 1 && strings.Contains("hjkl", s) { // letters search these
		return m, m.nfOpenList(s)
	}
	switch s {
	case "up", "k":
		return m, m.nfFocus(f.field - 1)
	case "down", "j":
		return m, m.nfFocus(f.field + 1)
	case "left", "h", "right", "l":
		if n == 0 {
			return m, nil
		}
		d := 1
		if s == "left" || s == "h" {
			d = -1
		}
		return m, m.nfChoose(f.field, ((m.nfChosen(f.field)+d)%n+n)%n)
	case "enter", " ":
		return m, m.nfOpenList("")
	default:
		if r := k.Runes; len(r) == 1 && r[0] > ' ' && nfSearchable(f.field) { // typing searches the list
			return m, m.nfOpenList(string(r))
		}
	}
	return m, nil
}

func (m model) onNewListKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.nf
	listed := m.nfListed()
	switch k.String() {
	case "esc":
		f.listing = false
		f.listIn.Blur()
		return m, nil
	case "up", "ctrl+p":
		f.listCur = max(0, f.listCur-1)
		return m, nil
	case "down", "ctrl+n":
		f.listCur = max(0, min(len(listed)-1, f.listCur+1))
		return m, nil
	case "enter", "tab":
		f.listing = false
		f.listIn.Blur()
		var cmd tea.Cmd
		if f.listCur >= 0 && f.listCur < len(listed) {
			cmd = m.nfChoose(f.field, listed[f.listCur])
		}
		if k.String() == "tab" {
			return m, tea.Batch(cmd, m.nfFocus(f.field+1))
		}
		return m, cmd
	}
	if !nfSearchable(f.field) {
		return m, nil
	}
	var cmd tea.Cmd
	f.listIn, cmd = f.listIn.Update(k)
	f.listCur = min(f.listCur, max(0, len(m.nfListed())-1))
	return m, cmd
}

func (m *model) nfCreate() tea.Cmd {
	f := m.nf
	title := strings.TrimSpace(f.title.Value())
	if title == "" {
		f.err = "A title, please."
		return m.nfFocus(nfTitle)
	}
	t, ok := m.nfTeam()
	if !ok {
		f.err = "Still loading the teams…"
		return nil
	}
	in := linear.IssueCreate{TeamID: t.ID, Title: title, Description: strings.TrimSpace(f.desc.Value()), Priority: f.prio}
	if st := m.nfStates(); f.status < len(st) {
		in.StateID = st[f.status].ID
	} // else Linear's default for the team
	if p := m.nfProjects(); f.project > 0 && f.project < len(p) {
		in.ProjectID = p[f.project].ID
	}
	if pp := m.nfPeople(); f.who < len(pp) {
		in.AssigneeID = pp[f.who].ID
	}
	f.busy, f.err = true, ""
	c := m.client
	return func() tea.Msg { // not tagged: after a switch, still say it's done
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		is, err := c.CreateIssue(ctx, in)
		return createdMsg{is, err}
	}
}

// created: the new issue is selected (its tab, refreshed), ready to start.
func (m *model) created(msg createdMsg) tea.Cmd {
	if m.nf == nil || !m.nf.busy { // the form is gone (a switch): just say so
		if msg.err != nil {
			return m.flashNote("the new issue wasn't created: " + errText(msg.err))
		}
		return m.flashNote("created " + msg.issue.Identifier)
	}
	if msg.err != nil {
		m.nf.busy, m.nf.err = false, errText(msg.err)
		return nil
	}
	m.screen, m.nf = scList, nil
	is := msg.issue
	m.details[is.Identifier] = is
	clear(m.fetched) // every tab may have it now
	nav := m.navigateTo(is)
	return tea.Batch(nav, m.fetchIssues(), m.flashNote("created "+is.Identifier+" — enter starts it"))
}

func (m model) newDialog() string {
	f := m.nf
	w := min(84, max(50, m.w-8))
	inner := w - 4
	const labelW = 15
	f.title.Width = inner - labelW - 1
	f.desc.SetWidth(inner - labelW)
	var b strings.Builder
	row := func(i int, value string) {
		mark, lab := "  ", lipgloss.NewStyle().Width(labelW-2).Render(nfLabels[i])
		if f.field == i {
			mark, lab = sAccent.Render("› "), sBold.Render(lab)
		} else {
			lab = sDim.Render(lab)
		}
		lines := strings.Split(value, "\n")
		b.WriteString(mark + lab + " " + lines[0] + "\n")
		for _, l := range lines[1:] {
			b.WriteString(strings.Repeat(" ", labelW+1) + l + "\n")
		}
	}
	row(nfTitle, f.title.View())
	row(nfDesc, f.desc.View())
	b.WriteString("\n")
	// The choices and the Create button keep their place: an open list
	// goes beside them, in a block of a fixed height.
	var left []string
	valW := inner - labelW - 1
	if f.listing {
		valW = min(valW, (inner-labelW)/2-2)
	}
	for i := nfTeam; i < nfSubmit; i++ {
		ch := m.nfChoices(i)
		value := sDim.Render("loading…")
		if f.err != "" && f.opts == nil { // the options didn't come
			value = sDim.Render("—")
		}
		if len(ch) > 0 {
			cur := ansi.Truncate(ch[min(m.nfChosen(i), len(ch)-1)], valW-4, "…")
			value = sDim.Render("‹ ") + cur + sDim.Render(" ›")
		}
		left = append(left, m.nfRow(i, value, labelW))
	}
	const btn = "[ Create issue ]"
	if f.field == nfSubmit {
		left = append(left, "", sAccent.Render("› ")+sAccent.Render(btn))
	} else {
		left = append(left, "", "  "+sDim.Render(btn))
	}
	var right []string
	if f.listing {
		right = m.nfListView(inner - labelW - valW - 3)
	}
	const blockH = nfListRows + 1
	leftW := labelW + valW
	for j := 0; j < blockH; j++ {
		l, r := "", ""
		if j < len(left) {
			l = left[j]
		}
		if j < len(right) {
			r = right[j]
		}
		if r == "" {
			b.WriteString(strings.TrimRight(l, " ") + "\n")
			continue
		}
		b.WriteString(l + strings.Repeat(" ", max(0, leftW-ansi.StringWidth(l))) + "   " + r + "\n")
	}
	switch {
	case f.busy:
		b.WriteString("\n" + sDim.Render("creating…"))
	case f.err != "":
		b.WriteString("\n" + sErr.Render(ansi.Wrap(f.err, inner, " ")))
	}
	hint := "tab/↑↓ fields · enter or type to choose · ←→ step · esc cancel"
	switch f.field {
	case nfTitle:
		hint = "enter create · tab/↓ more fields · esc cancel"
	case nfSubmit:
		hint = "enter create · tab/↑↓ fields · esc cancel"
	}
	if f.listing {
		hint = "type to search · ↑↓ choose · enter pick · esc back"
		if !nfSearchable(f.field) {
			hint = "↑↓ choose · enter pick · esc back"
		}
	}
	b.WriteString("\n\n" + sDim.Render(ansi.Truncate(hint, inner, "…")))
	title := "New issue"
	if m.multiWS() {
		title += " · " + m.ws.Name
	}
	return dialog(title, b.String(), w)
}

// nfRow is a choice's line: the label, marked when focused, and its value.
func (m model) nfRow(i int, value string, labelW int) string {
	mark, lab := "  ", lipgloss.NewStyle().Width(labelW-2).Render(nfLabels[i])
	if m.nf.field == i {
		mark, lab = sAccent.Render("› "), sBold.Render(lab)
	} else {
		lab = sDim.Render(lab)
	}
	return mark + lab + " " + value
}

const nfListRows = 8

// nfListView: the open choice's search and up to nfListRows matches, w wide.
func (m model) nfListView(w int) []string {
	f := m.nf
	ch := m.nfChoices(f.field)
	listed := m.nfListed()
	var out []string
	if nfSearchable(f.field) {
		out = append(out, f.listIn.View())
	}
	start := max(0, min(f.listCur-nfListRows/2, len(listed)-nfListRows))
	for j := start; j < len(listed) && j < start+nfListRows; j++ {
		line := "  " + ch[listed[j]]
		if j == f.listCur {
			line = sSel.Render("› ") + ch[listed[j]]
		}
		out = append(out, ansi.Truncate(line, w, "…"))
	}
	if len(listed) == 0 {
		out = append(out, sDim.Render("  no match"))
	}
	return out
}
