package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/workspace"
)

// Several Linear workspaces, one at a time. Each has its own API key, lists,
// scope and caches; the popup opens on the one last used in the repo it was
// opened from. p switches (and adds one), and an issue opened by ID or link
// is looked for in all of them.

// wsMsg is an answer from Linear tagged with the workspace it was asked of
// and the visit (gen): one that arrives after a switch — even back to the
// same workspace — or after its key was refused is dropped.
type wsMsg struct {
	ws  string
	gen int
	msg tea.Msg
}

// tag marks a command's answer with the current workspace.
func (m model) tag(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	ws, gen := m.ws.ID, m.gen
	return func() tea.Msg {
		if msg := cmd(); msg != nil {
			return wsMsg{ws, gen, msg}
		}
		return nil
	}
}

// connect makes a workspace's client from its saved key (the demo's own
// clients when set). LINEAR_API_KEY stands in for a workspace with no saved
// key; the viewer check catches it if it belongs to another one.
func (d Deps) connect(id string) (linear.API, string) {
	if d.Connect != nil {
		return d.Connect(id), "demo"
	}
	if d.Secrets == nil {
		return nil, ""
	}
	if key, src := d.Secrets.Get(id); key != "" {
		return linear.New(key), src
	}
	if key, src := d.Secrets.Legacy(); src == "env" {
		return linear.New(key), src
	}
	return nil, ""
}

// wsDir holds the current workspace's caches and scope.
func (m model) wsDir() string {
	if m.ws.ID == "" {
		return m.d.StateDir
	}
	return filepath.Join(m.d.StateDir, "ws", m.ws.ID)
}

// wsRepo is the repo the workspace choice is remembered for.
func (m model) wsRepo() string {
	if m.d.SourceRepo != "" {
		return m.d.SourceRepo
	}
	return m.repo
}

// multiWS: more than one workspace is connected (the header names it then).
func (m model) multiWS() bool { return m.reg != nil && len(m.reg.List) > 1 }

// useWorkspace makes id the current workspace: its client, its cached lists
// and scope; nothing is fetched yet. false when there's no key for it.
func (m *model) useWorkspace(id string) bool {
	m.gen++ // answers asked before are stale now
	m.ws, _ = m.reg.Get(id)
	m.client, m.keySource = m.d.connect(id)
	m.viewer = linear.Viewer{}
	m.issues, m.visible, m.cursor = nil, nil, 0
	*m.offset = 0
	m.lists = map[linear.Mode][]linear.Issue{}
	m.inflight, m.fetched = map[string]bool{}, map[string]bool{}
	m.scope, m.scopes, m.scopeErr = linear.Scope{}, nil, ""
	m.details = map[string]linear.Issue{}
	m.detWanted, m.detFailed = map[string]bool{}, map[string]bool{}
	m.teamStates = map[string][]linear.State{}
	m.teamPeople = map[string][]linear.Person{}
	m.palRemote, m.palAsking = map[string][]linear.IssueBrief{}, map[string]bool{}
	m.newOpts, m.nf = nil, nil
	m.drill, m.subFocus, m.opening, m.openNav, m.selectID = nil, false, "", false, ""
	m.listErr, m.loading = "", false
	if m.client == nil {
		return false
	}
	m.mode = linear.ParseMode(m.d.Cfg.DefaultMode)
	m.loadScopes()
	for _, md := range linear.Modes { // cached lists: instant switching and tab counts
		if cached := m.loadCache(md); cached != nil {
			m.lists[md] = cached
		}
	}
	m.setIssues(m.lists[m.mode])
	return true
}

// switchWorkspace opens another workspace; remember: it becomes this repo's
// (not when a link just passes through).
func (m *model) switchWorkspace(id string, remember bool) tea.Cmd {
	if remember {
		m.reg.Use(m.wsRepo(), id)
		_ = m.reg.Save()
	}
	if !m.useWorkspace(id) {
		*m = m.toKey("No key saved for " + m.ws.Name + " — paste it to reconnect")
		return textinput.Blink
	}
	m.screen = scList
	m.loading = true
	return tea.Batch(m.fetchViewer(), m.fetchIssues(), m.fetchWorktrees())
}

// keyChecked: a key that works adds (or reconnects) its workspace and opens it.
func (m model) keyChecked(msg keyCheckedMsg) (tea.Model, tea.Cmd) {
	m.keyBusy = false
	if msg.err != nil {
		if msg.src != "" { // the old single key or LINEAR_API_KEY: ask for one
			if !errors.Is(msg.err, linear.ErrUnauthorized) { // kept: it may well work next time
				m = m.toKey("Couldn't check the saved key (" + errText(msg.err) + ") — reopen to try again, or paste a key")
				return m, textinput.Blink
			}
			if msg.src != "env" {
				_ = m.d.Secrets.DeleteLegacy() // Linear refuses it: no use keeping it
			}
			m = m.toKey("The saved key didn't work (" + errText(msg.err) + ") — paste one")
			return m, textinput.Blink
		}
		m.keyErr = errText(msg.err)
		return m, nil
	}
	org := msg.viewer.Organization
	if org.ID == "" {
		m.keyErr = "Linear didn't say which workspace this key is for"
		return m, nil
	}
	where := msg.src
	if msg.src != "env" { // the environment's key stays there
		var err error
		if where, err = m.d.Secrets.Set(org.ID, msg.key); err != nil {
			m.keyErr = "Key works, but saving it failed: " + err.Error()
			return m, nil
		}
		if msg.src != "" { // the old single key, now saved for its workspace
			_ = m.d.Secrets.DeleteLegacy()
		}
	}
	m.reg.Add(workspace.WS{ID: org.ID, Name: org.Name, URLKey: org.URLKey})
	if msg.src != "" { // the lists cached for the old key are this workspace's
		moveLegacyState(m.d.StateDir, org.ID)
	}
	m.key.SetValue("")
	m.key.Blur()
	m.keyAdding = false
	back := m.keyFromSettings
	cmd := m.switchWorkspace(org.ID, true)
	m.viewer, m.keySource = msg.viewer, where
	if back && m.screen == scList { // replaced from settings: back there
		m.openSettings()
	}
	if !m.started { // the first run: what newModel would have set up
		cmd = tea.Batch(cmd, m.startup())
	}
	return m, cmd
}

// viewerChecked keeps the registry's name current and catches a key that
// answers for another workspace (LINEAR_API_KEY, say).
func (m *model) viewerChecked(v linear.Viewer) bool {
	org := v.Organization
	if m.ws.ID == "" || org.ID == "" {
		return true
	}
	if org.ID != m.ws.ID {
		return false
	}
	if org.Name != m.ws.Name || org.URLKey != m.ws.URLKey {
		m.ws = workspace.WS{ID: org.ID, Name: org.Name, URLKey: org.URLKey}
		m.reg.Add(m.ws)
		_ = m.reg.Save()
	}
	return true
}

// removeWorkspace forgets a workspace and its key; when it's the open one,
// another opens, or a key is asked for when none is left.
func (m *model) removeWorkspace(id string) tea.Cmd {
	if err := m.d.Secrets.Delete(id); err != nil {
		m.setErr = err.Error()
		return nil
	}
	m.reg.Remove(id)
	_ = m.reg.Save()
	if id != m.ws.ID {
		return nil
	}
	if next := m.reg.For(m.wsRepo()); next != "" {
		return m.switchWorkspace(next, true)
	}
	m.client, m.ws = nil, workspace.WS{}
	*m = m.toKey("Workspace removed — paste a key to connect one, or esc to quit")
	return textinput.Blink
}

// fetchOpen loads an issue by ID: from the link's workspace if it names one,
// then the current one, then the others. The answer says where it was found.
func (m model) fetchOpen(id string) tea.Cmd {
	type cand struct {
		ws string
		c  linear.API
	}
	cands := []cand{}
	switch w, ok := m.reg.ByURLKey(m.openWS); {
	case m.openWS != "" && !ok: // a link to a workspace not connected here
		ws := m.openWS
		return func() tea.Msg {
			return openMsg{id: id, err: errors.New("the link is to workspace " + ws + ", which isn't connected — p → Add a workspace…")}
		}
	case ok: // a link: its own workspace only (the same ID elsewhere is another issue)
		var c linear.API
		if w.ID == m.ws.ID {
			c = m.client
		}
		cands = append(cands, cand{w.ID, c})
	default: // an ID: here, then the other workspaces
		cands = append(cands, cand{m.ws.ID, m.client})
		for _, w := range m.reg.List {
			if w.ID != m.ws.ID {
				cands = append(cands, cand{w.ID, nil})
			}
		}
	}
	d, cur, known := m.d, m.ws.ID, m.viewer.ID != ""
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var first error
		for _, cd := range cands {
			c := cd.c
			if c == nil {
				if c, _ = d.connect(cd.ws); c == nil {
					continue
				}
			}
			is, err := c.Issue(ctx, id)
			if err != nil {
				if first == nil {
					first = err
				}
				continue // maybe in another workspace
			}
			msg := openMsg{id: id, issue: is, ws: cd.ws}
			// Which tab it belongs on depends on who "me" is there.
			if cd.ws != cur || !known {
				if v, verr := c.Viewer(ctx); verr == nil {
					msg.viewer = v
				} else {
					msg.err = verr
				}
			}
			return msg
		}
		return openMsg{id: id, err: first, ws: cur}
	}
}

// notFound: Linear has no such issue (in this workspace).
func notFound(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "not found") || strings.Contains(s, "could not find")
}

// Picker entries for workspaces, above the teams and projects.
const (
	kindWorkspace = "workspace"
	kindAddWS     = "add-workspace"
)

func (m model) workspaceScopes(q string) []linear.Scope {
	if m.reg == nil {
		return nil
	}
	var out []linear.Scope
	for _, w := range m.reg.List {
		if q == "" || strings.Contains(strings.ToLower(w.Name), q) {
			out = append(out, linear.Scope{Kind: kindWorkspace, ID: w.ID, Name: w.Name})
		}
	}
	if q == "" || strings.Contains("add a workspace", q) {
		out = append(out, linear.Scope{Kind: kindAddWS, Name: "Add a workspace…"})
	}
	return out
}

// toAddKey shows the key screen for another workspace; esc comes back.
func (m model) toAddKey() (tea.Model, tea.Cmd) {
	m = m.toKey("")
	m.keyAdding = true
	return m, textinput.Blink
}

// moveLegacyState moves the lists and scope cached before workspaces into the
// first workspace's folder.
func moveLegacyState(stateDir, id string) {
	dst := filepath.Join(stateDir, "ws", id)
	files, _ := filepath.Glob(filepath.Join(stateDir, "issues-*.json"))
	files = append(files, filepath.Join(stateDir, "scope.json"))
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			continue
		}
		if os.MkdirAll(dst, 0o700) == nil {
			_ = os.Rename(f, filepath.Join(dst, filepath.Base(f)))
		}
	}
}
