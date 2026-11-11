package ui

import (
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/kickoff"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
)

// repoModel: a list whose selected issue (ABC-964) is in team t-abc, with four
// repos remembered for it; ask_type on, so a start stops at the type screen
// instead of running the kickoff.
func repoModel(t *testing.T) model {
	m := listModel(t)
	m.client = linear.New("k")
	m.d.Cfg.AskType = true
	m.wtBranch, m.wtRepo = map[string]string{}, map[string]string{}
	for i := range m.issues {
		m.issues[i].Team.ID, m.issues[i].Team.Key = "t-abc", "ABC"
	}
	for _, r := range []string{"/c/web", "/c/abc_ios", "/c/abc_admin", "/c/abc_billing"} {
		m.rs.Remember("t-abc", r)
	}
	m.repo = ""
	m.navRootsCache = []string{} // never read the real herdr-navigator config
	return m
}

func TestStartAsksForRepo(t *testing.T) {
	m := repoModel(t)
	m.issues[m.visible[m.cursor]].Title = "Fix the ios sync"
	if v := m.View(); !strings.Contains(v, "new worktree in abc_ios?") {
		t.Errorf("card doesn't show the guess:\n%s", v)
	}
	m = press(m, "enter")
	if m.screen != scRepo {
		t.Fatalf("screen %v, want the repo chooser", m.screen)
	}
	v := m.View()
	for _, s := range []string{"where to start?", "abc_ios", `mentions "ios"`, "another folder…", "always for team ABC"} {
		if !strings.Contains(v, s) {
			t.Errorf("chooser lacks %q:\n%s", s, v)
		}
	}
	m = press(m, "t", "enter") // pin the team, take the guess
	if m.screen != scType || m.pendingRepo != "/c/abc_ios" {
		t.Fatalf("screen %v repo %q", m.screen, m.pendingRepo)
	}
	if m.rs.TeamPins["t-abc"] != "/c/abc_ios" || m.rs.Teams["t-abc"][0] != "/c/abc_ios" {
		t.Errorf("not remembered: pins %v teams %v", m.rs.TeamPins, m.rs.Teams)
	}
	// Pinned: the next start goes without asking.
	m = press(m, "esc")
	if v := m.View(); !strings.Contains(v, "new worktree in abc_ios ") && !strings.Contains(v, "new worktree in abc_ios ·") {
		t.Errorf("pinned card still asks:\n%s", v)
	}
}

func TestStartSureWithWorktree(t *testing.T) {
	m := repoModel(t)
	id := m.issues[m.visible[m.cursor]].Identifier
	m.wtBranch[id], m.wtRepo[id] = "abc-964-x", "/c/abc_billing"
	m = press(m, "enter")
	if m.screen != scType || m.pendingRepo != "/c/abc_billing" {
		t.Fatalf("asked anyway: screen %v repo %q", m.screen, m.pendingRepo)
	}
}

func TestAnotherFolder(t *testing.T) {
	m := repoModel(t)
	m = press(m, "enter")
	for range len(m.pickOpts) {
		m = press(m, "down")
	}
	m = press(m, "enter")
	if m.screen != scRepoFind || !strings.Contains(m.View(), "looking for repos") {
		t.Fatalf("another folder… didn't open the search: %v", m.screen)
	}
	mm, _ := m.Update(foundMsg{"/w/abc_billing", "/w/other/biller-legacy", "/w/unrelated"})
	m = typeText(mm.(model), "bill")
	if ms := m.findMatches(); len(ms) != 2 {
		t.Fatalf("filter bill: %v", ms)
	}
	m = press(m, "down", "enter")
	if m.pendingRepo != "/w/other/biller-legacy" || m.rs.Teams["t-abc"][0] != "/w/other/biller-legacy" {
		t.Errorf("picked %q", m.pendingRepo)
	}
}

func TestAnotherFolderByPath(t *testing.T) {
	dir := t.TempDir()
	if exec.Command("git", "init", "-q", dir).Run() != nil {
		t.Skip("git unavailable")
	}
	m := repoModel(t)
	m = press(m, "enter")
	for range len(m.pickOpts) {
		m = press(m, "down")
	}
	m = press(m, "enter")
	mm, _ := m.Update(foundMsg{})
	m = typeText(mm.(model), filepath.Join(dir, "nope"))
	if !strings.Contains(m.View(), "not a git repository") {
		t.Error("a bad path isn't flagged")
	}
	m.findIn.SetValue(dir)
	if !strings.Contains(m.View(), "✓ git repo") {
		t.Errorf("a repo path isn't recognised:\n%s", m.View())
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(model)
	real, _ := filepath.EvalSymlinks(dir)
	if m.pendingRepo != dir && m.pendingRepo != real {
		t.Errorf("picked %q, want %q", m.pendingRepo, dir)
	}
}

func TestAnotherFolderGroupsByRoot(t *testing.T) {
	m := repoModel(t)
	m.d.Cfg.RepoRoots = []string{"/w", "/p"}
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = press(mm.(model), "enter")
	for range len(m.pickOpts) {
		m = press(m, "down")
	}
	m = press(m, "enter")
	mm, _ = m.Update(foundMsg{"/w/zeta", "/p/alpha", "/w/lab/beta", "/elsewhere/gamma", "/w/alpha"})
	m = mm.(model)
	want := []string{"/elsewhere/gamma", "/p/alpha", "/w/alpha", "/w/lab/beta", "/w/zeta"}
	if got := m.findMatches(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order %v, want %v", got, want)
	}
	v := m.View()
	for _, s := range []string{"/elsewhere/", "/p/", "/w/", "lab/beta"} {
		if !strings.Contains(v, s) {
			t.Errorf("view lacks %q:\n%s", s, v)
		}
	}
	if strings.Index(v, "/p/") > strings.Index(v, "/w/") {
		t.Errorf("groups out of order:\n%s", v)
	}
	m = press(m, "down", "down", "down", "enter") // → /w/lab/beta
	if m.pendingRepo != "/w/lab/beta" {
		t.Errorf("picked %q", m.pendingRepo)
	}
}

// After a kickoff focused a workspace herdr has hidden the popup: the outcome
// goes to a notification and the popup exits, never a hidden done screen.
func TestDoneAfterFocusNotifies(t *testing.T) {
	var got []string
	orig := notify
	t.Cleanup(func() { notify = orig })
	notify = func(id string, res kickoff.Result, err error) {
		got = append(got, id+"|"+strings.Join(res.Notes, ";"))
	}
	m := repoModel(t)
	m.pending = "ABC-964"
	mm, cmd := m.Update(doneMsg{res: kickoff.Result{Workspace: "w7", Notes: []string{"worktree already existed"}}})
	if !quits(cmd) {
		t.Errorf("popup didn't exit, screen %v", mm.(model).screen)
	}
	if len(got) != 1 || got[0] != "ABC-964|worktree already existed" {
		t.Errorf("notified %v", got)
	}
	// No workspace focused (failed early): the popup is still up, show it there.
	got = nil
	mm, _ = m.Update(doneMsg{err: errors.New("not a git repository")})
	if mm.(model).screen != scDone || len(got) != 0 {
		t.Errorf("early failure: screen %v, notified %v", mm.(model).screen, got)
	}
}

// quits: the command (or one in its batch) exits the program.
func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg {
			if quits(c) {
				return true
			}
		}
	}
	return false
}

// Fixing mistakes happens in the chooser: x forgets a repo, unticking a pin
// removes it, and R opens the chooser even when the repo is pinned.
func TestChooserForgetAndUnpin(t *testing.T) {
	m := repoModel(t)
	m.rs.TeamPins["t-abc"] = "/c/abc_ios"
	if v := m.View(); !strings.Contains(v, "in abc_ios · pinned, R to change") {
		t.Errorf("card doesn't say it's pinned:\n%s", v)
	}
	m = press(m, "R")
	if m.screen != scRepo || m.pickOpts[0].Path != "/c/abc_ios" || !m.pinTeam {
		t.Fatalf("R: screen %v first %v pinTeam %v", m.screen, m.pickOpts, m.pinTeam)
	}
	// Forget web (a wrong pick).
	for m.pickOpts[m.pickCur].Path != "/c/web" {
		m = press(m, "down")
	}
	m = press(m, "x")
	if hasOption(m.pickOpts, "/c/web") || slices.Contains(m.rs.Teams["t-abc"], "/c/web") {
		t.Error("x didn't forget web")
	}
	// Untick the team pin and pick abc_billing: the pin is gone.
	m.pickCur = 0
	for m.pickOpts[m.pickCur].Path != "/c/abc_billing" {
		m = press(m, "down")
	}
	m = press(m, "t", "enter")
	if _, ok := m.rs.TeamPins["t-abc"]; ok || m.pendingRepo != "/c/abc_billing" {
		t.Errorf("pin kept or wrong repo: pins %v repo %q", m.rs.TeamPins, m.pendingRepo)
	}
}

func TestSettingsHaveNoRepos(t *testing.T) {
	m := repoModel(t)
	for _, r := range m.settingRows() {
		if strings.HasPrefix(r.id, "repo:") || strings.HasPrefix(r.id, "pin") {
			t.Errorf("settings still list %q", r.id)
		}
	}
}

func TestCloneFromChooser(t *testing.T) {
	dir := t.TempDir()
	upstream := filepath.Join(dir, "up", "abc_club")
	if out, err := exec.Command("git", "init", "-q", upstream).CombinedOutput(); err != nil {
		t.Skipf("git: %v %s", err, out)
	}
	exec.Command("git", "-C", upstream, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "x").Run()

	m := repoModel(t)
	m.d.Cfg.CloneRoot = filepath.Join(dir, "work")
	m = press(m, "enter")
	for range len(m.pickOpts) + 1 {
		m = press(m, "down")
	}
	m = press(m, "enter")
	if m.screen != scRepoClone {
		t.Fatalf("clone… didn't open: %v", m.screen)
	}
	m = press(typeText(m, "--upload-pack=x"), "enter")
	if m.cloneErr == "" || m.cloneBusy {
		t.Fatal("an option-like URL went to git")
	}
	m.cloneIn.SetValue("file://" + upstream)
	if v := m.View(); !strings.Contains(v, "work/abc_club") {
		t.Errorf("destination not shown:\n%s", v)
	}
	mm, _ := m.Update(key("enter"))
	m = mm.(model)
	if !m.cloneBusy || m.clone == nil || !strings.Contains(m.View(), "cloning… 0:0") {
		t.Fatalf("clone not started:\n%s", m.View())
	}
	m = drainClone(t, m)
	want := filepath.Join(dir, "work", "abc_club")
	if m.pendingRepo != want || m.rs.Teams["t-abc"][0] != want {
		t.Errorf("cloned repo not picked: %q, team %v", m.pendingRepo, m.rs.Teams["t-abc"])
	}
}

// drainClone feeds the clone's messages to the model until it finishes.
func drainClone(t *testing.T, m model) model {
	t.Helper()
	ch := m.clone.ch
	for m.cloneBusy {
		select {
		case msg := <-ch:
			mm, _ := m.Update(msg)
			m = mm.(model)
		case <-time.After(30 * time.Second):
			t.Fatal("clone didn't finish")
		}
	}
	return m
}

func TestCloneCancel(t *testing.T) {
	m := repoModel(t)
	m.d.Cfg.CloneRoot = t.TempDir()
	m = press(m, "enter")
	for range len(m.pickOpts) + 1 {
		m = press(m, "down")
	}
	m = press(m, "enter")
	// A URL nobody answers fast; cancel right away.
	m.cloneIn.SetValue("https://10.255.255.1/org/never.git")
	mm, _ := m.Update(key("enter"))
	m = press(mm.(model), "esc")
	m = drainClone(t, m)
	if m.screen != scRepoClone || !strings.Contains(m.cloneErr, "cancelled") {
		t.Errorf("after esc: screen %v err %q", m.screen, m.cloneErr)
	}
}

func TestCloneDestinations(t *testing.T) {
	dir := t.TempDir()
	upstream := filepath.Join(dir, "up", "sso")
	if exec.Command("git", "init", "-q", upstream).Run() != nil {
		t.Skip("git unavailable")
	}
	exec.Command("git", "-C", upstream, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "x").Run()

	m := repoModel(t)
	m.rs.Teams = map[string][]string{} // a new team: no repos to clone next to
	m = press(m, "enter")
	for range len(m.pickOpts) + 1 {
		m = press(m, "down")
	}
	m = press(m, "enter")
	gh := filepath.Join(dir, "github.com")
	mm, _ := m.Update(foundMsg{filepath.Join(gh, "a"), filepath.Join(gh, "b"), filepath.Join(dir, "work", "abc_ios")})
	m = mm.(model)
	m.navRootsCache = []string{filepath.Join(dir, "personal")}
	m.cloneIn.SetValue("https://github.com/rshelekhov/sso.git")
	ds := m.cloneDests()
	if len(ds) != 3 || ds[0].Path != gh || ds[0].Note != "matches the URL" || ds[0].Repos != 2 ||
		ds[2].Path != filepath.Join(dir, "personal") || ds[2].Note != "herdr-navigator root" {
		t.Errorf("destinations: %+v", ds)
	}
	if v := m.View(); !strings.Contains(v, "another folder…") || !strings.Contains(v, "/github.com/sso") {
		t.Errorf("clone dialog:\n%s", v)
	}

	// another folder…: a new one is created and the clone goes there.
	m.cloneIn.SetValue("file://" + upstream)
	for range len(m.cloneDests()) {
		m = press(m, "down")
	}
	m = press(m, "enter")
	if !m.clonePathMode {
		t.Fatal("another folder… didn't ask for a path")
	}
	target := filepath.Join(dir, "brand", "new")
	m.clonePath.SetValue(target)
	if !strings.Contains(m.View(), "will be created") {
		t.Error("no 'will be created' for a missing folder")
	}
	mm, _ = m.Update(key("enter"))
	m = drainClone(t, mm.(model))
	if want := filepath.Join(target, "sso"); m.pendingRepo != want {
		t.Errorf("cloned to %q, want %q (err %q)", m.pendingRepo, want, m.cloneErr)
	}
}

// w goes the same way as enter (repo, then type) but only for the worktree;
// enter and R afterwards are full starts again.
func TestWorktreeOnly(t *testing.T) {
	m := repoModel(t)
	if !strings.Contains(m.View(), "w worktree ·") {
		t.Error("no w hint in the footer")
	}
	m = press(m, "w")
	if m.screen != scRepo || !m.wtOnly {
		t.Fatalf("w: screen %v, worktree only %v", m.screen, m.wtOnly)
	}
	m = press(m, "enter")
	if m.screen != scType || !m.wtOnly {
		t.Fatalf("w lost on the way: screen %v, worktree only %v", m.screen, m.wtOnly)
	}
	m = press(m, "esc", "enter")
	if m.wtOnly {
		t.Error("enter after w still worktree only")
	}
	m = press(m, "esc", "esc", "w", "esc", "R")
	if m.wtOnly {
		t.Error("R after w still worktree only")
	}
}

// A: pick an agent for this start only; it rides through the repo and type
// screens, and a plain enter afterwards is back to the configured one.
func TestStartWithAgent(t *testing.T) {
	oldLook, oldOrch := lookPath, orchestratorInstalled
	lookPath = func(bin string) (string, error) {
		if bin == "claude" || bin == "codex" {
			return "/bin/" + bin, nil
		}
		return "", errors.New("no")
	}
	orchestratorInstalled = func() bool { return true }
	defer func() { lookPath, orchestratorInstalled = oldLook, oldOrch }()

	m := repoModel(t)
	m.d.Cfg.AgentCmd, m.d.Cfg.Launch = "claude", "magus"
	m = press(m, "A")
	if m.screen != scAgent {
		t.Fatalf("screen %v", m.screen)
	}
	v := m.View()
	for _, s := range []string{"start with", "claude", "codex", "magus manager", "default"} {
		if !strings.Contains(v, s) {
			t.Errorf("dialog lacks %q:\n%s", s, v)
		}
	}
	if o := m.agentOpts[m.agentCur]; o.launch != "magus" {
		t.Errorf("cursor not on the default: %+v", o)
	}
	m = press(m, "2") // codex
	if m.screen != scRepo || m.agentCmd != "codex" || m.agentLaunch != "prompt" {
		t.Fatalf("screen %v agent %q %q", m.screen, m.agentCmd, m.agentLaunch)
	}
	m = press(m, "enter")
	if m.screen != scType || m.agentCmd != "codex" {
		t.Fatalf("lost on the way: screen %v agent %q", m.screen, m.agentCmd)
	}
	m = press(m, "esc", "enter")
	if m.agentCmd != "" {
		t.Errorf("plain enter kept the one-time agent %q", m.agentCmd)
	}
}

func TestFooterAndHelp(t *testing.T) {
	m := repoModel(t)
	foot := m.footerHints()
	for _, s := range []string{"enter start", "w worktree", "s status", "/ search", "p project", "? more"} {
		if !strings.Contains(foot, s) {
			t.Errorf("footer lacks %q: %s", s, foot)
		}
	}
	if strings.Contains(foot, "o browser") {
		t.Errorf("footer still lists everything: %s", foot)
	}
	m = press(m, "?")
	v := m.View()
	for _, s := range []string{"Keys", "worktree only", "start with…", "search, ID or link", "settings"} {
		if !strings.Contains(v, s) {
			t.Errorf("help lacks %q", s)
		}
	}
	if m = press(m, "x"); m.screen != scList {
		t.Error("a key didn't close the help")
	}
}

// A closed issue, someone else's, or one in review: asked first; w isn't.
func TestConfirmUnusualStart(t *testing.T) {
	m := repoModel(t)
	m.viewer.ID = "me"
	cur := &m.issues[m.visible[m.cursor]]
	cur.Assignee = &struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}{"u2", "Sam Chen"}
	m = press(m, "enter")
	if m.screen != scConfirm || !strings.Contains(m.View(), "assigned to Sam Chen") {
		t.Fatalf("no question for someone else's issue: screen %v\n%s", m.screen, m.View())
	}
	m = press(m, "n")
	if m.screen != scList {
		t.Fatalf("n: screen %v", m.screen)
	}
	m = press(m, "w")
	if m.screen != scRepo {
		t.Errorf("w asked too: screen %v", m.screen)
	}
	m = press(m, "esc", "enter", "y")
	if m.screen != scRepo || m.wtOnly {
		t.Errorf("y: screen %v, worktree only %v", m.screen, m.wtOnly)
	}

	m = press(m, "esc")
	cur = &m.issues[m.visible[m.cursor]]
	cur.Assignee = nil
	for _, st := range []linear.State{{Name: "Done", Type: "completed"}, {Name: "In Review", Type: "started"}} {
		cur.State = st
		if m.startWarning(*cur) == "" {
			t.Errorf("%s: no warning", st.Name)
		}
	}
	cur.State = linear.State{Name: "In Progress", Type: "started"}
	if w := m.startWarning(*cur); w != "" {
		t.Errorf("in progress, unassigned: %q", w)
	}
}
