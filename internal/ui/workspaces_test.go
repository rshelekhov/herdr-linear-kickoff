package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/config"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/workspace"
)

// fakeAPI is one workspace's Linear, in memory.
type fakeAPI struct {
	org     string
	issues  []linear.Issue
	created []linear.IssueCreate
}

func (f *fakeAPI) Viewer(context.Context) (linear.Viewer, error) {
	var v linear.Viewer
	v.ID, v.Name = "u1", "Alex"
	v.Organization.ID, v.Organization.Name, v.Organization.URLKey = f.org, strings.ToUpper(f.org[:1])+f.org[1:], f.org
	return v, nil
}
func (f *fakeAPI) Issues(context.Context, linear.Mode, linear.Scope) ([]linear.Issue, error) {
	return f.issues, nil
}
func (f *fakeAPI) Scopes(context.Context) ([]linear.Scope, error) { return nil, nil }
func (f *fakeAPI) Issue(_ context.Context, id string) (linear.Issue, error) {
	for _, is := range f.issues {
		if is.Identifier == id {
			return is, nil
		}
	}
	return linear.Issue{}, errors.New("Linear: Entity not found: Issue")
}
func (f *fakeAPI) Briefs(context.Context, []string) ([]linear.IssueBrief, error) { return nil, nil }
func (f *fakeAPI) Search(context.Context, string, int) ([]linear.IssueBrief, error) {
	return nil, nil
}
func (f *fakeAPI) TeamStates(context.Context, string) ([]linear.State, error) {
	return []linear.State{{ID: "s-back", Name: "Backlog", Type: "backlog", Position: 1}, {ID: "s-todo", Name: "Todo", Type: "unstarted", Position: 2}}, nil
}
func (f *fakeAPI) UpdateIssue(context.Context, string, linear.IssueUpdate) error {
	return nil
}

func wsIssue(id, title string) linear.Issue {
	is := linear.Issue{ID: "id-" + id, Identifier: id, Title: title}
	is.State = linear.State{Name: "Todo", Type: "unstarted"}
	is.Team.Key = strings.Split(id, "-")[0]
	return is
}

// twoWorkspaces: acme (last used) and zeta, each with its own issue.
func twoWorkspaces(t *testing.T) (Deps, map[string]*fakeAPI) {
	dir := t.TempDir()
	reg := workspace.Load(dir)
	reg.Add(workspace.WS{ID: "acme", Name: "Acme", URLKey: "acme"})
	reg.Add(workspace.WS{ID: "zeta", Name: "Zeta", URLKey: "zeta"})
	reg.Last = "acme"
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	apis := map[string]*fakeAPI{
		"acme": {org: "acme", issues: []linear.Issue{wsIssue("ACM-1", "Acme thing")}},
		"zeta": {org: "zeta", issues: []linear.Issue{wsIssue("ZET-7", "Zeta thing")}},
	}
	d := Deps{Cfg: config.Default(), StateDir: dir, Secrets: &fakeSecrets{}, SourceRepo: "/r/web",
		Connect: func(id string) linear.API { return apis[id] }}
	return d, apis
}

// run feeds a command's messages back into the model, a few rounds deep.
func run(m model, cmd tea.Cmd) model {
	for i := 0; i < 4 && cmd != nil; i++ {
		msg := cmd()
		if b, ok := msg.(tea.BatchMsg); ok {
			var next []tea.Cmd
			for _, c := range b {
				if c == nil {
					continue
				}
				mm, nc := m.Update(c())
				m = mm.(model)
				next = append(next, nc)
			}
			cmd = tea.Batch(next...)
			continue
		}
		mm, nc := m.Update(msg)
		m, cmd = mm.(model), nc
	}
	return m
}

func TestSwitchWorkspace(t *testing.T) {
	d, _ := twoWorkspaces(t)
	m := newModel(d)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = run(mm.(model), m.Init())
	if m.ws.ID != "acme" || len(m.issues) != 1 || m.issues[0].Identifier != "ACM-1" {
		t.Fatalf("opened on %q with %+v", m.ws.ID, m.issues)
	}
	if v := m.View(); !strings.Contains(v, "Acme") {
		t.Errorf("header doesn't name the workspace:\n%s", v)
	}

	// p lists the workspaces; picking Zeta switches.
	m = press(m, "p")
	v := m.View()
	for _, s := range []string{"Workspaces", "Acme  ✓", "Zeta", "Add a workspace…"} {
		if !strings.Contains(v, s) {
			t.Errorf("picker lacks %q:\n%s", s, v)
		}
	}
	vis := m.visibleScopes()
	for i, s := range vis {
		if s.Kind == kindWorkspace && s.ID == "zeta" {
			m.scopeCur = i
		}
	}
	mm, cmd := m.Update(key("enter"))
	m = run(mm.(model), cmd)
	if m.ws.ID != "zeta" || len(m.issues) != 1 || m.issues[0].Identifier != "ZET-7" {
		t.Fatalf("after switching: %q with %+v", m.ws.ID, m.issues)
	}
	reg := workspace.Load(d.StateDir)
	if reg.Last != "zeta" || reg.Repos["/r/web"] != "zeta" {
		t.Errorf("not remembered: %+v", reg)
	}
	if newModel(d).ws.ID != "zeta" {
		t.Error("reopened in the repo on another workspace")
	}
}

// An answer asked of the workspace switched away from is dropped.
func TestStaleAnswerDropped(t *testing.T) {
	d, _ := twoWorkspaces(t)
	m := run(newModel(d), nil)
	old := m.tag(func() tea.Msg {
		return issuesMsg{key: m.listKey(m.mode), mode: m.mode, issues: []linear.Issue{wsIssue("ACM-9", "late")}}
	})
	m = run(m, m.switchWorkspace("zeta", true))
	m = run(m, m.switchWorkspace("acme", true)) // and back: still stale
	mm, _ := m.Update(old())
	m = mm.(model)
	for _, is := range m.issues {
		if is.Identifier == "ACM-9" {
			t.Fatal("a late answer from an earlier visit landed")
		}
	}
}

// An issue of another workspace, by ID: found there, and the popup goes there.
func TestOpenInAnotherWorkspace(t *testing.T) {
	d, _ := twoWorkspaces(t)
	m := run(newModel(d), nil)
	cmd := m.openByID("ZET-7")
	m = run(m, cmd)
	if m.ws.ID != "zeta" {
		t.Fatalf("still in %q", m.ws.ID)
	}
	if _, ok := m.details["ZET-7"]; !ok {
		t.Error("issue not kept for the preview")
	}

	// A link names its workspace: that one is asked first.
	d2, apis := twoWorkspaces(t)
	apis["acme"].issues = append(apis["acme"].issues, wsIssue("ZET-7", "same key in Acme"))
	d2.OpenID = "https://linear.app/zeta/issue/ZET-7/zeta-thing"
	m = newModel(d2)
	if m.ws.ID != "zeta" {
		t.Errorf("a Zeta link opened %q", m.ws.ID)
	}
}

// The single key from before workspaces moves to its workspace.
func TestLegacyKeyMoves(t *testing.T) {
	sec := &legacySecrets{fakeSecrets: fakeSecrets{}, legacy: "old"}
	d := Deps{Cfg: config.Default(), StateDir: t.TempDir(), Secrets: sec}
	if err := os.WriteFile(filepath.Join(d.StateDir, "scope.json"), []byte(`{"tab":"backlog"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newModel(d)
	if m.migrate[0] != "old" || m.screen != scLoading {
		t.Fatalf("legacy key not picked up: %+v, screen %v", m.migrate, m.screen)
	}
	var v linear.Viewer
	v.Organization.ID, v.Organization.Name = "acme", "Acme"
	mm, _ := m.Update(keyCheckedMsg{viewer: v, key: "old", src: "keyring"})
	m = mm.(model)
	if k, _ := sec.Get("acme"); k != "old" || !sec.legacyGone {
		t.Errorf("key %q, legacy gone %v", k, sec.legacyGone)
	}
	if reg := workspace.Load(d.StateDir); len(reg.List) != 1 || reg.List[0].Name != "Acme" {
		t.Errorf("registry %+v", reg.List)
	}
	if _, err := os.Stat(filepath.Join(d.StateDir, "ws", "acme", "scope.json")); err != nil {
		t.Errorf("old scope not moved: %v", err)
	}
}

// A key that answers for another workspace (LINEAR_API_KEY, say) is refused.
func TestKeyOfAnotherWorkspace(t *testing.T) {
	d, apis := twoWorkspaces(t)
	apis["acme"].org = "zeta" // Acme's client is really Zeta's
	m := run(newModel(d), nil)
	v, _ := apis["acme"].Viewer(context.Background())
	mm, _ := m.Update(viewerMsg{viewer: v})
	m = mm.(model)
	if m.screen != scKey || !strings.Contains(m.keyErr, "not Acme") {
		t.Errorf("screen %v, %q", m.screen, m.keyErr)
	}
	// Its list, already on the way, isn't kept as Acme's.
	late := wsMsg{ws: "acme", gen: m.gen - 1, msg: issuesMsg{key: m.listKey(m.mode), mode: m.mode, issues: []linear.Issue{wsIssue("ZET-1", "zeta's")}}}
	mm, _ = m.Update(late)
	for _, is := range mm.(model).issues {
		if is.Identifier == "ZET-1" {
			t.Error("another workspace's issues kept")
		}
	}
}

type legacySecrets struct {
	fakeSecrets
	legacy     string
	legacyGone bool
}

func (l *legacySecrets) Legacy() (string, string) {
	if l.legacyGone {
		return "", ""
	}
	return l.legacy, "keyring"
}
func (l *legacySecrets) DeleteLegacy() error { l.legacyGone = true; return nil }

func (f *fakeAPI) NewIssueOptions(context.Context) (linear.NewIssueOptions, error) {
	return linear.NewIssueOptions{Me: "u1", Teams: []linear.TeamOptions{{ID: "t1", Key: "ACM", Name: "Acme"}, {ID: "t2", Key: "OPS", Name: "Ops"}},
		Projects: []linear.ProjectOption{{ID: "p1", Name: "Checkout", Teams: []string{"t1"}}, {ID: "p2", Name: "Infra", Teams: []string{"t2"}}}}, nil
}

func (f *fakeAPI) TeamMembers(context.Context, string) ([]linear.Person, error) {
	return []linear.Person{{ID: "u1", Name: "Alex"}, {ID: "u2", Name: "Sam"}}, nil
}

func (f *fakeAPI) CreateIssue(_ context.Context, in linear.IssueCreate) (linear.Issue, error) {
	f.created = append(f.created, in)
	is := wsIssue("ACM-99", in.Title)
	f.issues = append(f.issues, is)
	return is, nil
}

// n: a new issue with Todo and me as defaults; created, then selected.
func TestNewIssue(t *testing.T) {
	d, apis := twoWorkspaces(t)
	m := newModel(d)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 36})
	m = run(mm.(model), m.Init())
	mm, cmd := m.Update(key("n"))
	m = run(mm.(model), cmd)
	if m.screen != scNew || m.nf == nil || m.nf.opts == nil {
		t.Fatalf("form: screen %v", m.screen)
	}
	v := m.View()
	for _, s := range []string{"New issue · Acme", "Title", "Todo", "Me", "No project"} {
		if !strings.Contains(v, s) {
			t.Errorf("form lacks %q:\n%s", s, v)
		}
	}
	m = press(m, "enter")
	if m.nf.err == "" {
		t.Error("created without a title")
	}
	m = typeText(m, "Fix the flaky test")
	m = press(m, "tab", "tab")                 // to Team
	m = press(m, "tab", "tab", "tab", "right") // Priority: Urgent
	m = press(m, "down", "down")               // past Assignee to the Create button
	if m.nf.field != nfSubmit || !strings.Contains(m.newDialog(), "[ Create issue ]") {
		t.Fatalf("field %d", m.nf.field)
	}
	mm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(mm.(model), cmd)
	got := apis["acme"].created
	if len(got) != 1 || got[0].Title != "Fix the flaky test" || got[0].StateID != "s-todo" || got[0].AssigneeID != "u1" || got[0].Priority != 1 {
		t.Fatalf("created %+v", got)
	}
	if m.screen != scList || m.selectID != "" && m.selectID != "ACM-99" {
		t.Errorf("after: screen %v, select %q", m.screen, m.selectID)
	}
	if is, _ := m.current(); is.Identifier != "ACM-99" {
		t.Errorf("selected %q", is.Identifier)
	}
}

// A link to a workspace that isn't connected: said so, not looked up elsewhere.
func TestLinkToUnconnectedWorkspace(t *testing.T) {
	d, apis := twoWorkspaces(t)
	apis["acme"].issues = append(apis["acme"].issues, wsIssue("OTH-3", "same ID, other issue"))
	m := run(newModel(d), nil)
	m.openWS = "other-co"
	m = run(m, m.openByID("OTH-3"))
	if m.ws.ID != "acme" || !strings.Contains(m.listErr, "isn't connected") {
		t.Errorf("ws %q, error %q", m.ws.ID, m.listErr)
	}
	if _, ok := m.details["OTH-3"]; ok {
		t.Error("opened the same ID from another workspace")
	}
}

// Settings list every workspace; x removes the one under the cursor, and a
// key replaced from there comes back to settings on esc.
func TestSettingsWorkspaces(t *testing.T) {
	d, _ := twoWorkspaces(t)
	d.ConfigDir = t.TempDir()
	m := newModel(d)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 36})
	m = run(mm.(model), m.Init())
	m = press(m, ",")
	v := m.View()
	if !strings.Contains(v, "Acme (open)") || !strings.Contains(v, "Zeta") || !strings.Contains(v, "Add a workspace…") {
		t.Fatalf("settings:\n%s", v)
	}
	m.setCur = rowIndex(t, m, "ws:zeta")
	m = press(m, "enter")
	if m.screen != scKey {
		t.Fatalf("enter: screen %v", m.screen)
	}
	m = press(m, "esc")
	if m.screen != scSettings {
		t.Errorf("esc from the key went to %v, not settings", m.screen)
	}
	m.setCur = rowIndex(t, m, "ws:zeta")
	m = press(m, "x")
	if m.ws.ID != "acme" || len(m.reg.List) != 1 || m.screen != scSettings {
		t.Errorf("x on Zeta: open %q, %d left, screen %v", m.ws.ID, len(m.reg.List), m.screen)
	}
}

// The form: any team, not just the scope's; a choice is searched by typing.
func TestNewIssueOtherTeam(t *testing.T) {
	d, apis := twoWorkspaces(t)
	m := newModel(d)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	m = run(mm.(model), m.Init())
	mm, cmd := m.Update(key("n"))
	m = run(mm.(model), cmd)
	m = typeText(m, "Rotate keys")
	m = press(m, "down", "down") // description, then Team
	if m.nf.field != nfTeam {
		t.Fatalf("↓↓ from the title: field %d", m.nf.field)
	}
	mm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")}) // typing opens the list
	m = run(mm.(model), cmd)
	m = typeText(m, "ps")
	if !m.nf.listing || !strings.Contains(m.View(), "OPS · Ops") {
		t.Fatalf("no searchable list:\n%s", m.View())
	}
	mm, cmd = m.Update(key("enter"))
	m = run(mm.(model), cmd)
	m = press(m, "down") // Project: Infra is OPS's
	m = press(m, "right")
	if p := m.nfProjects()[m.nf.project]; p.Name != "Infra" {
		t.Errorf("project %q", p.Name)
	}
	mm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	run(mm.(model), cmd)
	got := apis["acme"].created
	if len(got) != 1 || got[0].TeamID != "t2" || got[0].ProjectID != "p2" || got[0].StateID != "s-todo" {
		t.Errorf("created %+v", got)
	}
}

// The old key failing for a network reason is kept, and a key pasted then
// (another workspace's, maybe) doesn't take over the old key's lists.
func TestLegacyKeyKeptOnNetworkError(t *testing.T) {
	sec := &legacySecrets{fakeSecrets: fakeSecrets{}, legacy: "old"}
	d := Deps{Cfg: config.Default(), StateDir: t.TempDir(), Secrets: sec}
	if err := os.WriteFile(filepath.Join(d.StateDir, "scope.json"), []byte(`{"tab":"backlog"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newModel(d)
	mm, _ := m.Update(keyCheckedMsg{key: "old", src: "keyring", err: errors.New("Linear is unreachable: timeout")})
	m = mm.(model)
	if sec.legacyGone || m.screen != scKey || !strings.Contains(m.keyErr+m.View(), "try again") {
		t.Fatalf("legacy gone %v, screen %v", sec.legacyGone, m.screen)
	}
	var v linear.Viewer
	v.Organization.ID, v.Organization.Name = "beta", "Beta"
	mm, _ = m.Update(keyCheckedMsg{viewer: v, key: "new"})
	m = mm.(model)
	if sec.legacyGone {
		t.Error("the old key was deleted for another one")
	}
	if _, err := os.Stat(filepath.Join(d.StateDir, "ws", "beta", "scope.json")); err == nil {
		t.Error("the old key's scope moved to another workspace")
	}
}

// Searching a list down to nothing, then ↓ and enter: no panic, nothing picked.
func TestNewIssueEmptySearch(t *testing.T) {
	d, _ := twoWorkspaces(t)
	m := newModel(d)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	m = run(mm.(model), m.Init())
	mm, cmd := m.Update(key("n"))
	m = run(mm.(model), cmd)
	m = press(m, "down", "down") // Team
	team := m.nf.team
	m = typeText(m, "zzz")
	m = press(m, "down", "enter")
	if m.nf.listing || m.nf.team != team {
		t.Errorf("listing %v, team %d → %d", m.nf.listing, team, m.nf.team)
	}
}
