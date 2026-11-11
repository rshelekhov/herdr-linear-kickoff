package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/workspace"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/config"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
)

func sample() []linear.Issue {
	mk := func(id, title, state, typ, color string, prio int) linear.Issue {
		is := linear.Issue{Identifier: id, Title: title, URL: "https://linear.app/x/issue/" + id, Priority: prio,
			UpdatedAt: time.Now(), Description: "Description of " + id + ".\n\n- item one\n- item two"}
		is.State = linear.State{Name: state, Type: typ, Color: color}
		is.Team.Key = strings.Split(id, "-")[0]
		return is
	}
	return []linear.Issue{
		mk("ABC-970", "Export reports to Excel", "Todo", "unstarted", "#e2e2e2", 3),
		mk("ABC-964", "Optimize the web app for phone", "In Progress", "started", "#f2c94c", 2),
		mk("ABC-951", "Fix login redirect loop on Safari when session expires", "Backlog", "backlog", "#bec2c8", 1),
		mk("ABC-955", "Add a filter by manager", "In Progress", "started", "#f2c94c", 0),
	}
}

// fakeSecrets keeps tests away from the real keychain.
type fakeSecrets struct{ key string }

func (f *fakeSecrets) Get(string) (string, string) {
	if f.key == "" {
		return "", ""
	}
	return f.key, "test"
}
func (f *fakeSecrets) Set(_, k string) (string, error) { f.key = k; return "test", nil }
func (f *fakeSecrets) Delete(string) error             { f.key = ""; return nil }
func (f *fakeSecrets) Legacy() (string, string)        { return "", "" }
func (f *fakeSecrets) DeleteLegacy() error             { return nil }

// deps: one workspace connected (its key is the fake's).
func deps(t *testing.T) Deps {
	dir := t.TempDir()
	reg := workspace.Load(dir)
	reg.Add(workspace.WS{ID: "w1", Name: "Acme", URLKey: "acme"})
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	return Deps{Cfg: config.Default(), StateDir: dir, Secrets: &fakeSecrets{}}
}

// listModel is a real model (newModel) showing sample issues, no API key.
func listModel(t *testing.T) model {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := newModel(deps(t))
	m.wtBranch = map[string]string{"ABC-964": "old/abc-964-web"}
	m.mode = linear.ModeMine
	m.screen = scList
	m.setIssues(sample())
	m.viewer.Name = "Alex"
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 16})
	return mm.(model)
}
func TestListView(t *testing.T) {
	m := listModel(t)
	v := m.View()
	if os.Getenv("SHOW") != "" {
		t.Log("\n" + v)
	}
	for _, s := range []string{"In Progress", "ABC-964", "ABC-951", "old/abc-964-web", "enter → open worktree"} {
		if !strings.Contains(v, s) {
			t.Errorf("view lacks %q", s)
		}
	}
	// In Progress comes first, so the cursor starts on an in-progress issue.
	if is, _ := m.selected(); is.State.Type != "started" {
		t.Errorf("first selected = %s (%s)", is.Identifier, is.State.Name)
	}
	if lines := strings.Count(v, "\n") + 1; lines != 16 {
		t.Errorf("view is %d lines, window is 16", lines)
	}
}

func TestPaletteFindsInList(t *testing.T) {
	m := listModel(t)
	m = press(m, "/")
	if items := m.palItems(); len(items) != 0 {
		t.Errorf("nothing typed, yet %d rows", len(items))
	}
	if v := m.View(); !strings.Contains(v, "Type part of a title") || strings.Contains(v, "Export reports to") {
		t.Errorf("empty palette should show only the hint:\n%s", v)
	}
	h0 := lipgloss.Height(m.paletteDialog())
	m = typeText(m, "e")
	if h := lipgloss.Height(m.paletteDialog()); h != h0 {
		t.Errorf("the box changed height: %d → %d", h0, h)
	}
	m = typeText(m, "xcel")
	if !m.going {
		t.Fatal("/ doesn't open the palette")
	}
	items := m.palItems()
	if len(items) == 0 || items[0].id != "ABC-970" || items[0].kind != palLocal {
		t.Fatalf("excel → %+v", items)
	}
	v := m.View()
	for _, s := range []string{"Search", "ABC-970", "Export reports to", "Mine"} {
		if !strings.Contains(v, s) {
			t.Errorf("palette lacks %q:\n%s", s, v)
		}
	}
	m = press(m, "enter")
	if is, _ := m.current(); is.Identifier != "ABC-970" || m.going {
		t.Errorf("enter: selected %q, palette open %v", is.Identifier, m.going)
	}
}

// A match in another loaded tab: enter switches to that tab, on the issue.
func TestPaletteOtherTab(t *testing.T) {
	m := listModel(t)
	other := sample()[0]
	other.Identifier, other.Title = "ABC-777", "Backlog thing about invoices"
	m.lists[linear.ModeBacklog] = []linear.Issue{other}
	m.fetched[m.listKey(linear.ModeBacklog)] = true
	m = typeText(press(m, "/"), "invoices")
	if it := m.palItems(); len(it) != 1 || it[0].mode != linear.ModeBacklog {
		t.Fatalf("items %+v", it)
	}
	m = press(m, "enter")
	if is, _ := m.current(); m.mode != linear.ModeBacklog || is.Identifier != "ABC-777" {
		t.Errorf("mode %s, selected %q", m.mode, is.Identifier)
	}
}

// Linear's results come after the loaded ones, under "In Linear", without
// repeating what's already listed.
func TestPaletteLinearResults(t *testing.T) {
	m := listModel(t)
	m.client = linear.New("k")
	m = press(m, "/")
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("report")})
	m = mm.(model)
	if cmd == nil {
		t.Fatal("Linear search not scheduled")
	}
	if !strings.Contains(m.View(), "searching Linear") {
		t.Error("no searching note")
	}
	mm, _ = m.Update(palResultMsg{q: "report", results: []linear.IssueBrief{
		{Identifier: "ABC-970", Title: "Export reports to Excel"},
		{Identifier: "ABC-812", Title: "Old report export", State: linear.State{Name: "Done", Type: "completed"}},
	}})
	m = mm.(model)
	items := m.palItems()
	if len(items) != 2 || items[0].id != "ABC-970" || items[1].id != "ABC-812" || items[1].kind != palRemote {
		t.Fatalf("items %+v", items)
	}
	if v := m.View(); !strings.Contains(v, "In Linear") || !strings.Contains(v, "Done") {
		t.Errorf("no Linear section:\n%s", v)
	}
	m = press(m, "down")
	mm, cmd = m.Update(key("enter"))
	if m = mm.(model); m.opening != "ABC-812" || cmd == nil {
		t.Errorf("not opening ABC-812: %q", m.opening)
	}
}

// Linear found it by its description, but it's in a loaded tab: the row
// names the tab, and enter selects it there instead of fetching it.
func TestPaletteLinearResultInAList(t *testing.T) {
	m := listModel(t)
	m.client = linear.New("k")
	m = typeText(press(m, "/"), "manager")
	mm, _ := m.Update(palResultMsg{q: "manager", results: []linear.IssueBrief{
		{Identifier: "ABC-964", Title: "Optimize the web app for phone"},
	}})
	m = mm.(model)
	var it palItem
	for _, x := range m.palItems() {
		if x.id == "ABC-964" {
			it = x
		}
	}
	if it.kind != palRemote || it.mode != linear.ModeMine {
		t.Fatalf("ABC-964: %+v", it)
	}
	m.palCur = slices.IndexFunc(m.palItems(), func(x palItem) bool { return x.id == "ABC-964" })
	mm, _ = m.Update(key("enter"))
	if m = mm.(model); m.opening != "" {
		t.Errorf("fetched %q instead of selecting it", m.opening)
	}
	if is, _ := m.current(); is.Identifier != "ABC-964" {
		t.Errorf("selected %q", is.Identifier)
	}
}

func TestMatchIssue(t *testing.T) {
	cases := []struct {
		q, id, title string
		ok           bool
		tHL          []int
	}{
		{"excel", "ABC-970", "Export reports to Excel", true, []int{18, 19, 20, 21, 22}},
		{"970", "ABC-970", "x", true, nil},
		{"reports excel", "ABC-970", "Export reports to Excel", true, nil},
		{"exrp", "ABC-970", "Export reports", true, []int{0, 1, 4, 9}},
		{"zzz", "ABC-970", "Export", false, nil},
	}
	for _, c := range cases {
		_, _, tHL, ok := matchIssue(c.q, c.id, c.title)
		if ok != c.ok || (c.tHL != nil && !slices.Equal(tHL, c.tHL)) {
			t.Errorf("%q in %q: ok %v hl %v", c.q, c.title, ok, tHL)
		}
	}
	// A whole-word match outranks letters scattered around.
	a, _, _, _ := matchIssue("web", "ABC-1", "Optimize the web app")
	b, _, _, _ := matchIssue("web", "ABC-2", "Wide label")
	if a <= b {
		t.Errorf("web: word %d vs scattered %d", a, b)
	}
}

func TestPreviewNewBranch(t *testing.T) {
	m := listModel(t)
	m.wtBranch = map[string]string{}
	v := m.View()
	for _, s := range []string{"enter → new worktree", "abc-964-optimize-the-web-app-for-phone"} {
		if !strings.Contains(v, s) {
			t.Errorf("view lacks %q", s)
		}
	}
}

// A long description scrolls under the card pinned to the bottom.
func TestPreviewCardPinned(t *testing.T) {
	m := listModel(t)
	for i := range m.issues {
		m.issues[i].Description = strings.Repeat("line\n\n", 40)
	}
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 16 {
		t.Fatalf("view is %d lines", len(lines))
	}
	// Body ends just above the footer; the card's two lines are the last body lines.
	assertCardAtBottom(t, lines)
}

// Short content: the card still sits at the bottom, not right under the text.
func TestPreviewCardBottomShort(t *testing.T) {
	assertCardAtBottom(t, strings.Split(listModel(t).View(), "\n"))
}

// Above the footer: the start card and one blank line.
func assertCardAtBottom(t *testing.T, lines []string) {
	t.Helper()
	// The card is the scroll-hint line, one or two lines (action, branch), then
	// a blank line, right above the footer.
	if len(lines) != 16 || strings.Contains(lines[14], "enter →") ||
		!strings.Contains(lines[13]+lines[12], "enter →") {
		t.Fatalf("card not at bottom:\n%s", strings.Join(lines, "\n"))
	}
}
func TestPreviewComments(t *testing.T) {
	m := listModel(t)
	at := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	c := func(who, body string, d int) linear.Comment {
		cm := linear.Comment{Body: body, CreatedAt: at.AddDate(0, 0, d)}
		cm.User = &struct {
			Name string `json:"name"`
		}{who}
		return cm
	}
	m.details["ABC-964"] = withComments(c("Anna", "first", 0), c("Boris", "second", 1))
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = mm.(model)
	v := m.View()
	for _, s := range []string{"Comments · 2", "Anna", "first", "Boris", "second"} {
		if !strings.Contains(v, s) {
			t.Errorf("view lacks %q", s)
		}
	}
	if strings.Index(v, "first") > strings.Index(v, "second") {
		t.Error("comments not oldest first")
	}
	m.details = map[string]linear.Issue{}
	m.detFailed["ABC-964"] = true
	if v := m.View(); !strings.Contains(v, "couldn't load comments") {
		t.Error("no failure note")
	}
	if m.wantDetails() != nil {
		t.Error("failed fetch must not be retried automatically")
	}
}

func probe(n int) *struct {
	Nodes []struct {
		ID string `json:"id"`
	} `json:"nodes"`
} {
	p := &struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	}{}
	for range n {
		p.Nodes = append(p.Nodes, struct {
			ID string `json:"id"`
		}{"c"})
	}
	return p
}

// No comments or sub-issues per the list: no request, and no "loading" that
// then vanishes.
func TestNoCommentsNoLoading(t *testing.T) {
	m := listModel(t)
	m.client = linear.New("k")
	m.issues[m.visible[m.cursor]].CommentProbe = probe(0)
	m.issues[m.visible[m.cursor]].ChildProbe = probe(0)
	if strings.Contains(m.View(), "loading comments") {
		t.Error("shows loading for an issue without comments")
	}
	if m.wantDetails() != nil {
		t.Error("fetches comments for an issue without comments")
	}

	m.issues[m.visible[m.cursor]].CommentProbe = probe(1)
	clear(m.detWanted)
	if !strings.Contains(m.View(), "loading comments") {
		t.Error("no loading note for an issue with comments")
	}
	if m.wantDetails() == nil {
		t.Error("doesn't fetch comments for an issue with comments")
	}
}

func TestModeSwitch(t *testing.T) {
	m := listModel(t)
	m.client = linear.New("k")
	team := sample()[:1]
	team[0].Identifier, team[0].Title = "ABC-1", "Someone else's issue"
	team[0].Assignee = &struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}{"u", "Kim Kowalski"}
	m.lists[linear.ModeTeam] = team

	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = mm.(model)
	if m.mode != linear.ModeTeam || cmd == nil {
		t.Fatalf("tab → mode %s, cmd %v", m.mode, cmd)
	}
	v := m.View()
	for _, s := range []string{"ABC-1", "KK", "@Kim Kowalski"} {
		if !strings.Contains(v, s) {
			t.Errorf("team view lacks %q", s)
		}
	}
	if strings.Contains(v, "ABC-964") {
		t.Error("team view shows the mine list")
	}

	// A late answer for "mine" must not replace the team list on screen.
	mm, _ = m.Update(issuesMsg{key: "mine", mode: linear.ModeMine, issues: sample()})
	m = mm.(model)
	if is, _ := m.selected(); is.Identifier != "ABC-1" {
		t.Errorf("stale mine list replaced team view: %s", is.Identifier)
	}
	if len(m.lists[linear.ModeMine]) != 4 {
		t.Error("mine list not kept for later")
	}

	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if mm.(model).mode != linear.ModeMine {
		t.Error("1 doesn't switch to mine")
	}
}

func withComments(cs ...linear.Comment) linear.Issue {
	var is linear.Issue
	is.Comments = &struct {
		Nodes []linear.Comment `json:"nodes"`
	}{cs}
	return is
}

func ref(id, title, stateType string) linear.IssueRef {
	return linear.IssueRef{Identifier: id, Title: title, State: &linear.State{Name: stateType, Type: stateType}}
}

func TestSubIssuesNested(t *testing.T) {
	m := listModel(t)
	is := sample()
	// ABC-951 (backlog) and ABC-970 (todo) are sub-issues of ABC-964 (in progress).
	for i := range is {
		if is[i].Identifier == "ABC-951" || is[i].Identifier == "ABC-970" {
			p := ref("ABC-964", "Optimize", "started")
			is[i].Parent = &p
		}
	}
	m.setIssues(is)
	var order []string
	for _, i := range m.visible {
		order = append(order, m.issues[i].Identifier)
	}
	// Parent first among in-progress, its sub-issues right after it, oldest group order kept.
	want := "ABC-964 ABC-970 ABC-951 ABC-955"
	if got := strings.Join(order, " "); got != want {
		t.Fatalf("order %s, want %s", got, want)
	}
	v := m.View()
	if !strings.Contains(v, "↳") {
		t.Error("no ↳ for sub-issues")
	}
	body := v[strings.Index(v, "\n"):] // past the mode tabs, which say "Backlog"
	if strings.Contains(body, "Backlog") || strings.Contains(body, "Todo") {
		t.Error("sub-issues under a listed parent still open their own state groups\n" + v)
	}
}

func TestPreviewSubIssues(t *testing.T) {
	m := listModel(t)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = mm.(model)
	full := withComments()
	full.Children = &struct {
		Nodes []linear.IssueRef `json:"nodes"`
	}{[]linear.IssueRef{ref("ABC-1", "Part one", "completed"), ref("ABC-2", "Part two", "started")}}
	m.details["ABC-964"] = full
	v := m.View()
	for _, s := range []string{"Sub-issues · 1/2 done", "ABC-1", "Part two"} {
		if !strings.Contains(v, s) {
			t.Errorf("preview lacks %q", s)
		}
	}

	// A sub-issue shows its parent above the title.
	p := ref("ABC-900", "Inbox redesign", "started")
	m.issues[m.visible[m.cursor]].Parent = &p
	if v := m.View(); !strings.Contains(v, "↑ ABC-900 Inbox redesign") {
		t.Error("no parent line")
	}
}

func TestCardParentBranch(t *testing.T) {
	m := listModel(t)
	m.d.Cfg.SubIssueBranch = "parent"
	p := ref("ABC-900", "Inbox redesign", "started")
	m.issues[m.visible[m.cursor]].Parent = &p
	if v := m.View(); !strings.Contains(v, "new worktree for parent ABC-900") {
		t.Errorf("card doesn't mention the parent's branch:\n%s", v)
	}
	m.wtBranch["ABC-900"] = "abc-900-inbox"
	if v := m.View(); !strings.Contains(v, "new tab in ABC-900's worktree") || !strings.Contains(v, "abc-900-inbox") {
		t.Errorf("card doesn't offer the parent's worktree:\n%s", v)
	}
	m.d.Cfg.SubIssueBranch = "own" // own branch: back to the issue's worktree
	if v := m.View(); !strings.Contains(v, "old/abc-964-web") {
		t.Errorf("own mode lost the issue's worktree:\n%s", v)
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func press(m model, keys ...string) model {
	for _, k := range keys {
		mm, _ := m.Update(key(k))
		m = mm.(model)
	}
	return m
}

func TestOpenSubIssue(t *testing.T) {
	m := listModel(t)
	parent := withComments()
	parent.Identifier = "ABC-964"
	parent.Children = &struct {
		Nodes []linear.IssueRef `json:"nodes"`
	}{[]linear.IssueRef{ref("ABC-1", "Part one", "started"), ref("ABC-2", "Part two", "unstarted")}}
	m.details["ABC-964"] = parent
	sub := withComments()
	sub.Identifier, sub.Title, sub.Description = "ABC-2", "Part two", "Sub-issue body"
	p := ref("ABC-964", "Optimize the web app for phone", "started")
	sub.Parent = &p
	m.details["ABC-2"] = sub // already fetched: opens without a request

	m = press(m, "right")
	if !m.subFocus || !strings.Contains(m.View(), "enter open") {
		t.Fatal("→ doesn't focus sub-issues")
	}
	m = press(m, "down", "enter")
	if is, _ := m.current(); is.Identifier != "ABC-2" {
		t.Fatalf("opened %q, want ABC-2", is.Identifier)
	}
	v := m.View()
	for _, s := range []string{"Sub-issue body", "↑ ABC-964", "← back"} {
		if !strings.Contains(v, s) {
			t.Errorf("opened sub-issue view lacks %q", s)
		}
	}
	m = press(m, "left")
	if is, _ := m.current(); is.Identifier != "ABC-964" {
		t.Errorf("← went to %q, want ABC-964", is.Identifier)
	}
	m = press(m, "right", "enter")
	m = press(m, "j") // moving in the list drops the opened sub-issue
	if len(m.drill) != 0 || m.subFocus {
		t.Error("list move kept the preview drilled in")
	}
}

func TestPreviewScroll(t *testing.T) {
	m := listModel(t)
	m.issues[m.visible[m.cursor]].Description = strings.Repeat("para\n\n", 30)
	v := m.View()
	if !strings.Contains(v, "more · J") {
		t.Fatalf("no scroll hint:\n%s", v)
	}
	m = press(m, "ctrl+d")
	m.View()
	if m.pv.scroll == 0 {
		t.Fatal("ctrl+d didn't scroll")
	}
	for range 20 {
		m = press(m, "ctrl+d")
		m.View()
	}
	if m.pv.scroll != m.pv.max {
		t.Errorf("scroll %d past max %d", m.pv.scroll, m.pv.max)
	}
	if v := m.View(); !strings.Contains(v, "↑ K") {
		t.Error("no ↑ hint at the bottom")
	}
}

func TestJKScroll(t *testing.T) {
	m := listModel(t)
	m.issues[m.visible[m.cursor]].Description = strings.Repeat("para\n\n", 30)
	m.View()
	m = press(m, "J", "J")
	if m.pv.scroll != 2 {
		t.Fatalf("J J → scroll %d", m.pv.scroll)
	}
	m = press(m, "K")
	if m.pv.scroll != 1 || m.screen != scList {
		t.Fatalf("K → scroll %d, screen %v (K must not open the key screen)", m.pv.scroll, m.screen)
	}
}

// In the sub-issue list: the card says enter opens the chosen sub-issue, and
// scrolling still works.
func TestSubFocusCardAndScroll(t *testing.T) {
	m := listModel(t)
	m.issues[m.visible[m.cursor]].Description = strings.Repeat("para\n\n", 30)
	parent := withComments()
	parent.Children = &struct {
		Nodes []linear.IssueRef `json:"nodes"`
	}{[]linear.IssueRef{ref("ABC-1", "Part one", "started"), ref("ABC-2", "Part two", "unstarted")}}
	m.details["ABC-964"] = parent
	m.View()
	m = press(m, "right", "down")
	v := m.View()
	if !strings.Contains(v, "enter → open ABC-2 Part two") || strings.Contains(v, "new worktree") {
		t.Errorf("card in sub-issue list:\n%s", v)
	}
	before := m.pv.scroll
	m = press(m, "K", "K", "K") // the list is at the bottom: scroll up, away from it
	m.View()
	if m.pv.scroll >= before {
		t.Errorf("J doesn't scroll in the sub-issue list (scroll %d, max %d)", m.pv.scroll, m.pv.max)
	}
	if !m.subFocus {
		t.Error("J left the sub-issue list")
	}
}

func TestScopePicker(t *testing.T) {
	m := listModel(t)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = mm.(model)
	m.client = linear.New("k")
	m.scopes = []linear.Scope{
		{Kind: "team", ID: "t1", Name: "ABC · Lanes"},
		{Kind: "project", ID: "p1", Name: "Web & Mobile"},
		{Kind: "project", ID: "p2", Name: "Booker"},
	}
	m = press(m, "p")
	if m.screen != scScope || !strings.Contains(m.View(), "Web & Mobile") {
		t.Fatalf("p doesn't open the picker:\n%s", m.View())
	}
	for _, r := range "book" {
		m = press(m, string(r))
	}
	if vis := m.visibleScopes(); len(vis) != 2 || vis[1].Name != "Booker" {
		t.Fatalf("search: %+v", vis)
	}
	m = press(m, "down", "enter")
	if m.screen != scList || m.scope.ID != "p2" {
		t.Fatalf("enter didn't apply: screen %v scope %+v", m.screen, m.scope)
	}
	if !strings.Contains(m.View(), "Project: Booker") {
		t.Error("header doesn't show the scope")
	}
	if !m.inflight["mine-project-p2"] {
		t.Errorf("no request for the scoped list: %v", m.inflight)
	}
	// A late answer for the unscoped list must not show up now.
	mm, _ = m.Update(issuesMsg{key: "mine", mode: linear.ModeMine, issues: sample()})
	if len(mm.(model).issues) != 0 {
		t.Error("stale unscoped list shown under a scope")
	}
	// Every tab is narrowed to it: switching asks for that tab's list in p2.
	for range 2 {
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = mm.(model)
	}
	for _, key := range []string{"team-project-p2", "backlog-project-p2"} {
		if !m.inflight[key] {
			t.Errorf("no request %s: %v", key, m.inflight)
		}
	}
	if !strings.Contains(m.View(), "Project: Booker") {
		t.Error("the scope isn't shown on another tab")
	}
	// The choice survives reopening.
	m2 := newModel(Deps{Cfg: m.d.Cfg, StateDir: m.d.StateDir, Secrets: &fakeSecrets{key: "k"}})
	if m2.scope.ID != "p2" {
		t.Errorf("saved scope: %+v", m2.scope)
	}
}

func TestScopePickerGroups(t *testing.T) {
	m := listModel(t)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = mm.(model)
	m.client = linear.New("k")
	m.scopes = []linear.Scope{{Kind: "team", ID: "t1", Name: "ABC"}, {Kind: "project", ID: "p1", Name: "Web & Mobile"}}
	m = press(m, "p")
	v := m.View()
	order := []string{"All my teams", "Teams", "ABC", "Projects", "Web & Mobile"}
	last := -1
	for _, s := range order {
		i := strings.Index(v, s)
		if i <= last {
			t.Fatalf("%q missing or out of order:\n%s", s, v)
		}
		last = i
	}
	if strings.ContainsAny(v, "▤◆∗") {
		t.Error("type icons still shown")
	}
	// Headings aren't selectable: down from "All my teams" lands on the team.
	m = press(m, "down", "enter")
	if m.scope.ID != "t1" {
		t.Errorf("chose %+v", m.scope)
	}
	if !strings.Contains(m.View(), "Team: ABC") {
		t.Error("header doesn't say Team: ABC")
	}
}

// Once the current tab is loaded, the others load in the background so their
// counts show without visiting them.
func TestPrefetchOtherTabs(t *testing.T) {
	m := listModel(t)
	m.client = linear.New("k")
	m.scope = linear.Scope{Kind: "team", ID: "t1", Name: "ABC"}
	m.inflight["mine-team-t1"] = true
	mm, _ := m.Update(issuesMsg{key: "mine-team-t1", mode: linear.ModeMine, issues: sample()})
	m = mm.(model)
	for _, key := range []string{"team-team-t1", "backlog-team-t1"} {
		if !m.inflight[key] {
			t.Errorf("%s not prefetched: %v", key, m.inflight)
		}
	}
	// The background answer fills the count but leaves the screen alone.
	mm, _ = m.Update(issuesMsg{key: "backlog-team-t1", mode: linear.ModeBacklog, issues: sample()[:2]})
	m = mm.(model)
	if v := m.View(); !strings.Contains(v, "Backlog 2") || m.mode != linear.ModeMine {
		t.Errorf("backlog count not shown or tab switched:\n%s", v)
	}
	// Already loaded: no second round.
	if cmd := m.prefetchOtherTabs(); cmd != nil && !m.inflight["team-team-t1"] {
		t.Error("prefetched a loaded tab again")
	}
}

func typeText(m model, s string) model {
	for _, r := range s {
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mm.(model)
	}
	return m
}

func TestGotoIssue(t *testing.T) {
	m := listModel(t)
	m.client = linear.New("k")

	// In the list: the cursor moves to it.
	m = press(m, ":")
	if !m.going || !strings.Contains(m.View(), "Search") {
		t.Fatal(": doesn't open the palette")
	}
	m = press(typeText(m, "abc-970"), "enter")
	if is, _ := m.current(); is.Identifier != "ABC-970" || m.going {
		t.Fatalf("selected %q", is.Identifier)
	}

	// Nothing matches: a note, the palette stays.
	m = press(typeText(press(m, ":"), "zq"), "enter")
	if !m.going || !strings.Contains(m.View(), "nothing found") {
		t.Fatalf("no match accepted:\n%s", m.View())
	}
	m = press(m, "esc")

	// Not in the list: a link loads the issue into the preview.
	m = typeText(press(m, ":"), "https://linear.app/acme/issue/ABC-5/some-title")
	mm, cmd := m.Update(key("enter"))
	m = mm.(model)
	if m.opening != "ABC-5" || cmd == nil {
		t.Fatalf("not fetching ABC-5: opening=%q", m.opening)
	}
	other := withComments()
	other.Identifier, other.Title = "ABC-5", "Someone else's thing"
	mm, _ = m.Update(openMsg{id: "ABC-5", issue: other})
	m = mm.(model)
	if is, _ := m.current(); is.Identifier != "ABC-5" || !strings.Contains(m.View(), "Someone else's thing") {
		t.Fatalf("ABC-5 not in the preview: %q", is.Identifier)
	}
}

// A Ctrl-clicked link opens the popup on that issue; by-id opens the input.
func TestStartFromLinkOrByID(t *testing.T) {
	d := deps(t)
	d.Secrets, d.OpenID = &fakeSecrets{key: "k"}, "abc-77"
	if m := newModel(d); m.opening != "ABC-77" {
		t.Errorf("link: opening %q", m.opening)
	}
	d.OpenID, d.StartGoto = "", true
	if m := newModel(d); !m.going {
		t.Error("by-id: no go-to input")
	}
}

func issueIn(id, teamID, teamKey, stateType, assigneeID string) linear.Issue {
	is := withComments()
	is.Identifier, is.Title = id, "Title of "+id
	is.Team.ID, is.Team.Key, is.Team.Name = teamID, teamKey, teamKey+" team"
	is.State = linear.State{Name: stateType, Type: stateType}
	if assigneeID != "" {
		is.Assignee = &struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}{assigneeID, "Someone"}
	}
	return is
}

// Opening an issue from another team moves the scope there (remembered), to
// the tab that lists it, and selects it once the list arrives.
func TestGotoOtherTeam(t *testing.T) {
	m := listModel(t)
	m.client = linear.New("k")
	m.viewer.ID = "me"
	m.scope = linear.Scope{Kind: "team", ID: "ops", Name: "OPS · Ops tools"}

	m = press(typeText(press(m, ":"), "ABC-500"), "enter")
	if m.opening != "ABC-500" {
		t.Fatalf("opening %q", m.opening)
	}
	is := issueIn("ABC-500", "abc", "ABC", "started", "someone")
	mm, _ := m.Update(openMsg{id: "ABC-500", issue: is})
	m = mm.(model)
	if m.scope.ID != "abc" || m.mode != linear.ModeTeam {
		t.Fatalf("scope %+v mode %s", m.scope, m.mode)
	}
	if v := m.View(); !strings.Contains(v, "switched from OPS · Ops tools") || !strings.Contains(v, "Team: ABC · ABC team") {
		t.Errorf("no switch note or scope in header:\n%s", v)
	}
	// The list for the new scope arrives with the issue in it: cursor on it.
	list := append(sample(), is)
	mm, _ = m.Update(issuesMsg{key: "team-team-abc", mode: linear.ModeTeam, issues: list})
	m = mm.(model)
	if cur, _ := m.current(); cur.Identifier != "ABC-500" || len(m.drill) != 0 {
		t.Errorf("selected %q, drill %v", cur.Identifier, m.drill)
	}
	// Remembered for the next popup.
	m2 := newModel(Deps{Cfg: m.d.Cfg, StateDir: m.d.StateDir, Secrets: &fakeSecrets{key: "k"}})
	if m2.scope.ID != "abc" {
		t.Errorf("not remembered: %+v", m2.scope)
	}
}

func TestGotoKeepsCoveringScope(t *testing.T) {
	m := listModel(t)
	m.client = linear.New("k")
	m.viewer.ID = "me"
	m = press(typeText(press(m, ":"), "ABC-501"), "enter")
	mm, _ := m.Update(openMsg{id: "ABC-501", issue: issueIn("ABC-501", "abc", "ABC", "backlog", "me")})
	m = mm.(model)
	if !m.scope.All() || m.mode != linear.ModeMine || m.flash != "" {
		t.Errorf("all-teams scope changed: %+v mode %s flash %q", m.scope, m.mode, m.flash)
	}
}

// A link opened before the viewer arrived: the open fetches it along, and
// the issue lands on Mine, not on All as someone else's.
func TestGotoBeforeViewerKnown(t *testing.T) {
	m := listModel(t)
	m.client = linear.New("k")
	m.viewer = linear.Viewer{}
	m.switchMode(linear.ModeTeam) // looking at All: Mine must come from the viewer
	m = press(typeText(press(m, ":"), "ABC-503"), "enter")
	mm, _ := m.Update(openMsg{id: "ABC-503", issue: issueIn("ABC-503", "abc", "ABC", "started", "me"),
		viewer: linear.Viewer{ID: "me"}})
	m = mm.(model)
	if m.viewer.ID != "me" || m.mode != linear.ModeMine {
		t.Errorf("viewer %q, mode %s", m.viewer.ID, m.mode)
	}
}

func TestGotoClosedIssue(t *testing.T) {
	m := listModel(t)
	m.client = linear.New("k")
	m = press(typeText(press(m, ":"), "ABC-502"), "enter")
	mm, _ := m.Update(openMsg{id: "ABC-502", issue: issueIn("ABC-502", "abc", "ABC", "completed", "")})
	m = mm.(model)
	if cur, _ := m.current(); cur.Identifier != "ABC-502" || !strings.Contains(m.View(), "not in any list") {
		t.Errorf("closed issue: current %q\n%s", cur.Identifier, m.View())
	}
	if !m.scope.All() {
		t.Error("closed issue moved the scope")
	}
}

func TestMatchIssueNotStrewn(t *testing.T) {
	if _, _, _, ok := matchIssue("rep", "ABC-951", "Fix login redirect loop on Safari"); ok {
		t.Error("rep matched letters strewn over the title")
	}
}
