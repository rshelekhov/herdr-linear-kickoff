package ui

import (
	"cmp"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/config"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/kickoff"
)

// ",": the settings screen. Each change is applied at once and written to
// config.toml (comments kept); "e" opens the file itself in $EDITOR.

type editorDoneMsg struct{ err error }

// setRow is one line of the settings screen.
type setRow struct {
	id, section, label string
	value              string
	hint               string // keys that do something here
}

// repoURL is where the plugin lives: its README, releases and issues.
const repoURL = "https://github.com/rshelekhov/herdr-linear-kickoff"

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// wsLabel: the workspace the key is for, then the key.
func wsLabel(ws, key string) string {
	if ws == "" {
		return key
	}
	return ws + " · " + key
}

// settingRows lists what the screen shows, in order.
func (m model) settingRows() []setRow {
	c := m.d.Cfg
	branch := c.BranchTemplate
	if is, ok := m.current(); ok {
		branch += "  → " + kickoff.BranchName(kickoff.Env{Cfg: c, Viewer: m.viewer}, is, "")
	}
	subIssue := "own branch"
	if strings.EqualFold(c.SubIssueBranch, "parent") {
		subIssue = "parent's branch"
	}
	var rows []setRow
	if m.reg != nil {
		for _, w := range m.reg.List { // every connected workspace and its key
			name := w.Name
			if w.ID == m.ws.ID && len(m.reg.List) > 1 {
				name += " (open)"
			}
			rows = append(rows, setRow{"ws:" + w.ID, "Workspaces", name, m.keyMasks[w.ID], "enter replace the key · x remove the workspace"})
		}
	}
	rows = append(rows, setRow{"ws-add", "Workspaces", "Add a workspace…", "", "enter paste a key from another workspace"})
	rows = append(rows, []setRow{
		{"branch", "Branches", "Branch name", branch, "enter choose"},
		{"wsname", "Branches", "Workspace name", wsName(m, c), "enter choose"},
	}...)
	if strings.Contains(c.BranchTemplate, "{type}") { // only then is there a type to ask for
		rows = append(rows, setRow{"asktype", "Branches", "Ask branch type", onOff(c.AskType), "enter toggle · feat/fix/… picked on start"})
	}
	rows = append(rows,
		setRow{"subissue", "Branches", "Sub-issues", subIssue, "enter switch"},
		setRow{"agent", "Start", "Agent", c.AgentCmd, "enter choose"},
		setRow{"launch", "Start", "Launch", launchLabel(c), "enter switch"},
		setRow{"install", "Start", "Install packages", installLabel(c.Install), "enter next · c custom command"},
		setRow{"inprogress", "Start", "Set In Progress", onOff(c.StartSetsInProgress), "enter toggle"},
		setRow{"assign", "Start", "Assign to me", onOff(c.StartAssignsMe), "enter toggle"},
		setRow{"version", "About", "Version", cmp.Or(m.d.Version, "dev"), ""},
		setRow{"repo", "About", "Repository", strings.TrimPrefix(repoURL, "https://"), "enter open in the browser · y copy the link"},
	)
	return rows
}

func (m *model) openSettings() tea.Cmd {
	m.screen = scSettings
	m.setCur, m.setEditing, m.setErr = 0, "", ""
	// Read the key once here, not on every render: it may come from the
	// keychain, which can be slow or ask for access.
	m.keyMasks = map[string]string{}
	if m.reg != nil {
		for _, w := range m.reg.List {
			mask := "no key"
			if m.d.Connect != nil {
				mask = "demo"
			} else if key, src := m.d.Secrets.Get(w.ID); key != "" {
				mask = key[:min(8, len(key))] + "…" + key[max(0, len(key)-4):] + " · " + src
			} else if w.ID == m.ws.ID && m.keySource != "" {
				mask = m.keySource
			}
			m.keyMasks[w.ID] = mask
		}
	}
	return nil
}

// save writes one key and applies it to the running config.
func (m *model) save(key string, value any, apply func(*config.Config)) {
	m.setErr = ""
	if m.d.ConfigDir != "" {
		if err := config.Set(m.d.ConfigDir, key, value); err != nil {
			m.setErr = err.Error()
			return
		}
	}
	apply(&m.d.Cfg)
}

// next is the value after cur in a cycle; a value outside it starts it over.
func next(cycle []string, cur string) string {
	i := slices.Index(cycle, cur)
	return cycle[(i+1)%len(cycle)]
}

func (m *model) startEdit(id, value string) tea.Cmd {
	m.setEditing = id
	m.setIn.SetValue(value)
	m.setIn.CursorEnd()
	return m.setIn.Focus()
}

func (m model) onSettingsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.setEditing != "" {
		return m.onSettingEdit(k)
	}
	if m.pick != nil {
		return m.onPickKey(k)
	}
	rows := m.settingRows()
	m.setNote = ""
	switch k.String() {
	case "esc", "q", ",":
		m.screen = scList
		return m, nil
	case "up", "k":
		m.setCur = max(0, m.setCur-1)
		return m, nil
	case "down", "j":
		m.setCur = min(len(rows)-1, m.setCur+1)
		return m, nil
	case "e":
		return m, m.openEditor()
	}
	if m.setCur >= len(rows) {
		return m, nil
	}
	row, c := rows[m.setCur], m.d.Cfg
	switch k.String() {
	case "x":
		switch {
		case strings.HasPrefix(row.id, "ws:"):
			cmd := m.removeWorkspace(strings.TrimPrefix(row.id, "ws:"))
			m.setCur = 0
			if m.screen == scSettings {
				m.openSettings() // the rows changed
			}
			return m, cmd
		}
		return m, nil
	case "y":
		if row.id == "repo" {
			copyText(repoURL)
			m.setNote = "copied " + repoURL
		}
		return m, nil
	case "c":
		switch row.id {
		case "install":
			return m, m.startEdit("install", c.Install)
		}
		return m, nil
	case "enter", " ":
	default:
		return m, nil
	}
	if strings.HasPrefix(row.id, "ws:") { // replace that workspace's key
		m = m.toKey("Paste the new key for " + row.label)
		m.keyFromSettings = true
		return m, textinput.Blink
	}
	switch row.id {
	case "repo":
		openURL(repoURL)
	case "ws-add":
		mm, cmd := m.toAddKey()
		m = mm.(model)
		m.keyFromSettings = true
		return m, cmd
	case "branch", "wsname":
		m.openPick(row.id)
	case "asktype":
		m.save("ask_type", !c.AskType, func(c *config.Config) { c.AskType = !c.AskType })
	case "inprogress":
		m.save("start_sets_in_progress", !c.StartSetsInProgress, func(c *config.Config) { c.StartSetsInProgress = !c.StartSetsInProgress })
	case "assign":
		m.save("start_assigns_me", !c.StartAssignsMe, func(c *config.Config) { c.StartAssignsMe = !c.StartAssignsMe })
	case "launch":
		v := next([]string{"prompt", "magus"}, strings.ToLower(c.Launch))
		m.save("launch", v, func(c *config.Config) { c.Launch = v })
		if err := kickoff.CheckLaunch(m.d.Cfg); err != nil {
			m.setErr = err.Error() // saved, but Start would refuse: say why now
		}
	case "subissue":
		v := next([]string{"own", "parent"}, strings.ToLower(c.SubIssueBranch))
		m.save("subissue_branch", v, func(c *config.Config) { c.SubIssueBranch = v })
	case "agent":
		m.openPick("agent")
	case "install":
		v := next([]string{"auto", "none"}, c.Install)
		m.save("install", v, func(c *config.Config) { c.Install = v })
	}
	return m, nil
}

// onSettingEdit handles the inline text field of a setting.
func (m model) onSettingEdit(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.setEditing = ""
		m.setIn.Blur()
		return m, nil
	case "enter":
		v := strings.TrimSpace(m.setIn.Value())
		id := m.setEditing
		m.setEditing = ""
		m.setIn.Blur()
		if v == "" {
			return m, nil
		}
		switch id {
		case "branch", "agent", "wsname":
			m.save(pickKeys[id], v, func(c *config.Config) { setPick(c, id, v) })
		case "install":
			m.save("install", v, func(c *config.Config) { c.Install = v })
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.setIn, cmd = m.setIn.Update(k)
	return m, cmd
}

// editor is $VISUAL, else $EDITOR, else vi.
func editor() string {
	for _, v := range []string{"VISUAL", "EDITOR"} {
		if e := strings.TrimSpace(os.Getenv(v)); e != "" {
			return e
		}
	}
	return "vi"
}

// openEditor suspends the popup for $EDITOR on config.toml, then reloads it.
func (m model) openEditor() tea.Cmd {
	if m.d.ConfigDir == "" {
		return nil
	}
	args := append(strings.Fields(editor()), config.Path(m.d.ConfigDir))
	return tea.ExecProcess(exec.Command(args[0], args[1:]...), func(err error) tea.Msg { return editorDoneMsg{err} })
}

func (m *model) editorDone(msg editorDoneMsg) {
	if msg.err != nil {
		m.setErr = "editor: " + msg.err.Error()
		return
	}
	cfg, err := config.Load(m.d.ConfigDir)
	m.d.Cfg, m.d.CfgErr = cfg, err
	if err != nil {
		m.setErr = err.Error()
	}
}

func (m model) viewSettings() string {
	rows := m.settingRows()
	labelW := 0
	for _, r := range rows {
		labelW = max(labelW, len([]rune(r.label)))
	}
	labelW = min(labelW+2, 28)
	var lines []string
	cur, section := 0, ""
	for i, r := range rows {
		if r.section != section {
			if section != "" {
				lines = append(lines, "")
			}
			lines = append(lines, "  "+sBold.Render(r.section))
			section = r.section
		}
		value := r.value
		if m.setEditing == r.id {
			value = m.setIn.View()
		}
		label := fmt.Sprintf("%-*s", labelW, ansi.Truncate(r.label, labelW-2, "…"))
		line := "    " + label + value
		if i == m.setCur {
			cur = len(lines)
			line = "  " + sSel.Render("› "+label) + value
			if m.setEditing == "" {
				line += "   " + sDim.Render(r.hint)
			}
		}
		lines = append(lines, ansi.Truncate(line, m.w-2, "…"))
	}
	room := max(3, m.h-6)
	start := max(0, min(cur-room/2, len(lines)-room))
	end := min(len(lines), start+room)

	head := sAccent.Render("Settings") + "  " + sDim.Render(m.configPathLabel())
	foot := "enter change · e open config.toml in " + filepath.Base(strings.Fields(editor())[0]) + " · esc back"
	if m.setEditing != "" {
		foot = "enter save · esc cancel"
	}
	body := strings.Join(lines[start:end], "\n")
	out := []string{"  " + head, "", body}
	if m.setErr != "" {
		out = append(out, "", "  "+sErr.Render(m.setErr))
	} else if m.setNote != "" {
		out = append(out, "", "  "+sDim.Render(m.setNote))
	}
	return lipgloss.NewStyle().Height(m.h-1).Render(strings.Join(out, "\n")) + "\n" +
		sBar.Width(m.w).MaxWidth(m.w).Render(ansi.Truncate(foot, m.w-4, "…"))
}

func (m model) configPathLabel() string {
	if m.d.ConfigDir == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil {
		return strings.Replace(config.Path(m.d.ConfigDir), home, "~", 1)
	}
	return config.Path(m.d.ConfigDir)
}

func newSettingInput() textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.Width = 50
	return in
}

// ---- pickers: a short list of ready values with "custom…"

type pickOpt struct {
	label, detail, value string
}

type settingPick struct {
	id, title string
	opts      []pickOpt
	cur       int
}

// Agents with a verified way to start an interactive session with a first
// prompt: the prompt goes last (see kickoff.StartScript). Others: custom.
var knownAgents = []struct{ bin, cmd, note string }{
	{"claude", "claude", "Claude Code"},
	{"codex", "codex", "OpenAI Codex CLI"},
	{"gemini", "gemini", "Gemini CLI"},
	{"cursor-agent", "cursor-agent", "Cursor Agent"},
	{"opencode", "opencode --prompt", "OpenCode"},
	{"qwen", "qwen -i", "Qwen Code"},
}

// lookPath is a var so tests can fake which agents are installed.
var lookPath = exec.LookPath

// wsName: the template, and what it makes of the selected issue.
func wsName(m model, c config.Config) string {
	if is, ok := m.current(); ok {
		return c.WorkspaceName + "  → " + kickoff.WorkspaceName(c, is)
	}
	return c.WorkspaceName
}

// wsNamePresets are workspace name templates.
var wsNamePresets = []string{"{ID}", "{ID} {title}", "{ID} · {title}", "{title}"}

// The settings picked from a list: their config key, current value, setter.
var pickKeys = map[string]string{"branch": "branch_template", "agent": "agent_cmd", "wsname": "workspace_name"}

func pickValue(c config.Config, id string) string {
	return map[string]string{"branch": c.BranchTemplate, "agent": c.AgentCmd, "wsname": c.WorkspaceName}[id]
}

func setPick(c *config.Config, id, v string) {
	switch id {
	case "branch":
		c.BranchTemplate = v
	case "agent":
		c.AgentCmd = v
	case "wsname":
		c.WorkspaceName = v
	}
}

// branchPresets are common branch name templates.
var branchPresets = []struct{ tmpl, note string }{
	{"{linear}", "Linear's own"},
	{"{type}/{id}-{slug}", ""},
	{"{id}-{slug}", ""},
	{"{me}/{id}-{slug}", ""},
	{"{type}/{slug}", ""},
}

func (m *model) openPick(id string) {
	p := &settingPick{id: id}
	cur := ""
	switch id {
	case "branch":
		p.title, cur = "Branch name", m.d.Cfg.BranchTemplate
		is, ok := m.current()
		if ok {
			p.title += " · examples for " + is.Identifier
		}
		for _, b := range branchPresets {
			example := b.note
			if ok {
				c := m.d.Cfg
				c.BranchTemplate = b.tmpl
				example = kickoff.BranchName(kickoff.Env{Cfg: c, Viewer: m.viewer}, is, "")
			}
			p.opts = append(p.opts, pickOpt{b.tmpl, example, b.tmpl})
		}
	case "wsname":
		p.title, cur = "Workspace name", m.d.Cfg.WorkspaceName
		is, ok := m.current()
		for _, t := range wsNamePresets {
			example := ""
			if ok {
				c := m.d.Cfg
				c.WorkspaceName = t
				example = kickoff.WorkspaceName(c, is)
			}
			p.opts = append(p.opts, pickOpt{t, example, t})
		}
	case "agent":
		p.title, cur = "Agent", m.d.Cfg.AgentCmd
		for _, a := range knownAgents {
			if _, err := lookPath(a.bin); err == nil {
				p.opts = append(p.opts, pickOpt{a.bin, a.note, a.cmd})
			}
		}
	}
	p.opts = append(p.opts, pickOpt{"custom…", "type your own", ""})
	for i, o := range p.opts {
		if o.value == cur {
			p.cur = i
		}
	}
	m.pick = p
}

func (m model) onPickKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.pick
	switch k.String() {
	case "esc", "q":
		m.pick = nil
	case "up", "k":
		p.cur = max(0, p.cur-1)
	case "down", "j":
		p.cur = min(len(p.opts)-1, p.cur+1)
	case "enter":
		o := p.opts[p.cur]
		m.pick = nil
		if o.value == "" { // custom…
			return m, m.startEdit(p.id, pickValue(m.d.Cfg, p.id))
		}
		v, id := o.value, p.id
		m.save(pickKeys[id], v, func(c *config.Config) { setPick(c, id, v) })
	}
	return m, nil
}

func (m model) pickDialog() string {
	p := m.pick
	w := min(76, max(40, m.w-8))
	cur := pickValue(m.d.Cfg, p.id)
	labelW := 0
	for _, o := range p.opts {
		labelW = max(labelW, len([]rune(o.label)))
	}
	var b strings.Builder
	for i, o := range p.opts {
		check := " "
		if o.value != "" && o.value == cur {
			check = "✓"
		}
		label := fmt.Sprintf("%-*s", labelW, o.label)
		line := "  " + label + "  " + sDim.Render(o.detail) + " " + check
		if i == p.cur {
			line = sSel.Render("› "+label) + "  " + sDim.Render(o.detail) + " " + check
		}
		b.WriteString(ansi.Truncate(line, w-6, "…") + "\n")
	}
	if p.id == "agent" {
		b.WriteString("\n" + sDim.Render("installed agents only; anything else: custom…") + "\n")
	}
	b.WriteString("\n" + sDim.Render("enter choose · esc cancel"))
	return dialog(p.title, b.String(), w)
}

// installLabel says what an install setting does.
func installLabel(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "auto":
		return "auto  " + sDim.Render("bun / pnpm / yarn / npm, by the project's lockfile")
	case "none", "":
		return "none  " + sDim.Render("don't install anything")
	}
	return v
}

// launchLabel says what Start runs.
func launchLabel(c config.Config) string {
	if kickoff.Magus(c) {
		note := "magus orchestrator's manager reads the issue"
		if !kickoff.OrchestratorInstalled() {
			note = "orchestrator plugin not installed in Claude Code"
		}
		return "magus  " + sDim.Render(note)
	}
	return "prompt  " + sDim.Render("the agent gets the whole issue")
}
