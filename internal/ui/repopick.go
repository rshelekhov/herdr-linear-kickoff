package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/branch"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/config"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/herdr"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/kickoff"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/repos"
)

// Where an issue starts: sure (an existing worktree, a pin) → go; otherwise
// the chooser with a guess on top, then "another folder…" to search the disk.

type foundMsg []string // repos discovered for "another folder…"

// repoPlan decides where an issue would start, from what the UI already knows.
func (m model) repoPlan(is linear.Issue) repos.Plan {
	h := repos.Hints{ConfigRepos: map[string]string{}}
	for k, v := range m.d.Cfg.Repos {
		h.ConfigRepos[k] = config.ExpandHome(v)
	}
	home := is.Identifier
	if kickoff.SharesParentBranch(m.d.Cfg, is) {
		home = is.Parent.Identifier // it starts in its parent's worktree
	}
	h.WorktreeRepo = m.wtRepo[home]
	if is.Parent != nil {
		h.ParentRepo = m.wtRepo[is.Parent.Identifier]
	}
	return m.rs.Decide(is, h)
}

// begin starts an issue (only: just its worktree): straight away when the
// repo is sure, else asks.
func (m *model) begin(is linear.Issue, only bool) tea.Cmd {
	if !only && !m.confirmed && m.confirmStart(is) {
		return nil
	}
	m.wtOnly = only
	if p := m.repoPlan(is); p.Sure {
		return m.proceed(is, p.Path)
	}
	m.openChooser(is)
	return nil
}

// openChooser shows "where to start?" for an issue — also when the repo is
// sure (R), so a pin can be changed or a wrong pick forgotten.
func (m *model) openChooser(is linear.Issue) {
	p := m.repoPlan(is)
	opts := p.Options
	if p.Sure && !hasOption(opts, p.Path) { // a pin or a worktree: offer it first
		opts = append([]repos.Option{{Path: p.Path, Reason: p.Reason}}, opts...)
	}
	for _, r := range m.rs.Teams[is.Team.ID] { // and the team's other repos
		if !hasOption(opts, r) {
			opts = append(opts, repos.Option{Path: r})
		}
	}
	if m.repo != "" && !hasOption(opts, m.repo) { // the workspace the popup was opened from
		opts = append(opts, repos.Option{Path: m.repo, Reason: "this workspace"})
	}
	m.pickIssue, m.pickOpts, m.pickCur = is, opts, 0
	// The boxes show the pins as they are; unticking one removes it.
	m.pinTeam = m.rs.TeamPins[is.Team.ID] != ""
	m.pinProject = is.Project != nil && m.rs.ProjectPins[is.Project.ID] != ""
	m.screen = scRepo
}

func hasOption(opts []repos.Option, p string) bool {
	for _, o := range opts {
		if o.Path == p {
			return true
		}
	}
	return false
}

// chosen records the picked repo (and pins), then goes on to start.
func (m *model) chosen(path string) tea.Cmd {
	is := m.pickIssue
	m.rs.Remember(is.Team.ID, path)
	m.rs.Names[is.Team.ID] = is.Team.Key
	if is.Project != nil {
		m.rs.Names[is.Project.ID] = is.Project.Name
	}
	if m.pinTeam {
		m.rs.TeamPins[is.Team.ID] = path
	} else {
		delete(m.rs.TeamPins, is.Team.ID)
	}
	if is.Project != nil {
		if m.pinProject {
			m.rs.ProjectPins[is.Project.ID] = path
		} else {
			delete(m.rs.ProjectPins, is.Project.ID)
		}
	}
	_ = m.rs.Save()
	return m.proceed(is, path)
}

// proceed: the branch type (if asked for), then the kickoff.
func (m *model) proceed(is linear.Issue, repo string) tea.Cmd {
	m.pendingRepo = repo
	if m.d.Cfg.AskType && len(m.d.Cfg.Types) > 0 {
		m.pending = is.Identifier
		m.types = m.d.Cfg.Types
		m.typeCur = 0
		guess := kickoff.BranchName(kickoff.Env{Cfg: m.d.Cfg}, is, "")
		for i, t := range m.types {
			if strings.HasPrefix(guess, t+"/") || t == branch.TypeFor(is.LabelNames(), m.d.Cfg.TypeMap, m.d.Cfg.TypeDefault) {
				m.typeCur = i
				break
			}
		}
		m.screen = scType
		return nil
	}
	return m.startKickoff(is.Identifier, "", repo)
}

func (m model) onRepoKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.pickOpts) + 2 // + "another folder…" + "clone…"
	switch k.String() {
	case "esc", "q":
		m.screen = scList
	case "up", "k":
		m.pickCur = max(0, m.pickCur-1)
	case "down", "j":
		m.pickCur = min(n-1, m.pickCur+1)
	case "p":
		m.pinProject = m.pickIssue.Project != nil && !m.pinProject
	case "t":
		m.pinTeam = !m.pinTeam
	case "x": // forget a wrong pick: out of the team's list (and any pin to it)
		if m.pickCur < len(m.pickOpts) {
			is, path := m.pickIssue, m.pickOpts[m.pickCur].Path
			m.rs.Forget(is.Team.ID, path)
			for _, pins := range []map[string]string{m.rs.TeamPins, m.rs.ProjectPins} {
				for id, p := range pins {
					if p == path {
						delete(pins, id)
					}
				}
			}
			_ = m.rs.Save()
			if path != m.repo { // "this workspace" stays offered
				m.pickOpts = append(m.pickOpts[:m.pickCur:m.pickCur], m.pickOpts[m.pickCur+1:]...)
				m.pickCur = min(m.pickCur, len(m.pickOpts))
			}
		}
	case "enter":
		if m.pickCur < len(m.pickOpts) {
			return m, m.chosen(m.pickOpts[m.pickCur].Path)
		}
		if m.pickCur == len(m.pickOpts) {
			return m, m.openFind()
		}
		return m, m.openClone()
	}
	return m, nil
}

func (m *model) openFind() tea.Cmd {
	m.screen = scRepoFind
	m.findIn.SetValue("")
	m.findCur = 0
	return tea.Batch(m.findIn.Focus(), m.scanRepos())
}

// navRoots are herdr-navigator's [[roots]], read once per popup.
func (m *model) navRoots() []string {
	if m.navRootsCache == nil {
		m.navRootsCache = repos.NavigatorRoots(filepath.Dir(config.HerdrConfigPath()))
		if m.navRootsCache == nil {
			m.navRootsCache = []string{}
		}
	}
	return m.navRootsCache
}

// scanRepos finds the git repos on this machine (once per popup): repo_roots
// and herdr-navigator's roots, plus known repos and those open in herdr.
func (m *model) scanRepos() tea.Cmd {
	if m.found != nil || m.foundBusy {
		return nil
	}
	m.foundBusy = true
	roots := append(slices.Clone(m.d.Cfg.RepoRoots), m.navRoots()...)
	known := m.rs.Known()
	if m.repo != "" {
		known = append(known, m.repo)
	}
	return func() tea.Msg {
		extra := known
		if wss, err := herdr.Workspaces(); err == nil { // repos open in herdr
			for _, ws := range wss {
				if cwd, err := herdr.WorkspaceCwd(ws.ID); err == nil && cwd != "" {
					if top, ok := repos.RepoRoot(cwd); ok {
						extra = append(extra, top)
					}
				}
			}
		}
		return foundMsg(repos.Discover(roots, extra))
	}
}

// pathMode: what's typed is a path (~/…, /…, ./…), not a search.
func pathMode(s string) bool {
	return strings.HasPrefix(s, "~") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, ".")
}

// findMatches filters the discovered repos by what's typed, in display order:
// grouped by root folder (see repoGroup), groups and names sorted.
func (m model) findMatches() []string {
	q := strings.ToLower(strings.TrimSpace(m.findIn.Value()))
	if pathMode(q) {
		return nil
	}
	var out []string
	for _, p := range m.found {
		if q == "" || strings.Contains(strings.ToLower(filepath.Base(p)), q) ||
			strings.Contains(strings.ToLower(repos.Tilde(p)), q) {
			out = append(out, p)
		}
	}
	roots := m.findRoots()
	sort.SliceStable(out, func(i, j int) bool {
		gi, gj := repoGroup(out[i], roots), repoGroup(out[j], roots)
		if gi != gj {
			return strings.ToLower(gi) < strings.ToLower(gj)
		}
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

func (m model) findRoots() []string {
	roots := make([]string, 0, len(m.d.Cfg.RepoRoots))
	for _, r := range m.d.Cfg.RepoRoots {
		roots = append(roots, filepath.Clean(config.ExpandHome(r)))
	}
	return roots
}

// repoGroup is the folder a repo is listed under: the deepest repo_roots
// entry holding it (~/work for ~/work/a/b), else its parent folder.
func repoGroup(path string, roots []string) string {
	best := ""
	for _, r := range roots {
		if strings.HasPrefix(path, r+string(filepath.Separator)) && len(r) > len(best) {
			best = r
		}
	}
	if best == "" {
		return filepath.Dir(path)
	}
	return best
}

func (m model) onFindKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	v := m.findIn.Value()
	switch k.String() {
	case "esc":
		m.screen = scRepo
		m.findIn.Blur()
		return m, nil
	case "up", "ctrl+p":
		m.findCur = max(0, m.findCur-1)
		return m, nil
	case "down", "ctrl+n":
		m.findCur = min(max(0, len(m.findMatches())-1), m.findCur+1)
		return m, nil
	case "tab":
		if pathMode(v) {
			if c := repos.Complete(v); len(c) > 0 {
				m.findIn.SetValue(repos.Tilde(repos.CommonPrefix(c)))
				m.findIn.CursorEnd()
			}
		}
		return m, nil
	case "enter":
		path := ""
		if pathMode(v) {
			if top, ok := m.repoAt(v); ok {
				path = top
			}
		} else if ms := m.findMatches(); m.findCur < len(ms) {
			path = ms[m.findCur]
		}
		if path == "" {
			return m, nil
		}
		m.findIn.Blur()
		return m, m.chosen(path)
	}
	var cmd tea.Cmd
	m.findIn, cmd = m.findIn.Update(k)
	m.findCur = 0
	return m, cmd
}

// ---- views (drawn over the list, like "go to issue")

func dialog(title, body string, w int) string {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).
		Padding(1, 2).Width(w).Render(sAccent.Render(title) + "\n\n" + body)
}

func (m model) repoDialog() string {
	is := m.pickIssue
	w := min(72, max(40, m.w-8))
	var b strings.Builder
	if len(m.pickOpts) == 0 {
		b.WriteString(sDim.Render("No repos known for "+is.Team.Key+" yet.") + "\n\n")
	}
	row := func(i int, name, note string) {
		line := "  " + name
		if note != "" {
			line += "  " + sDim.Render(note)
		}
		if i == m.pickCur {
			line = sSel.Render("› "+name) + "  " + sDim.Render(note)
		}
		b.WriteString(ansi.Truncate(line, w-6, "…") + "\n")
	}
	for i, o := range m.pickOpts {
		row(i, o.Name(), o.Reason)
	}
	row(len(m.pickOpts), "another folder…", "search this computer")
	row(len(m.pickOpts)+1, "clone…", "from a git URL")
	b.WriteString("\n")
	box := func(on bool) string {
		if on {
			return "[x]"
		}
		return "[ ]"
	}
	if is.Project != nil {
		b.WriteString(fmt.Sprintf("%s always for project %s  %s\n", box(m.pinProject), is.Project.Name, sDim.Render("p")))
	}
	b.WriteString(fmt.Sprintf("%s always for team %s  %s\n", box(m.pinTeam), is.Team.Key, sDim.Render("t")))
	b.WriteString("\n" + sDim.Render("enter start · x forget repo · esc cancel"))
	return dialog(is.Identifier+" · where to start?", b.String(), w)
}

// pathMemo remembers the last typed path's check: the dialog redraws on
// every cursor blink, and each check is a git process.
type pathMemo struct {
	path, top string
	ok, done  bool
}

func (m model) repoAt(path string) (string, bool) {
	c := m.pathMemo
	if c == nil {
		return repos.RepoRoot(path)
	}
	if !c.done || c.path != path {
		c.top, c.ok = repos.RepoRoot(path)
		c.path, c.done = path, true
	}
	return c.top, c.ok
}

func (m model) findDialog() string {
	w := min(80, max(40, m.w-8))
	var b strings.Builder
	b.WriteString(m.findIn.View() + "\n\n")
	v := m.findIn.Value()
	switch {
	case pathMode(v):
		if top, ok := m.repoAt(v); ok {
			b.WriteString(sOK.Render("✓ git repo "+filepath.Base(top)) + "  " + sDim.Render(repos.Tilde(top)) + "\n")
		} else {
			b.WriteString(sErr.Render("✗ not a git repository") + "\n")
		}
		b.WriteString("\n" + sDim.Render("tab complete · enter use it · esc back"))
	case m.foundBusy:
		b.WriteString(sDim.Render("looking for repos…") + "\n")
	default:
		ms := m.findMatches()
		roots := m.findRoots()
		// Lines with a header above each group; sel is the cursor's line.
		type line struct {
			text string
			path int // index into ms, -1 for a header
		}
		var lines []line
		sel, group := 0, ""
		for i, p := range ms {
			if g := repoGroup(p, roots); g != group || i == 0 {
				if i > 0 {
					lines = append(lines, line{"", -1})
				}
				group = g
				lines = append(lines, line{sDim.Render(repos.Tilde(g) + "/"), -1})
			}
			rel, err := filepath.Rel(group, p)
			if err != nil {
				rel = filepath.Base(p)
			}
			text := "  " + rel
			if i == m.findCur {
				text = sSel.Render("› " + rel)
				sel = len(lines)
			}
			lines = append(lines, line{text, i})
		}
		room := max(3, m.h-16)
		start := max(0, min(sel-room/2, len(lines)-room))
		if start > 0 && lines[start].path >= 0 { // keep the group's header in view
			for h := start - 1; h >= 0 && sel-h < room; h-- {
				if lines[h].path < 0 && lines[h].text != "" {
					start = h
					break
				}
			}
		}
		for i := start; i < len(lines) && i < start+room; i++ {
			b.WriteString(ansi.Truncate("  "+lines[i].text, w-6, "…") + "\n")
		}
		if len(ms) == 0 {
			b.WriteString(sDim.Render("no repos match — type a path: ~/…") + "\n")
		}
		b.WriteString("\n" + sDim.Render("type to filter · or a path: ~/… (tab) · enter use · esc back"))
	}
	return dialog(m.pickIssue.Identifier+" · pick a repo", b.String(), w)
}

func newFindInput() textinput.Model {
	in := textinput.New()
	in.Prompt = "/ "
	in.Placeholder = "repo name, or a path"
	in.Width = 50
	return in
}

// ---- "clone…": a repo that isn't on this machine yet

type (
	clonedMsg struct {
		path string
		err  error
	}
	cloneProgressMsg string    // a progress line from git
	cloneTickMsg     time.Time // a second passed: redraw the timer
)

func newClonePathInput() textinput.Model {
	in := textinput.New()
	in.Prompt = "› "
	in.Width = 50
	return in
}

func newCloneInput() textinput.Model {
	in := textinput.New()
	in.Prompt = "› "
	in.Placeholder = "git@github.com:org/repo.git, https://…, or org/repo"
	in.Width = 56
	return in
}

func (m *model) openClone() tea.Cmd {
	m.screen = scRepoClone
	m.cloneIn.SetValue("")
	m.cloneErr, m.cloneBusy = "", false
	m.cloneDest, m.clonePathMode = 0, false
	m.navRoots()
	return tea.Batch(m.cloneIn.Focus(), m.scanRepos())
}

// cloneDests: where the clone can go, best first (see repos.CloneDests).
func (m model) cloneDests() []repos.Dest {
	return repos.CloneDests(m.d.Cfg.CloneRoot, m.rs.Teams[m.pickIssue.Team.ID], m.found, m.navRootsCache, m.cloneIn.Value())
}

// cloneRoot is the folder the clone goes into: the chosen destination, or
// the typed path under "another folder…".
func (m model) cloneRoot() string {
	if m.clonePathMode {
		return config.ExpandHome(strings.TrimSpace(m.clonePath.Value()))
	}
	if ds := m.cloneDests(); m.cloneDest < len(ds) {
		return ds[m.cloneDest].Path
	}
	return repos.CloneRoot("", nil) // nothing found anywhere: ~/code
}

func (m model) onCloneKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.cloneBusy {
		if k.String() == "esc" && m.clone != nil {
			m.clone.cancel() // the clone stops and removes its folder
		}
		return m, nil
	}
	if m.clonePathMode { // typing the folder for "another folder…"
		switch k.String() {
		case "esc":
			m.clonePathMode = false
			m.clonePath.Blur()
			return m, m.cloneIn.Focus()
		case "tab":
			if c := repos.Complete(m.clonePath.Value()); len(c) > 0 {
				m.clonePath.SetValue(repos.Tilde(repos.CommonPrefix(c)))
				m.clonePath.CursorEnd()
			}
			return m, nil
		case "enter":
			if strings.TrimSpace(m.clonePath.Value()) == "" {
				return m, nil
			}
			return m.beginClone()
		}
		var cmd tea.Cmd
		m.clonePath, cmd = m.clonePath.Update(k)
		return m, cmd
	}
	n := len(m.cloneDests()) + 1 // + "another folder…"
	switch k.String() {
	case "esc":
		m.screen = scRepo
		m.cloneIn.Blur()
		return m, nil
	case "up", "ctrl+p":
		m.cloneDest = max(0, m.cloneDest-1)
		return m, nil
	case "down", "ctrl+n":
		m.cloneDest = min(n-1, m.cloneDest+1)
		return m, nil
	case "enter":
		if _, err := repos.ParseCloneSource(m.cloneIn.Value()); err != nil {
			m.cloneErr = err.Error()
			return m, nil
		}
		if m.cloneDest == n-1 { // another folder…
			m.clonePathMode = true
			m.clonePath.SetValue("~/")
			m.clonePath.CursorEnd()
			m.cloneIn.Blur()
			return m, m.clonePath.Focus()
		}
		return m.beginClone()
	}
	var cmd tea.Cmd
	m.cloneIn, cmd = m.cloneIn.Update(k)
	m.cloneErr = ""
	m.cloneDest = 0 // the URL may change what fits best
	return m, cmd
}

func (m model) beginClone() (tea.Model, tea.Cmd) {
	src, err := repos.ParseCloneSource(m.cloneIn.Value())
	if err != nil {
		m.cloneErr = err.Error()
		return m, nil
	}
	m.cloneBusy, m.cloneErr = true, ""
	return m, m.startClone(src)
}

// cloneRun is a clone in progress: its messages arrive on ch.
type cloneRun struct {
	ch       chan tea.Msg
	cancel   context.CancelFunc
	started  time.Time
	progress string
}

func (m *model) startClone(src repos.CloneSource) tea.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	run := &cloneRun{ch: make(chan tea.Msg, 64), cancel: cancel, started: time.Now()}
	m.clone = run
	root := m.cloneRoot()
	go func() {
		defer cancel()
		p, err := repos.Clone(ctx, src, root, func(line string) {
			select { // drop a line rather than block git
			case run.ch <- cloneProgressMsg(line):
			default:
			}
		})
		run.ch <- clonedMsg{p, err}
	}()
	return tea.Batch(waitProgress(run.ch), cloneTick())
}

func cloneTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return cloneTickMsg(t) })
}

func (m *model) cloned(msg clonedMsg) tea.Cmd {
	m.cloneBusy, m.clone = false, nil
	if msg.err != nil {
		m.cloneErr = msg.err.Error()
		return nil
	}
	m.cloneIn.Blur()
	return m.chosen(msg.path)
}

func (m model) cloneDialog() string {
	w := min(80, max(44, m.w-8))
	var b strings.Builder
	b.WriteString(m.cloneIn.View() + "\n\n")
	b.WriteString(sDim.Render("into") + "\n")
	dests := m.cloneDests()
	if m.foundBusy && len(dests) == 0 {
		b.WriteString(sDim.Render("  looking for your repo folders…") + "\n")
	}
	row := func(i int, name, note string) {
		line := "  " + name + "  " + sDim.Render(note)
		if i == m.cloneDest && !m.clonePathMode {
			line = sSel.Render("› "+name) + "  " + sDim.Render(note)
		}
		b.WriteString(ansi.Truncate(line, w-6, "…") + "\n")
	}
	for i, d := range dests {
		note := d.Note
		if d.Repos > 0 {
			note = strings.TrimSpace(fmt.Sprintf("%d repo%s  %s", d.Repos, plural(d.Repos), note))
		}
		row(i, repos.Tilde(d.Path), note)
	}
	row(len(dests), "another folder…", "type a path")
	if m.clonePathMode {
		b.WriteString("  " + m.clonePath.View() + "\n")
		if p := config.ExpandHome(strings.TrimSpace(m.clonePath.Value())); p != "" {
			if fi, err := os.Stat(p); err != nil {
				b.WriteString("  " + sDim.Render("will be created") + "\n")
			} else if !fi.IsDir() {
				b.WriteString("  " + sErr.Render("not a folder") + "\n")
			}
		}
	}
	where := repos.Tilde(m.cloneRoot())
	if src, err := repos.ParseCloneSource(m.cloneIn.Value()); err == nil {
		where = repos.Tilde(filepath.Join(m.cloneRoot(), src.Name))
	}
	b.WriteString("\n" + sDim.Render("→ ") + where + "\n")
	switch {
	case m.cloneBusy && m.clone != nil:
		el := time.Since(m.clone.started).Round(time.Second)
		b.WriteString("\n" + sAccent.Render(fmt.Sprintf("cloning… %d:%02d", int(el.Minutes()), int(el.Seconds())%60)) + "\n")
		if m.clone.progress != "" {
			b.WriteString(sDim.Render(ansi.Truncate(m.clone.progress, w-6, "…")) + "\n")
		}
		b.WriteString("\n" + sDim.Render("esc cancel"))
	case m.cloneErr != "":
		b.WriteString("\n" + sErr.Render(ansi.Truncate(m.cloneErr, w-6, "…")) + "\n\n" + sDim.Render("enter retry · esc back"))
	default:
		hint := "↑↓ folder · enter clone · esc back"
		if m.clonePathMode {
			hint = "tab complete · enter clone · esc back"
		}
		b.WriteString("\n" + sDim.Render(hint))
	}
	return dialog(m.pickIssue.Identifier+" · clone a repo", b.String(), w)
}
