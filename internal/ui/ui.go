// Package ui is the popup: API key setup, the issue list and the kickoff.
package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/config"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/herdr"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/kickoff"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/repos"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/workspace"
)

type screen int

const (
	scKey screen = iota
	scLoading
	scList
	scType
	scScope
	scRepo      // where to start: the chooser
	scRepoFind  // "another folder…"
	scRepoClone // "clone…"
	scStatus    // "s": change the issue's status
	scSettings  // ",": settings
	scAgent     // "A": start with another agent
	scHelp      // "?": all the keys
	scConfirm   // "start it anyway?"
	scNew       // "n": a new issue
	scRunning
	scDone
)

// Secrets keeps the API keys, one per workspace (secret.Store; tests use a
// fake so they never touch the real keychain).
type Secrets interface {
	Get(ws string) (key, source string)
	Set(ws, key string) (where string, err error)
	Delete(ws string) error
	// Legacy: the key from before workspaces, or LINEAR_API_KEY ("env").
	Legacy() (key, source string)
	DeleteLegacy() error
}

// Deps is what the UI is started with.
type Deps struct {
	Cfg             config.Config
	CfgErr          error
	Secrets         Secrets
	SourceWorkspace string
	StateDir        string
	PluginID        string
	Background      string // "#rrggbb" or "" for the terminal default
	ConfigDir       string // where config.toml is, for the settings screen
	OpenID          string // open this issue in the preview on start (link click)
	StartGoto       bool   // start with the "go to issue" input (by-id action)

	SourceRepo string // the repo the popup was opened from: its workspace is remembered

	// Set by the demo mode: Linear, worktrees and the kickoff itself.
	Connect   func(ws string) linear.API          // a workspace's client, instead of one with its saved key
	Worktrees func(repo string) map[string]string // instead of kickoff.IssueWorktrees
	Kickoff   func(ctx context.Context, env kickoff.Env, id string, opt kickoff.Options, report kickoff.Report) (kickoff.Result, error)
}

type model struct {
	d      Deps
	screen screen
	w, h   int

	key       textinput.Model
	keyErr    string
	keyBusy   bool
	keySource string

	client          linear.API
	viewer          linear.Viewer
	reg             *workspace.Registry // the connected workspaces
	ws              workspace.WS        // the current one
	openWS          string              // URL key of the workspace a link being opened names
	keyAdding       bool                // the key screen adds a workspace: esc goes back
	keyFromSettings bool                // the key screen was opened from settings: esc returns there
	keyMasks        map[string]string   // settings: each workspace's key, masked
	migrate         [2]string           // the pre-workspace key and its source, checked by Init
	gen             int                 // which visit to a workspace: answers carry it
	started         bool                // the link to open / goto palette was set up

	issues    []linear.Issue
	visible   []int // indexes into issues, in display order (sub-issues nested)
	cursor    int   // index into visible
	offset    *int  // first visible line of the list (shared: View renders on a copy)
	pathMemo  *pathMemo
	mode      linear.Mode
	lists     map[linear.Mode][]linear.Issue // last loaded list per mode
	inflight  map[string]bool                // list request running, by listKey
	fetched   map[string]bool                // loaded fresh in this popup, by listKey
	scope     linear.Scope                   // project/team every tab is narrowed to (saved)
	scopes    []linear.Scope                 // what a list can be narrowed to
	scopeErr  string
	scopeIn   textinput.Model // the scope picker's search
	gotoIn    textinput.Model // the search palette's input ("/" and ":")
	going     bool            // the search palette is open
	palCur    int
	palRemote map[string][]linear.IssueBrief // Linear's results, by query
	palAsking map[string]bool                // queries being asked
	gotoOnly  bool                           // opened by the by-id action: cancelling closes the popup
	openNav   bool                           // the issue being opened came from ":" or a link: go where it lives
	selectID  string                         // select this issue once its list arrives
	flash     string                         // a short note in the header ("switched from …")
	gotoErr   string
	scopeCur  int
	loading   bool // the current mode's list is loading
	listErr   string
	repo      string
	wtBranch  map[string]string // identifier → branch of its existing worktree
	wtRepo    map[string]string // identifier → repo of its existing worktree
	rs        *repos.Store      // repos picked per team, pins

	pickIssue   linear.Issue // the issue whose repo is being chosen
	pickOpts    []repos.Option
	pickCur     int
	pinProject  bool
	pinTeam     bool
	pendingRepo string // chosen repo, waiting for the branch type
	wtOnly      bool   // w: the worktree only — no agent, no Linear changes
	// A: this start's agent and launch mode instead of the configured ones
	agentCmd, agentLaunch string
	agentIssue            linear.Issue
	// "start anyway?": the issue, why, and the agent chosen for it (A)
	confIssue             linear.Issue
	confWhy               string
	confAgent, confLaunch string
	confirmed             bool // the yes was given: begin doesn't ask again

	nf         *newForm                   // "n": the new issue form
	teamPeople map[string][]linear.Person // team ID → its members, for the form
	newOpts    *linear.NewIssueOptions    // its teams and projects, loaded once per workspace
	agentOpts  []agentOpt
	agentCur   int

	teamStates map[string][]linear.State // team ID → workflow states, by position
	stIssue    linear.Issue              // the issue whose status is being changed
	stIn       textinput.Model           // its search field

	setCur        int             // settings: the cursor row
	setEditing    string          // settings: the row being edited inline
	setIn         textinput.Model // settings: its text field
	setErr        string
	keyMask       string       // settings: the API key, masked, read when the screen opens
	pick          *settingPick // settings: an open picker (branch name, agent)
	stCur         int
	stBusy        bool
	stErr         string
	findIn        textinput.Model
	cloneIn       textinput.Model // "clone…": the URL
	cloneBusy     bool
	cloneErr      string
	clone         *cloneRun // the clone in progress
	cloneDest     int       // the chosen destination folder
	clonePathMode bool      // "another folder…": typing a path
	clonePath     textinput.Model
	navRootsCache []string // herdr-navigator's roots; nil = not read yet
	found         []string // repos found on disk, for "another folder…"
	foundBusy     bool
	findCur       int
	depth         map[int]int   // issues index → nesting under a listed parent
	pv            *previewState // preview scroll (shared: View renders on a copy)
	subFocus      bool          // the cursor is in the preview's sub-issue list
	subCur        int           // selected sub-issue
	drill         []string      // sub-issues opened from the preview (a stack)
	opening       string        // sub-issue being fetched to open

	// Comments, sub-issues and the parent's description aren't in the list
	// query; they're fetched for the issue the cursor rests on. Maps are
	// shared across model copies.
	details   map[string]linear.Issue // identifier → the full issue
	detWanted map[string]bool         // fetch scheduled or in flight
	detFailed map[string]bool         // fetch failed; retried on refresh (r)

	types   []string
	typeCur int
	pending string // identifier being started

	progress []string
	progCh   chan tea.Msg
	result   kickoff.Result
	runErr   error
}

// previewState is shared across model copies: View clamps and adjusts it.
type previewState struct {
	scroll, max int
	follow      bool // bring the selected sub-issue into view on the next render
}

type (
	openMsg struct {
		id     string
		issue  linear.Issue
		err    error
		viewer linear.Viewer // fetched along when it wasn't known yet
		ws     string        // the workspace it was found in
	}
	keyCheckedMsg struct {
		viewer linear.Viewer
		key    string
		err    error
		src    string // "" typed; else where a found key came from
	}
	viewerMsg struct {
		viewer linear.Viewer
		err    error
	}
	scopesMsg struct {
		scopes []linear.Scope
		err    error
	}
	issuesMsg struct {
		key    string // listKey the request was for
		mode   linear.Mode
		issues []linear.Issue
		err    error
	}
	worktreesMsg struct {
		repo   string            // the source workspace's repo
		has    map[string]string // identifier → worktree branch
		repoOf map[string]string // identifier → worktree repo
	}
	detailsTickMsg string // debounce: fetch comments if still selected
	detailsMsg     struct {
		id    string
		issue linear.Issue
		err   error
	}
	flashDoneMsg struct{}
	progressMsg  string
	doneMsg      struct {
		res kickoff.Result
		err error
	}
)

// Run starts the popup.
func Run(d Deps) error {
	if d.Background != "" {
		// OSC 11 sets the terminal's default background, so every cell —
		// including ones after a style reset — gets it; OSC 111 restores it.
		fmt.Print("\x1b]11;" + d.Background + "\x1b\\")
		defer fmt.Print("\x1b]111\x1b\\")
		applyBackground(d.Background)
	}
	_, err := tea.NewProgram(newModel(d), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

func newModel(d Deps) model {
	k := textinput.New()
	k.Placeholder = "lin_api_…"
	k.EchoMode = textinput.EchoPassword
	k.EchoCharacter = '•'
	k.CharLimit = 200
	k.Width = 50
	gi := textinput.New()
	gi.Prompt = "⌕ "
	gi.Placeholder = "issue title, ID or link"
	gi.CharLimit = 300
	si := textinput.New()
	si.Prompt = "/ "
	si.Placeholder = "workspace, team or project"
	m := model{d: d, key: k, palRemote: map[string][]linear.IssueBrief{}, palAsking: map[string]bool{}, scopeIn: si, gotoIn: gi, findIn: newFindInput(), cloneIn: newCloneInput(), clonePath: newClonePathInput(),
		rs: repos.Load(d.StateDir), wtRepo: map[string]string{}, teamStates: map[string][]linear.State{}, teamPeople: map[string][]linear.Person{}, stIn: newStatusInput(), setIn: newSettingInput(), wtBranch: map[string]string{}, offset: new(int), pv: &previewState{}, pathMemo: &pathMemo{},
		mode:  linear.ParseMode(d.Cfg.DefaultMode),
		lists: map[linear.Mode][]linear.Issue{}, inflight: map[string]bool{}, fetched: map[string]bool{},
		details: map[string]linear.Issue{}, detWanted: map[string]bool{}, detFailed: map[string]bool{}}

	m.reg = workspace.Load(d.StateDir)
	m.openWS = linear.LinkWorkspace(d.OpenID)
	id := m.reg.For(d.SourceRepo)
	if w, ok := m.reg.ByURLKey(m.openWS); ok { // a link: straight to its workspace
		id = w.ID
	}
	switch {
	case id == "": // none connected yet
		if d.Secrets != nil && d.Connect == nil {
			if key, src := d.Secrets.Legacy(); key != "" {
				m.migrate = [2]string{key, src} // Init checks it: which workspace is it?
				m.screen = scLoading
				return m
			}
		}
		m.screen = scKey
		m.key.Focus()
		return m
	case !m.useWorkspace(id):
		m = m.toKey("No key saved for " + m.ws.Name + " — paste it to reconnect")
		return m
	}
	m.screen = scLoading
	if m.rs.Prune() { // repos that are gone (or never were) leave the chooser too
		_ = m.rs.Save()
	}
	if len(m.issues) > 0 {
		m.screen = scList
	}
	m.loading = true
	m.started = true
	if id, ok := linear.ParseIdentifier(d.OpenID); ok {
		m.opening, m.openNav = id, true // fetched by Init; navigated to on arrival
	}
	if d.StartGoto {
		m.openPalette()
		m.gotoOnly = true
	}
	return m
}

// startup: the link to open or the goto palette, for a popup that had to ask
// for a key first.
func (m *model) startup() tea.Cmd {
	m.started = true
	var cmds []tea.Cmd
	if id, ok := linear.ParseIdentifier(m.d.OpenID); ok {
		m.opening, m.openNav = id, true
		cmds = append(cmds, m.fetchOpen(id))
	}
	if m.d.StartGoto {
		cmds = append(cmds, m.openPalette())
		m.gotoOnly = true
	}
	return tea.Batch(cmds...)
}

func (m model) Init() tea.Cmd {
	if m.migrate[0] != "" {
		return checkKey(m.migrate[0], m.migrate[1])
	}
	if m.screen == scKey {
		return textinput.Blink
	}
	cmds := []tea.Cmd{m.fetchViewer(), m.fetchIssues(), m.fetchWorktrees()}
	if m.opening != "" { // a link click (set up in newModel: Init gets a copy)
		cmds = append(cmds, m.fetchOpen(m.opening))
	}
	if m.going {
		cmds = append(cmds, textinput.Blink)
	}
	return tea.Batch(cmds...)
}

// openByID shows an issue: selects it if it's in the list, otherwise loads it
// into the preview, where enter starts it like any other.
func (m *model) openByID(id string) tea.Cmd {
	for i, idx := range m.visible {
		if m.issues[idx].Identifier == id {
			m.move(i - m.cursor)
			return nil
		}
	}
	m.drill, m.subFocus = nil, false
	m.pv.scroll = 0
	if _, ok := m.details[id]; ok {
		m.drill = []string{id}
		return nil
	}
	m.opening, m.openNav = id, true
	return m.fetchOpen(id)
}

// navigateTo shows an issue opened by ID or link where it lives: if it's
// outside the current scope, the scope moves to its team (and is remembered);
// the tab is the one that lists it, and the cursor lands on it when the list
// arrives. Until then — or if no tab lists it (done, canceled) — it's shown
// on its own in the preview.
func (m *model) navigateTo(is linear.Issue) tea.Cmd {
	m.drill, m.subFocus, m.pv.scroll = []string{is.Identifier}, false, 0
	tab := is.TabFor(m.viewer.ID)
	if tab == "" {
		return m.flashNote(is.Identifier + " is " + strings.ToLower(is.State.Name) + " — not in any list")
	}
	var cmds []tea.Cmd
	if !is.InScope(m.scope) {
		from := m.scope.Name
		m.scope = is.TeamScope()
		m.saveScopes()
		m.lists = map[linear.Mode][]linear.Issue{}
		for _, md := range linear.Modes {
			if cached := m.loadCache(md); cached != nil {
				m.lists[md] = cached
			}
		}
		m.mode = "" // force switchMode to load the tab under the new scope
		cmds = append(cmds, m.flashNote("switched from "+from))
	}
	m.selectID = is.Identifier
	cmds = append(cmds, m.switchMode(tab))
	m.selectPending()
	return tea.Batch(cmds...)
}

// selectPending puts the cursor on selectID if the list has it.
func (m *model) selectPending() {
	if m.selectID == "" {
		return
	}
	for i, idx := range m.visible {
		if m.issues[idx].Identifier == m.selectID {
			m.cursor = i
			m.drill, m.selectID = nil, ""
			return
		}
	}
}

func (m *model) flashNote(s string) tea.Cmd {
	m.flash = s
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return flashDoneMsg{} })
}

// ---- commands

func (m model) fetchViewer() tea.Cmd {
	c := m.client
	return m.tag(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		v, err := c.Viewer(ctx)
		return viewerMsg{v, err}
	})
}

// fetchIssues loads the current mode's list (one request per mode at a time).
func (m *model) fetchIssues() tea.Cmd {
	m.loading = true
	return m.fetchList(m.mode)
}

// fetchList loads a mode's list under the current scope, unless it's already
// on its way.
func (m *model) fetchList(mode linear.Mode) tea.Cmd {
	scope, key, c := m.scope, m.listKey(mode), m.client
	if m.inflight[key] {
		return nil
	}
	m.inflight[key] = true
	return m.tag(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		is, err := c.Issues(ctx, mode, scope)
		return issuesMsg{key, mode, is, err}
	})
}

// prefetchOtherTabs loads the other tabs in the background once the current
// one is in: Linear has no issue count, so this is how their tab counts get
// filled — and switching to them is instant.
func (m *model) prefetchOtherTabs() tea.Cmd {
	var cmds []tea.Cmd
	for _, md := range linear.Modes {
		if md != m.mode && !m.fetched[m.listKey(md)] {
			cmds = append(cmds, m.fetchList(md))
		}
	}
	return tea.Batch(cmds...)
}

// listKey identifies a mode's list under its current scope.
func (m model) listKey(mode linear.Mode) string {
	if k := m.scope.Key(); k != "" {
		return string(mode) + "-" + k
	}
	return string(mode)
}

// setScope narrows every tab to a project or team and remembers the choice.
func (m *model) setScope(s linear.Scope) tea.Cmd {
	m.scope = s
	m.saveScopes()
	m.lists = map[linear.Mode][]linear.Issue{}
	for _, md := range linear.Modes { // cached lists under the new scope: tab counts
		if cached := m.loadCache(md); cached != nil {
			m.lists[md] = cached
		}
	}
	is := m.lists[m.mode]
	m.cursor, *m.offset = 0, 0
	m.listErr = ""
	m.setIssues(is)
	m.cursor = 0
	m.drill, m.subFocus = nil, false
	return m.fetchIssues()
}

func (m model) fetchScopes() tea.Cmd {
	c := m.client
	return m.tag(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		s, err := c.Scopes(ctx)
		return scopesMsg{s, err}
	})
}

// visibleScopes: the workspaces, then "all", then the teams and projects
// matching the picker's search.
func (m model) visibleScopes() []linear.Scope {
	q := strings.ToLower(strings.TrimSpace(m.scopeIn.Value()))
	out := append(m.workspaceScopes(q), linear.Scope{Name: "All"})
	for _, s := range m.scopes {
		if q == "" || strings.Contains(strings.ToLower(s.Name), q) {
			out = append(out, s)
		}
	}
	return out
}

// switchMode shows another list: cached instantly, refreshed once per popup.
func (m *model) switchMode(mode linear.Mode) tea.Cmd {
	if mode == m.mode {
		return nil
	}
	m.mode = mode
	m.saveScopes() // remembers the tab too
	is, ok := m.lists[mode]
	if !ok {
		is = m.loadCache(mode)
		m.lists[mode] = is
	}
	m.cursor, *m.offset = 0, 0
	m.listErr = ""
	m.setIssues(is)
	m.cursor = 0
	m.loading = m.inflight[m.listKey(mode)]
	if m.fetched[m.listKey(mode)] {
		return nil
	}
	return m.fetchIssues()
}

// fetchWorktrees finds the issues that already have a worktree, in any list:
// on open, on refresh and when the key is set — not per tab or scope, since
// it costs a herdr call per known repo.
func (m model) fetchWorktrees() tea.Cmd {
	d := m.d
	known := m.rs.Known()
	return func() tea.Msg {
		// Worktrees in every repo the plugin knows, plus this workspace's.
		src, _ := kickoff.RepoFor(kickoff.Env{Cfg: d.Cfg, SourceWorkspace: d.SourceWorkspace}, "")
		msg := worktreesMsg{repo: src, has: map[string]string{}, repoOf: map[string]string{}}
		seen := map[string]bool{}
		for _, repo := range append([]string{src}, known...) {
			if repo == "" || seen[repo] {
				continue
			}
			seen[repo] = true
			list := kickoff.IssueWorktrees
			if d.Worktrees != nil {
				list = d.Worktrees
			}
			for id, br := range list(repo) {
				msg.has[id], msg.repoOf[id] = br, repo
			}
		}
		return msg
	}
}

// wantDetails schedules a comments fetch for the selected issue after a
// short pause, so scrolling through the list doesn't fire a request per row.
func (m model) wantDetails() tea.Cmd {
	id := m.selectedID()
	if m.screen != scList || m.client == nil || id == "" || m.detWanted[id] || m.detFailed[id] {
		return nil
	}
	if _, ok := m.details[id]; ok {
		return nil
	}
	if is, _ := m.selected(); !needsDetails(is) {
		return nil
	}
	m.detWanted[id] = true
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return detailsTickMsg(id) })
}

func (m model) fetchDetails(id string) tea.Cmd {
	c := m.client
	return m.tag(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		is, err := c.Issue(ctx, id)
		return detailsMsg{id, is, err}
	})
}

// checkKey asks Linear whose key it is; src is set for a key found rather
// than typed (the pre-workspace one, or LINEAR_API_KEY).
func checkKey(key, src string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		v, err := linear.New(key).Viewer(ctx)
		return keyCheckedMsg{v, key, err, src}
	}
}

func waitProgress(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// notify reports a kickoff's outcome as a herdr notification, for when the
// popup is already hidden. A var so tests don't post real notifications.
var notify = func(identifier string, res kickoff.Result, err error) {
	title, body := identifier, strings.Join(res.Notes, "\n")
	if err != nil {
		title += " · kickoff failed"
		body = strings.TrimSpace(err.Error() + "\n" + body)
	} else {
		title += " · " + res.Branch
	}
	_ = herdr.Run(nil, "notification", "show", title, "--body", body)
}

func (m *model) startKickoff(identifier, typ, repo string) tea.Cmd {
	if m.reg != nil && m.ws.ID != "" && repo != "" { // this repo goes with this workspace now
		m.reg.Use(repo, m.ws.ID)
		_ = m.reg.Save()
	}
	m.screen = scRunning
	m.pending = identifier
	m.progress = nil
	m.progCh = make(chan tea.Msg, 16)
	env := kickoff.Env{
		Cfg: m.d.Cfg, Client: m.client, Viewer: m.viewer,
		SourceWorkspace: m.d.SourceWorkspace, StateDir: m.d.StateDir, PluginID: m.d.PluginID,
	}
	ch, only, agent, launch := m.progCh, m.wtOnly, m.agentCmd, m.agentLaunch
	start := kickoff.Start
	if m.d.Kickoff != nil {
		start = m.d.Kickoff
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		opt := kickoff.Options{Type: typ, Repo: repo, WorktreeOnly: only, AgentCmd: agent, Launch: launch}
		res, err := start(ctx, env, identifier, opt, func(s string) { ch <- progressMsg(s) })
		ch <- doneMsg{res, err}
	}()
	return waitProgress(ch)
}

// ---- cache

func (m model) cachePath(mode linear.Mode) string {
	return filepath.Join(m.wsDir(), "issues-"+m.listKey(mode)+".json")
}

// scopeState is saved as scope.json: the chosen scope per mode, and the
// options last fetched (so the picker opens instantly).
type scopeState struct {
	Chosen  linear.Scope   `json:"scope"`
	Options []linear.Scope `json:"options"`
	Tab     linear.Mode    `json:"tab"` // the tab last looked at: the popup reopens on it
}

func (m model) scopePath() string { return filepath.Join(m.wsDir(), "scope.json") }

func (m *model) loadScopes() {
	b, err := os.ReadFile(m.scopePath())
	if err != nil {
		return
	}
	var st scopeState
	if json.Unmarshal(b, &st) != nil {
		return
	}
	linear.Sanitize(&st)
	m.scope = st.Chosen
	m.scopes = st.Options
	if st.Tab != "" {
		m.mode = linear.ParseMode(string(st.Tab))
	}
}

func (m model) saveScopes() {
	if b, err := json.Marshal(scopeState{m.scope, m.scopes, m.mode}); err == nil {
		_ = os.MkdirAll(m.wsDir(), 0o700)
		_ = os.WriteFile(m.scopePath(), b, 0o600)
	}
}

func (m model) loadCache(mode linear.Mode) []linear.Issue {
	b, err := os.ReadFile(m.cachePath(mode))
	if err != nil {
		return nil
	}
	var is []linear.Issue
	_ = json.Unmarshal(b, &is)
	linear.Sanitize(&is) // a cache written before sanitizing existed
	return is
}

func (m model) saveCache(mode linear.Mode, is []linear.Issue) {
	if m.keySource == "env" && m.viewer.ID == "" { // whose lists these are isn't known yet
		return
	}
	if b, err := json.Marshal(is); err == nil {
		_ = os.MkdirAll(m.wsDir(), 0o700)
		_ = os.WriteFile(m.cachePath(mode), b, 0o600)
	}
}

// ---- list state

func (m *model) setIssues(is []linear.Issue) {
	sel := m.selectedID()
	linear.SortIssues(is)
	m.issues = is
	m.applyFilter()
	for i, idx := range m.visible {
		if m.issues[idx].Identifier == sel {
			m.cursor = i
		}
	}
}

func (m *model) applyFilter() {
	m.visible = m.visible[:0]
	for i := range m.issues {
		m.visible = append(m.visible, i)
	}
	m.visible, m.depth = nestSubIssues(m.issues, m.visible)
	if m.cursor >= len(m.visible) {
		m.cursor = max(0, len(m.visible)-1)
	}
	m.pv.scroll = 0
}

// nestSubIssues moves each sub-issue right under its parent when the parent is
// in the list too, and reports how deep each one sits. Sub-issues whose parent
// isn't listed keep their place.
func nestSubIssues(issues []linear.Issue, order []int) ([]int, map[int]int) {
	pos := map[string]int{} // identifier → issues index, for listed issues
	for _, i := range order {
		pos[issues[i].Identifier] = i
	}
	kids := map[int][]int{}
	listedParent := func(i int) (int, bool) {
		if p := issues[i].Parent; p != nil {
			pi, ok := pos[p.Identifier]
			return pi, ok && pi != i
		}
		return 0, false
	}
	for _, i := range order {
		if pi, ok := listedParent(i); ok {
			kids[pi] = append(kids[pi], i)
		}
	}
	out := make([]int, 0, len(order))
	depth := map[int]int{}
	seen := map[int]bool{}
	var add func(i, d int)
	add = func(i, d int) {
		if seen[i] {
			return
		}
		seen[i] = true
		out = append(out, i)
		if d > 0 {
			depth[i] = d
		}
		for _, k := range kids[i] {
			add(k, d+1)
		}
	}
	for _, i := range order {
		if _, ok := listedParent(i); !ok {
			add(i, 0)
		}
	}
	for _, i := range order { // a parent cycle would leave issues out; keep them
		add(i, 0)
	}
	return out, depth
}

func (m model) selected() (linear.Issue, bool) {
	if m.cursor < 0 || m.cursor >= len(m.visible) {
		return linear.Issue{}, false
	}
	return m.issues[m.visible[m.cursor]], true
}

// current is the issue the preview shows and enter starts: an opened
// sub-issue, else the list's selection.
func (m model) current() (linear.Issue, bool) {
	if n := len(m.drill); n > 0 {
		if is, ok := m.details[m.drill[n-1]]; ok {
			return is, true
		}
	}
	return m.selected()
}

// currentSubs are the current issue's loaded sub-issues.
func (m model) currentSubs() []linear.IssueRef {
	is, ok := m.current()
	if !ok {
		return nil
	}
	if full, ok := m.details[is.Identifier]; ok {
		return full.SubIssues()
	}
	return nil
}

// openSub shows a sub-issue in the preview, fetching it first if needed.
func (m *model) openSub(id string) tea.Cmd {
	m.subFocus = false
	if _, ok := m.details[id]; ok {
		m.drill = append(m.drill, id)
		m.pv.scroll = 0
		return nil
	}
	m.opening, m.openNav = id, false // inside the preview: no scope change
	c := m.client
	return m.tag(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		is, err := c.Issue(ctx, id)
		return openMsg{id: id, issue: is, err: err}
	})
}

func (m *model) scrollPreview(d int) {
	m.pv.scroll = max(0, min(m.pv.scroll+d, m.pv.max))
}

func (m model) selectedID() string {
	if is, ok := m.selected(); ok {
		return is.Identifier
	}
	return ""
}

// ---- update

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	if nm, ok := next.(model); ok {
		if c := nm.wantDetails(); c != nil {
			cmd = tea.Batch(cmd, c)
		}
	}
	return next, cmd
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case detailsTickMsg:
		id := string(msg)
		if m.selectedID() != id {
			delete(m.detWanted, id) // moved on; refetch if they come back
			return m, nil
		}
		return m, m.fetchDetails(id)

	case openMsg:
		if m.opening == msg.id {
			m.opening = ""
			if msg.err != nil {
				m.listErr = "couldn't open " + msg.id + ": " + errText(msg.err)
				return m, nil
			}
			if msg.ws != "" && msg.ws != m.ws.ID { // found in another workspace: go there
				cmd := m.switchWorkspace(msg.ws, false)
				m.viewer = msg.viewer
				m.details[msg.id] = msg.issue
				return m, tea.Batch(cmd, m.navigateTo(msg.issue))
			}
			if msg.viewer.ID != "" && m.viewer.ID == "" {
				m.viewer = msg.viewer
			}
			m.details[msg.id] = msg.issue
			if m.openNav {
				m.openNav = false
				return m, m.navigateTo(msg.issue)
			}
			m.drill = append(m.drill, msg.id)
			m.pv.scroll = 0
		}
		return m, nil

	case statesMsg:
		if msg.err != nil {
			m.stErr = errText(msg.err)
			if m.nf != nil {
				m.nf.err = "couldn't load the team's statuses: " + errText(msg.err)
			}
			return m, nil
		}
		linear.SortWorkflow(msg.states)
		m.teamStates[msg.teamID] = msg.states
		if m.nf != nil && m.nf.opts != nil { // the new issue form waits for them: Todo
			m.nfDefaultStatus()
		}
		if msg.teamID == m.stIssue.Team.ID {
			m.stCur = m.stCurrentIndex()
		}
		return m, nil

	case newOptsMsg:
		if msg.err != nil {
			if m.nf != nil {
				m.nf.err = "couldn't load teams and projects: " + errText(msg.err) + ". esc, then n to try again"
			}
			return m, nil
		}
		o := msg.opts
		m.newOpts = &o
		if m.nf != nil && m.nf.opts == nil {
			m.nf.opts = m.newOpts
			return m, m.nfDefaults()
		}
		return m, nil

	case peopleMsg:
		if msg.err == nil {
			m.teamPeople[msg.teamID] = msg.people
		} else if m.nf != nil {
			m.nf.err = "couldn't load the team's people: " + errText(msg.err)
		}
		return m, nil

	case createdMsg:
		return m, m.created(msg)

	case palTickMsg:
		return m, m.palSearch(string(msg))

	case palResultMsg:
		m.palResult(msg)
		return m, nil

	case statusSetMsg:
		return m, m.statusSet(msg)

	case editorDoneMsg:
		m.editorDone(msg)
		return m, nil

	case clonedMsg:
		return m, m.cloned(msg)

	case cloneProgressMsg:
		if m.clone == nil {
			return m, nil
		}
		m.clone.progress = string(msg)
		return m, waitProgress(m.clone.ch)

	case cloneTickMsg:
		if m.clone == nil {
			return m, nil
		}
		return m, cloneTick()

	case foundMsg:
		m.found, m.foundBusy = msg, false
		return m, nil

	case flashDoneMsg:
		m.flash = ""
		return m, nil

	case detailsMsg:
		delete(m.detWanted, msg.id)
		if msg.err != nil {
			m.detFailed[msg.id] = true
		} else {
			m.details[msg.id] = msg.issue
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case keyCheckedMsg:
		m.migrate = [2]string{}
		return m.keyChecked(msg)

	case wsMsg:
		if msg.ws != m.ws.ID || msg.gen != m.gen { // asked before a switch: stale
			return m, nil
		}
		return m.Update(msg.msg)

	case viewerMsg:
		if errors.Is(msg.err, linear.ErrUnauthorized) {
			return m.toKey("Linear rejected the saved key for " + m.ws.Name + " — paste a new one"), textinput.Blink
		}
		if msg.err == nil {
			if !m.viewerChecked(msg.viewer) {
				m.client = nil
				m.gen++ // its other answers (issues) are another workspace's: drop them
				return m.toKey("That key is for " + msg.viewer.Organization.Name + ", not " + m.ws.Name + " — paste " + m.ws.Name + "'s"), textinput.Blink
			}
			m.viewer = msg.viewer
		}
		return m, nil

	case scopesMsg:
		if msg.err != nil {
			m.scopeErr = errText(msg.err)
		} else {
			m.scopes, m.scopeErr = msg.scopes, ""
			m.saveScopes()
		}
		return m, nil

	case issuesMsg:
		delete(m.inflight, msg.key)
		if errors.Is(msg.err, linear.ErrUnauthorized) {
			return m.toKey("Linear rejected the saved key — paste a new one"), textinput.Blink
		}
		if msg.key != m.listKey(msg.mode) { // the scope changed since: drop it
			return m, nil
		}
		if msg.err == nil {
			m.lists[msg.mode] = msg.issues
			m.fetched[msg.key] = true
			m.saveCache(msg.mode, msg.issues)
		}
		if msg.mode != m.mode { // a background list, or one for a tab we've since left
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.listErr = errText(msg.err)
		} else {
			m.listErr = ""
			m.setIssues(msg.issues)
			m.selectPending()
		}
		if m.screen == scLoading {
			m.screen = scList
		}
		return m, m.prefetchOtherTabs()

	case worktreesMsg:
		m.repo = msg.repo
		if msg.has != nil {
			m.wtBranch, m.wtRepo = msg.has, msg.repoOf
		}
		return m, nil

	case progressMsg:
		m.progress = append(m.progress, string(msg))
		return m, waitProgress(m.progCh)

	case doneMsg:
		m.result, m.runErr = msg.res, msg.err
		if msg.err == nil && len(msg.res.Notes) == 0 {
			return m, tea.Quit
		}
		if msg.res.Workspace != "" {
			// The kickoff focused a workspace, and herdr hides the popup on a
			// focus change while this process lives on: a result screen here
			// would never be seen and would wait for a key forever. Say it in
			// a notification instead, and exit.
			notify(m.pending, msg.res, msg.err)
			return m, tea.Quit
		}
		m.screen = scDone
		return m, nil

	case tea.MouseMsg:
		if m.screen == scList {
			overPreview := msg.X > m.leftWidth()+1
			switch {
			case msg.Button == tea.MouseButtonWheelUp && overPreview:
				m.scrollPreview(-3)
			case msg.Button == tea.MouseButtonWheelDown && overPreview:
				m.scrollPreview(3)
			case msg.Button == tea.MouseButtonWheelUp:
				m.move(-1)
			case msg.Button == tea.MouseButtonWheelDown:
				m.move(1)
			}
		}
		return m, nil

	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m model) toKey(why string) model {
	m.screen = scKey
	m.keyAdding, m.keyFromSettings = false, false
	m.keyErr = why
	m.key.SetValue("")
	m.key.Focus()
	m.loading = false
	return m
}

func (m *model) move(d int) {
	if len(m.visible) == 0 {
		return
	}
	m.cursor = min(max(m.cursor+d, 0), len(m.visible)-1)
	m.pv.scroll = 0
	m.drill, m.subFocus, m.opening = nil, false, ""
}

func (m model) onKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.screen {
	case scKey:
		switch k.String() {
		case "esc":
			if m.client != nil {
				m.screen = scList
				if m.keyFromSettings { // back where it was asked from
					m.screen = scSettings
				}
				m.keyAdding, m.keyFromSettings = false, false
				m.key.Blur()
				return m, nil
			}
			return m, tea.Quit
		case "enter":
			v := strings.TrimSpace(m.key.Value())
			if v == "" || m.keyBusy {
				return m, nil
			}
			if m.d.Connect != nil { // the demo: nothing goes to Linear
				m.keyErr = "The demo can't connect a real workspace"
				return m, nil
			}
			m.keyBusy, m.keyErr = true, ""
			return m, checkKey(v, "")
		}
		var cmd tea.Cmd
		m.key, cmd = m.key.Update(k)
		return m, cmd

	case scLoading:
		if k.String() == "q" || k.String() == "esc" {
			return m, tea.Quit
		}

	case scList:
		if m.going {
			return m.onPaletteKey(k)
		}
		switch k.String() { // preview scrolling works wherever the cursor is
		case "J", "shift+down":
			m.scrollPreview(1)
			return m, nil
		case "K", "shift+up":
			m.scrollPreview(-1)
			return m, nil
		case "ctrl+d":
			m.scrollPreview(m.h / 2)
			return m, nil
		case "ctrl+u":
			m.scrollPreview(-m.h / 2)
			return m, nil
		}
		if m.subFocus { // choosing a sub-issue in the preview
			subs := m.currentSubs()
			switch k.String() {
			case "up", "k":
				m.subCur = max(0, m.subCur-1)
				m.pv.follow = true
			case "down", "j":
				m.subCur = min(len(subs)-1, m.subCur+1)
				m.pv.follow = true
			case "enter", "right", "l":
				if m.subCur < len(subs) {
					return m, m.openSub(subs[m.subCur].Identifier)
				}
			case "left", "h", "esc":
				m.subFocus = false
			case "q", "ctrl+c":
				return m, tea.Quit
			}
			return m, nil
		}
		switch k.String() {
		case "right", "l":
			if len(m.currentSubs()) > 0 {
				m.subFocus, m.subCur = true, 0
				m.pv.follow = true
			}
			return m, nil
		case "left", "h":
			if n := len(m.drill); n > 0 {
				m.drill = m.drill[:n-1]
				m.pv.scroll = 0
			}
			return m, nil
		case "esc":
			if n := len(m.drill); n > 0 {
				m.drill = m.drill[:n-1]
				m.pv.scroll = 0
				return m, nil
			}
			return m, tea.Quit
		case "q":
			return m, tea.Quit
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "pgup":
			m.move(-10)
		case "pgdown":
			m.move(10)
		case "home":
			m.move(-len(m.visible))
		case "end", "G":
			m.move(len(m.visible))
		case "/", ":":
			return m, m.openPalette()
		case "n":
			return m, m.openNewIssue()
		case "r":
			clear(m.details)
			clear(m.detFailed)
			return m, tea.Batch(m.fetchIssues(), m.fetchWorktrees())
		case "s":
			return m, m.openStatus()
		case "R": // choose the repo, even a pinned one
			if is, ok := m.current(); ok && m.client != nil {
				m.wtOnly, m.agentCmd, m.agentLaunch = false, "", ""
				m.openChooser(is)
			}
			return m, nil
		case "p":
			m.screen = scScope
			m.scopeIn.SetValue("")
			m.scopeIn.Focus()
			m.scopeCur = 0
			for i, s := range m.visibleScopes() { // start on the current choice
				if s.Kind != kindWorkspace && s.Kind != kindAddWS && s.ID == m.scope.ID {
					m.scopeCur = i
				}
			}
			return m, tea.Batch(textinput.Blink, m.fetchScopes())
		case "tab":
			return m, m.switchMode(linear.Modes[(m.modeIndex()+1)%len(linear.Modes)])
		case "shift+tab":
			return m, m.switchMode(linear.Modes[(m.modeIndex()+len(linear.Modes)-1)%len(linear.Modes)])
		case "1", "2", "3", "4":
			return m, m.switchMode(linear.Modes[int(k.String()[0]-'1')])
		case ",":
			return m, m.openSettings()
		case "o":
			if is, ok := m.current(); ok {
				openURL(is.URL)
			}
		case "g": // its pull request
			if is, ok := m.current(); ok {
				if pr, ok := mainPR(m.prs(is)); ok {
					openURL(pr.URL)
				} else {
					return m, m.flashNote("no pull request linked to " + is.Identifier)
				}
			}
		case "y":
			if is, ok := m.current(); ok {
				copyText(is.URL)
			}
		case "enter":
			is, ok := m.current()
			if !ok || m.client == nil {
				return m, nil
			}
			m.agentCmd, m.agentLaunch = "", ""
			return m, m.begin(is, false)
		case "w": // open or create the worktree, nothing else
			is, ok := m.current()
			if !ok || m.client == nil {
				return m, nil
			}
			m.agentCmd, m.agentLaunch = "", ""
			return m, m.begin(is, true)
		case "A": // start with another agent, this time
			m.openAgents()
			return m, nil
		case "?":
			m.screen = scHelp
			return m, nil
		}

	case scScope:
		vis := m.visibleScopes()
		switch k.String() {
		case "esc":
			m.screen = scList
			m.scopeIn.Blur()
			return m, nil
		case "up", "ctrl+p":
			m.scopeCur = max(0, m.scopeCur-1)
			return m, nil
		case "down", "ctrl+n":
			m.scopeCur = min(len(vis)-1, m.scopeCur+1)
			return m, nil
		case "enter":
			m.screen = scList
			m.scopeIn.Blur()
			if m.scopeCur >= len(vis) {
				return m, nil
			}
			switch s := vis[m.scopeCur]; {
			case s.Kind == kindAddWS:
				return m.toAddKey()
			case s.Kind == kindWorkspace:
				if s.ID != m.ws.ID {
					return m, m.switchWorkspace(s.ID, true)
				}
			case s.ID != m.scope.ID:
				return m, m.setScope(s)
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.scopeIn, cmd = m.scopeIn.Update(k)
		m.scopeCur = min(m.scopeCur, max(0, len(m.visibleScopes())-1))
		return m, cmd

	case scStatus:
		return m.onStatusKey(k)
	case scSettings:
		return m.onSettingsKey(k)
	case scRepo:
		return m.onRepoKey(k)
	case scRepoFind:
		return m.onFindKey(k)
	case scRepoClone:
		return m.onCloneKey(k)

	case scAgent:
		return m.onAgentKey(k)

	case scConfirm:
		return m.onConfirmKey(k)

	case scNew:
		return m.onNewKey(k)

	case scHelp:
		if s := k.String(); s == "ctrl+c" {
			return m, tea.Quit
		}
		m.screen = scList // any key closes it
		return m, nil

	case scType:
		switch k.String() {
		case "esc", "q":
			m.screen = scList
		case "up", "k":
			m.typeCur = max(0, m.typeCur-1)
		case "down", "j":
			m.typeCur = min(len(m.types)-1, m.typeCur+1)
		case "enter":
			return m, m.startKickoff(m.pending, m.types[m.typeCur], m.pendingRepo)
		}

	case scDone:
		if m.runErr != nil && (k.String() == "esc" || k.String() == "b") {
			m.screen = scList
			return m, nil
		}
		return m, tea.Quit
	}
	return m, nil
}

func errText(err error) string {
	if errors.Is(err, linear.ErrUnauthorized) {
		return "Linear rejected the key. Check that it was copied in full and has not been revoked."
	}
	return err.Error()
}

func openURL(u string) {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	_ = exec.Command(name, u).Start()
}

func copyText(s string) {
	for _, c := range [][]string{{"pbcopy"}, {"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "-ib"}} {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdin = strings.NewReader(s)
		if cmd.Run() == nil {
			return
		}
	}
}

// ---- view (colours and styles: theme.go)

func (m model) View() string {
	if m.w == 0 {
		return ""
	}
	switch m.screen {
	case scKey:
		return m.viewKey()
	case scLoading:
		return pad(sDim.Render("Loading Linear issues…"))
	case scType:
		return m.viewType()
	case scScope:
		return m.viewScope()
	case scStatus:
		return overlayCenter(m.viewList(), m.statusDialog(), m.w)
	case scSettings:
		if m.pick != nil {
			return overlayCenter(m.viewSettings(), m.pickDialog(), m.w)
		}
		return m.viewSettings()
	case scRepo:
		return overlayCenter(m.viewList(), m.repoDialog(), m.w)
	case scAgent:
		return overlayCenter(m.viewList(), m.agentDialog(), m.w)
	case scConfirm:
		return overlayCenter(m.viewList(), m.confirmDialog(), m.w)
	case scNew:
		return overlayCenter(m.viewList(), m.newDialog(), m.w)
	case scHelp:
		return overlayCenter(m.viewList(), m.helpDialog(), m.w)
	case scRepoFind:
		return overlayCenter(m.viewList(), m.findDialog(), m.w)
	case scRepoClone:
		return overlayCenter(m.viewList(), m.cloneDialog(), m.w)
	case scRunning, scDone:
		return m.viewRun()
	}
	return m.viewList()
}

func pad(s string) string { return lipgloss.NewStyle().Padding(1, 2).Render(s) }

// viewScope is the project/team picker for all tabs: "All my teams",
// then Teams and Projects under their own headings.
func (m model) viewScope() string {
	var b strings.Builder
	b.WriteString(sAccent.Render("Show issues from") + "\n\n")
	b.WriteString(m.scopeIn.View() + "\n\n")

	var lines []string
	cur := 0
	group := ""
	for i, s := range m.visibleScopes() {
		if g := scopeGroup(s); g != group || i == 0 {
			switch {
			case g != "":
				if len(lines) > 0 {
					lines = append(lines, "")
				}
				lines = append(lines, "  "+sBold.Render(g))
			case len(lines) > 0: // "all" after the workspaces
				lines = append(lines, "")
			}
			group = g
		}
		indent := "  "
		if group != "" {
			indent = "    "
		}
		name := s.Name
		if s.All() && s.Kind == "" {
			name = "All my teams"
		}
		switch s.Kind {
		case kindWorkspace:
			if s.ID == m.ws.ID {
				name += sDim.Render("  ✓")
			}
		case kindAddWS:
			name = sDim.Render(name)
		default:
			if s.ID == m.scope.ID {
				name += sDim.Render("  ✓")
			}
		}
		if i == m.scopeCur {
			cur = len(lines)
			lines = append(lines, indent[:len(indent)-2]+sSel.Render("› "+name))
		} else {
			lines = append(lines, indent+name)
		}
	}
	room := max(3, m.h-10)
	start := max(0, min(cur-room/2, len(lines)-room))
	for i := start; i < len(lines) && i < start+room; i++ {
		b.WriteString(lines[i] + "\n")
	}
	if len(m.scopes) == 0 {
		msg := "loading projects…"
		if m.scopeErr != "" {
			msg = m.scopeErr
		}
		b.WriteString("\n" + sDim.Render(msg) + "\n")
	}
	b.WriteString("\n" + sDim.Render("type to search · ↑↓ choose · enter apply · esc cancel"))
	return pad(b.String())
}

// scopeGroup is the picker heading a scope sits under.
func scopeGroup(s linear.Scope) string {
	switch s.Kind {
	case "team":
		return "Teams"
	case "project":
		return "Projects"
	case kindWorkspace, kindAddWS:
		return "Workspaces"
	}
	return ""
}

// scopeLabel names a scope in the header: "Project: Web & Mobile".
func scopeLabel(s linear.Scope) string {
	if s.Kind == "team" {
		return "Team: " + s.Name
	}
	return "Project: " + s.Name
}

func (m model) viewKey() string {
	var b strings.Builder
	title := "Linear API key"
	if m.keyAdding {
		title = "Add a Linear workspace"
	}
	b.WriteString(sAccent.Render(title) + "\n\n")
	b.WriteString("Create a personal key in the workspace: Linear → Settings → Security & access →\n")
	b.WriteString("Personal API keys. Read and write access to issues is enough. One key per workspace.\n\n")
	b.WriteString(m.key.View() + "\n\n")
	switch {
	case m.keyBusy:
		b.WriteString(sDim.Render("Checking the key…"))
	case m.keyErr != "":
		b.WriteString(sErr.Render(m.keyErr))
	}
	b.WriteString("\n\n" + sDim.Render("The key is stored in Keychain (macOS) / Secret Service (Linux), otherwise in a 0600 file.") + "\n\n")
	b.WriteString(sDim.Render("enter — check and save · esc — cancel"))
	return pad(b.String())
}

func (m model) viewType() string {
	var b strings.Builder
	b.WriteString(sAccent.Render("Branch type for "+m.pending) + "\n\n")
	for i, t := range m.types {
		line := "  " + t
		if i == m.typeCur {
			line = sSel.Render("› " + t)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + sDim.Render("enter — start · esc — back"))
	return pad(b.String())
}

func (m model) viewRun() string {
	var b strings.Builder
	b.WriteString(sAccent.Render("Kickoff") + "\n\n")
	for _, p := range m.progress {
		b.WriteString(sDim.Render("· "+p) + "\n")
	}
	if m.screen == scDone {
		b.WriteString("\n")
		if m.runErr != nil {
			b.WriteString(sErr.Render("Failed: "+m.runErr.Error()) + "\n\n")
			b.WriteString(sDim.Render("esc — back to list · any key — close"))
		} else {
			b.WriteString(sOK.Render("Done: "+m.result.Branch) + "\n")
			for _, n := range m.result.Notes {
				b.WriteString(sDim.Render("! "+n) + "\n")
			}
			b.WriteString("\n" + sDim.Render("any key — close"))
		}
	}
	return pad(lipgloss.NewStyle().Width(m.w - 4).Render(b.String()))
}

// initials of the assignee ("RS"), "·" if unassigned.
func initials(is linear.Issue) string {
	if is.Assignee == nil {
		return "·"
	}
	var out []rune
	for _, w := range strings.Fields(is.Assignee.Name) {
		if r := []rune(w); len(r) > 0 && len(out) < 2 {
			out = append(out, unicode.ToUpper(r[0]))
		}
	}
	return string(out)
}

var modeTitles = map[linear.Mode]string{linear.ModeMine: "Mine", linear.ModeTeam: "All", linear.ModeBacklog: "Backlog", linear.ModeRecent: "Recent"}

func (m model) modeIndex() int {
	for i, md := range linear.Modes {
		if md == m.mode {
			return i
		}
	}
	return 0
}

// modeTabs renders "Mine  Team  Backlog" with the current one highlighted.
func (m model) modeTabs() string {
	var tabs []string
	for _, md := range linear.Modes {
		label := modeTitles[md]
		if is, ok := m.lists[md]; ok {
			label += " " + fmt.Sprint(len(is))
		}
		if md == m.mode {
			tabs = append(tabs, sTabOn.Render(label))
		} else {
			tabs = append(tabs, sTabOff.Render(label))
		}
	}
	return strings.Join(tabs, " ")
}

// header: mode tabs on the left; who and where on the right, apart from them.
func (m model) header() string {
	tabs := m.modeTabs()
	if s := m.scope; !s.All() { // the list is narrowed: say to what
		tabs += "   " + lipgloss.NewStyle().Foreground(cAccent).Render(scopeLabel(s))
	}
	var ctx []string
	if m.flash != "" {
		ctx = append(ctx, m.flash)
	}
	if m.opening != "" {
		ctx = append(ctx, "opening "+m.opening+"…")
	}
	if m.loading {
		ctx = append(ctx, "refreshing…")
	}
	if m.multiWS() && m.ws.Name != "" {
		ctx = append(ctx, m.ws.Name)
	}
	if m.viewer.Name != "" {
		ctx = append(ctx, m.viewer.Name)
	}

	tabs = strings.Repeat(" ", margin-1) + tabs // the pill's own padding makes up the rest
	// Short of room, the names (workspace, me) go first, the news last.
	right := strings.Join(ctx, " · ")
	gap := m.w - lipgloss.Width(tabs) - lipgloss.Width(right) - margin
	for gap < 2 && len(ctx) > 1 {
		ctx = ctx[:len(ctx)-1]
		right = strings.Join(ctx, " · ")
		gap = m.w - lipgloss.Width(tabs) - lipgloss.Width(right) - margin
	}
	if right == "" || gap < 2 {
		return ansi.Truncate(tabs, m.w, "…")
	}
	return tabs + strings.Repeat(" ", gap) + sDim.Render(right)
}

const margin = 2 // left edge of the window to the content

func (m model) leftWidth() int { return max(m.w*45/100, 30) }

// indent shifts every line right by n cells.
func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	return pad + strings.ReplaceAll(s, "\n", "\n"+pad)
}

// footerHints shows the keys that do something where the cursor is.
func (m model) footerHints() string {
	if m.subFocus {
		return "↑↓ choose sub-issue · enter open · ← back · J/K scroll · q quit"
	}
	// The few that matter most here; ? lists the rest.
	h := []string{"enter start", "w worktree", "s status"}
	if len(m.currentSubs()) > 0 {
		h = append(h, "→ sub-issues")
	}
	if len(m.drill) > 0 {
		h = append(h, "← back")
	}
	pick := "p project"
	if m.multiWS() {
		pick = "p workspace"
	}
	h = append(h, "/ search", pick, "? more")
	return strings.Join(h, " · ")
}

func (m model) viewList() string {
	// The popup border already says "Linear" (manifest title).
	header := m.header()
	foot := sBar.Width(m.w).MaxWidth(m.w).Render(ansi.Truncate(m.footerHints(), m.w-4, "…"))
	var warn string
	if m.listErr != "" {
		warn = sErr.Render(ansi.Truncate(m.listErr, m.w-2, "…"))
	} else if m.d.CfgErr != nil {
		warn = sErr.Render(ansi.Truncate(m.d.CfgErr.Error(), m.w-2, "…"))
	} else if ws := m.d.Cfg.Warnings(); len(ws) > 0 {
		warn = sDim.Render("! " + ws[0])
	}

	bodyH := m.h - 3 // header, the gap under it, footer
	if warn != "" {
		bodyH--
	}
	bodyH = max(bodyH, 3)
	leftW := m.leftWidth()
	rightW := max(m.w-leftW-3, 10)

	left := indent(m.renderList(leftW-margin, bodyH), margin)
	right := m.renderPreview(rightW, bodyH)
	sep := sDim.Render(strings.Repeat("│\n", bodyH-1) + "│")
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", sep, " ", right)

	parts := []string{header, ""}
	parts = append(parts, body)
	if warn != "" {
		parts = append(parts, warn)
	}
	parts = append(parts, foot)
	view := strings.Join(parts, "\n")
	if m.going {
		view = overlayCenter(view, m.paletteDialog(), m.w)
	}
	return view
}

// overlayCenter draws box over the middle of view (both multi-line strings),
// keeping view visible around it.
func overlayCenter(view, box string, w int) string {
	lines := strings.Split(view, "\n")
	boxLines := strings.Split(box, "\n")
	bw := lipgloss.Width(box)
	x := max(0, (w-bw)/2)
	y := max(0, (len(lines)-len(boxLines))/2)
	for i, bl := range boxLines {
		if y+i >= len(lines) {
			break
		}
		bg := lines[y+i]
		left := ansi.Truncate(bg, x, "")
		left += strings.Repeat(" ", max(0, x-ansi.StringWidth(left)))
		right := ansi.TruncateLeft(bg, x+bw, "")
		lines[y+i] = left + "\x1b[0m" + bl + "\x1b[0m" + right
	}
	return strings.Join(lines, "\n")
}

func stateDot(s linear.State) string { return stateDotOn(s, lipgloss.NewStyle()) }

// stateDotOn renders the state icon on base (which may carry a background).
func stateDotOn(s linear.State, base lipgloss.Style) string {
	icon := "○"
	switch s.Type {
	case "started":
		icon = "◐"
	case "backlog", "triage":
		icon = "◌"
	case "completed":
		icon = "●"
	case "canceled":
		icon = "⊘"
	}
	if s.Color == "" {
		return base.Render(icon)
	}
	return base.Foreground(lipgloss.Color(readable(s.Color))).Render(icon)
}

func priorityMark(p int, base lipgloss.Style) string {
	switch p {
	case 1:
		return base.Foreground(cErr).Render("!!")
	case 2:
		return base.Foreground(cWarm).Render("! ")
	default:
		return base.Render("  ")
	}
}

// issueRow renders one list row of exactly w cells. Every segment carries base,
// so a background on base spans the whole row despite the inner colour resets.
func (m *model) issueRow(is linear.Issue, grouped bool, depth int, w int, base lipgloss.Style) string {
	sp := base.Render(" ")
	wt := base.Render(" ")
	if m.wtBranch[is.Identifier] != "" {
		wt = base.Foreground(cOK).Render("⎇")
	}
	head := wt + sp
	if !grouped {
		head += stateDotOn(is.State, base) + sp
	}
	head += priorityMark(is.Priority, base) + sp
	if depth > 0 { // a sub-issue under its parent; its state may differ from the group's
		head += base.Render(strings.Repeat("  ", depth-1)) + base.Foreground(cDim).Render("↳") + sp
		if grouped {
			head += stateDotOn(is.State, base) + sp
		}
	}
	head += base.Render(fmt.Sprintf("%-9s ", is.Identifier))
	if m.mode != linear.ModeMine { // whose it is matters outside "mine"
		head += base.Foreground(cDim).Render(fmt.Sprintf("%-3s", initials(is))) + sp
	}
	rest := max(0, w-lipgloss.Width(head))
	title := ansi.Truncate(is.Title, rest, "…")
	title += strings.Repeat(" ", max(0, rest-lipgloss.Width(title)))
	return ansi.Truncate(head+base.Render(title), w, "")
}

func (m *model) renderList(w, h int) string {
	if len(m.visible) == 0 {
		msg := map[linear.Mode]string{
			linear.ModeMine:    "No open issues assigned to you.",
			linear.ModeTeam:    "No active issues here.",
			linear.ModeBacklog: "The backlog is empty.",
			linear.ModeRecent:  "Nothing you created, have or follow changed in the last 30 days.",
		}[m.mode]
		if m.loading {
			msg = "Loading…"
		}
		return lipgloss.NewStyle().Width(w).Height(h).Render(sDim.Render(msg))
	}
	type line struct {
		text  string
		issue int // index into visible, -1 for headers
		idx   int // index into issues
	}
	var lines []line
	lastState := ""
	grouped := true
	for vi, idx := range m.visible {
		is := m.issues[idx]
		if grouped && m.depth[idx] == 0 && is.State.Name != lastState {
			if lastState != "" {
				lines = append(lines, line{"", -1, -1})
			}
			lines = append(lines, line{stateDot(is.State) + " " + sBold.Render(is.State.Name), -1, -1})
			lastState = is.State.Name
		}
		lines = append(lines, line{"", vi, idx})
	}
	cur := 0
	for i, l := range lines {
		if l.issue == m.cursor {
			cur = i
		}
	}
	if cur < *m.offset {
		*m.offset = cur
		if cur > 0 && lines[cur-1].issue == -1 {
			*m.offset = cur - 1
		}
	}
	if cur >= *m.offset+h {
		*m.offset = cur - h + 1
	}
	*m.offset = max(0, min(*m.offset, max(0, len(lines)-h)))
	var out []string
	for i := *m.offset; i < len(lines) && i < *m.offset+h; i++ {
		t := ansi.Truncate(lines[i].text, w, "…")
		if l := lines[i]; l.issue >= 0 {
			base := lipgloss.NewStyle()
			if l.issue == m.cursor {
				base = sSel
			}
			t = m.issueRow(m.issues[l.idx], grouped, m.depth[l.idx], w, base)
		}
		out = append(out, t)
	}
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(strings.Join(out, "\n"))
}

// subIssuesBlock lists the issue's sub-issues with their states.
func (m model) subIssuesBlock(is linear.Issue, w int) string {
	full, ok := m.details[is.Identifier]
	if !ok {
		if has, _ := is.HasChildren(); has && m.client != nil && !m.detFailed[is.Identifier] {
			return sDim.Render("loading sub-issues…")
		}
		return ""
	}
	subs := full.SubIssues()
	if len(subs) == 0 {
		return ""
	}
	done := 0
	for _, c := range subs {
		if c.State != nil && (c.State.Type == "completed" || c.State.Type == "canceled") {
			done++
		}
	}
	lines := []string{sBold.Render("Sub-issues") + sDim.Render(fmt.Sprintf(" · %d/%d done", done, len(subs)))}
	for i, c := range subs {
		base := lipgloss.NewStyle()
		if m.subFocus && i == m.subCur {
			base = sSel
		}
		dot := base.Render("○")
		if c.State != nil {
			dot = stateDotOn(*c.State, base)
		}
		head := dot + base.Render(" ") + base.Foreground(cDim).Render(fmt.Sprintf("%-9s", c.Identifier)) + base.Render(" ")
		rest := max(0, w-lipgloss.Width(head))
		title := ansi.Truncate(c.Title, rest, "…")
		if base.GetBackground() != (lipgloss.NoColor{}) {
			title += strings.Repeat(" ", max(0, rest-lipgloss.Width(title)))
		}
		lines = append(lines, head+base.Render(title))
	}
	return strings.Join(lines, "\n")
}

// needsDetails: the list says the issue has comments, sub-issues or a parent
// (whose description we want) — or it doesn't know (an older cache).
func needsDetails(is linear.Issue) bool {
	c, ck := is.HasComments()
	k, kk := is.HasChildren()
	return c || k || is.Parent != nil || !ck || !kk
}

// commentsBlock renders the selected issue's comments, oldest first, each as
// a quote-like block with a thin bar, w cells wide.
func (m model) commentsBlock(is linear.Issue, w int) string {
	id := is.Identifier
	full, ok := m.details[id]
	cs := full.SortedComments()
	switch {
	case m.detFailed[id]:
		return sDim.Render("couldn't load comments and sub-issues — r to retry")
	case !ok && m.client != nil:
		// Only promise comments when the list knows there are some.
		if has, _ := is.HasComments(); has {
			return sDim.Render("loading comments…")
		}
		return ""
	case len(cs) == 0:
		return ""
	}
	bar := lipgloss.NewStyle().
		Border(lipgloss.Border{Left: "│"}, false, false, false, true).
		BorderForeground(cFaint).PaddingLeft(1).Width(w - 1)
	now := time.Now()
	blocks := []string{sBold.Render("Comments") + sDim.Render(fmt.Sprintf(" · %d", len(cs)))}
	for _, c := range cs {
		who := "?"
		if c.User != nil {
			who = c.User.Name
		}
		head := sBold.Render(who) + sDim.Render(" · "+relTime(c.CreatedAt, now))
		blocks = append(blocks, bar.Render(head+"\n"+renderMarkdown(c.Body)))
	}
	return strings.Join(blocks, "\n\n")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// scrollRule is the line above the start card; it says when the preview has
// more above or below.
func scrollRule(w, above, below int) string {
	hint := ""
	switch {
	case below > 0:
		hint = fmt.Sprintf(" ↓ %d more · J ", below)
	case above > 0:
		hint = " ↑ K "
	}
	if hint == "" || lipgloss.Width(hint)+4 > w {
		return sDim.Render(strings.Repeat("─", w))
	}
	return sDim.Render(strings.Repeat("─", w-lipgloss.Width(hint)-2) + hint + "──")
}

// startCard says what enter will do: open the issue's existing worktree, or
// create a new worktree on a branch named by branch_template. It sits at the bottom of the
// preview under a rule, apart from the issue content.
func (m model) startCard(is linear.Issue, w int) []string {
	rule := sDim.Render(strings.Repeat("─", w))
	if subs := m.currentSubs(); m.subFocus && m.subCur < len(subs) {
		// In the sub-issue list enter opens the chosen one; say so.
		c := subs[m.subCur]
		line := sDim.Render("enter → open ") + c.Identifier + " " + c.Title
		return []string{rule, ansi.Truncate(line, w, "…"), ""}
	}
	// Which repo: "in abc_billing" when sure, "in abc_billing?" when it'll
	// ask with that guess, nothing when it'll ask with no guess.
	plan := m.repoPlan(is)
	where := ", repo asked"
	if plan.Path != "" {
		where = " in " + filepath.Base(plan.Path)
		if !plan.Sure {
			where += "?"
		} else if strings.HasPrefix(plan.Reason, "pinned") {
			where += " · pinned, R to change"
		}
	}
	action, name := "enter → new worktree"+where, kickoff.BranchName(kickoff.Env{Cfg: m.d.Cfg, Viewer: m.viewer}, is, "")
	if kickoff.SharesParentBranch(m.d.Cfg, is) {
		pid := is.Parent.Identifier
		if br := m.wtBranch[pid]; br != "" {
			action, name = "enter → new tab in "+pid+"'s worktree"+where, br
		} else {
			action, name = "enter → new worktree for parent "+pid+where, sDim.Render("(named from "+pid+")")
		}
	} else if br := m.wtBranch[is.Identifier]; br != "" {
		action, name = "enter → open worktree"+where, br
	} else if m.d.Cfg.AskType {
		action += " · branch type asked"
	}
	if kickoff.Magus(m.d.Cfg) {
		action += " · magus manager"
	}
	// The trailing blank line keeps it off the footer bar.
	if line := sDim.Render(action+"  ⎇ ") + name; lipgloss.Width(line) <= w {
		return []string{rule, line, ""}
	}
	// Too narrow for one line: give the branch name its own.
	return []string{rule, ansi.Truncate(sDim.Render(action), w, "…"), ansi.Truncate(sDim.Render("⎇ ")+name, w, "…"), ""}
}

// previewMaxWidth caps the text column: long lines are hard to read.
const previewMaxWidth = 80

// renderPreview shows the issue with the start card pinned to the bottom.
func (m model) renderPreview(w, h int) string {
	is, ok := m.current()
	if !ok {
		return lipgloss.NewStyle().Width(w).Height(h).Render("")
	}
	// Same 2-cell gap on both sides: the separator side gets " " from the join
	// plus the 1-cell indent below, the window edge gets 2 cells here.
	tw := max(10, min(w-3, previewMaxWidth))
	wrap := lipgloss.NewStyle().Width(tw)

	meta := sDim.Render(is.Identifier) + "  " + stateDot(is.State) + " " + is.State.Name
	if is.Priority != 0 && is.PriorityLabel != "" {
		meta += "  " + strings.TrimSpace(priorityMark(is.Priority, lipgloss.NewStyle())) + " " + sDim.Render(is.PriorityLabel)
	}
	if m.mode != linear.ModeMine {
		who := "unassigned"
		if is.Assignee != nil {
			who = "@" + is.Assignee.Name
		}
		meta += "  " + sDim.Render(who)
	}
	if is.Project != nil && is.Project.Name != "" {
		meta += "  " + sDim.Render("▤ "+is.Project.Name)
	}
	full := is
	if d, ok := m.details[is.Identifier]; ok { // the list's copy may be older
		full = d
	}
	for _, p := range planMeta(full, time.Now()) {
		meta += "  " + p
	}
	for _, l := range is.LabelNames() {
		meta += "  " + sDim.Render("#"+l)
	}
	desc := renderMarkdown(is.Description)
	if desc == "" {
		desc = sDim.Render("(no description)")
	}
	title := sBold.Render(is.Title)
	if is.Parent != nil { // a sub-issue: say whose, right above its title
		title = sDim.Render("↑ "+is.Parent.Identifier+" "+is.Parent.Title) + "\n" + title
	}
	parts := []string{wrap.Render(meta), wrap.Render(title), wrap.Render(desc)}
	subLine := -1 // content line of the selected sub-issue, kept in view
	if c := m.subIssuesBlock(is, tw); c != "" {
		if m.subFocus {
			subLine = len(strings.Split(strings.Join(parts, "\n\n"), "\n")) + 2 + m.subCur
		}
		parts = append(parts, c)
	}
	if c := m.prBlock(is, tw); c != "" {
		parts = append(parts, c)
	}
	if c := m.commentsBlock(is, tw); c != "" {
		parts = append(parts, c)
	}
	content := strings.Split(strings.Join(parts, "\n\n"), "\n")
	card := m.startCard(is, tw)

	room := max(1, h-len(card)) // content scrolls above the card, which stays at the bottom
	m.pv.max = max(0, len(content)-room)
	if subLine >= 0 && m.pv.follow {
		m.pv.follow = false
		if subLine < m.pv.scroll {
			m.pv.scroll = subLine
		} else if subLine >= m.pv.scroll+room {
			m.pv.scroll = subLine - room + 1
		}
	}
	m.pv.scroll = min(m.pv.scroll, m.pv.max)
	start := m.pv.scroll
	card[0] = scrollRule(tw, start, len(content)-start-room)
	lines := content[start:]
	if len(lines) > room {
		lines = lines[:room]
	}
	for len(lines) < room {
		lines = append(lines, "")
	}
	lines = append(lines, card...)
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(indent(strings.Join(lines, "\n"), 1))
}
