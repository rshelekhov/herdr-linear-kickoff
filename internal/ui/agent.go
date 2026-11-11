package ui

import (
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/kickoff"
)

// "A": start this issue with another agent than the one in settings, this
// time only. Then the usual way: repo, branch type, kickoff.

// agentOpt is one way to start: an agent command and a launch mode.
type agentOpt struct {
	name, note string
	cmd        string
	launch     string // "prompt" or "magus"
}

// orchestratorInstalled is a var so tests don't read the real ~/.claude.
var orchestratorInstalled = kickoff.OrchestratorInstalled

// agentOptions: the installed agents, magus manager when it can run, and the
// configured command if it's none of those. The current default is marked.
func (m model) agentOptions() []agentOpt {
	cfg := m.d.Cfg
	launch := "prompt"
	if kickoff.Magus(cfg) {
		launch = "magus"
	}
	var out []agentOpt
	claude := ""
	for _, a := range knownAgents {
		if _, err := lookPath(a.bin); err == nil {
			out = append(out, agentOpt{a.bin, a.note, a.cmd, "prompt"})
			if a.bin == "claude" {
				claude = a.cmd
			}
		}
	}
	if bin := filepath.Base(strings.Fields(cfg.AgentCmd + " x")[0]); bin == "claude" || bin == "claudish" {
		claude = cfg.AgentCmd // claudish too, as set up
	}
	if claude != "" && orchestratorInstalled() {
		out = append(out, agentOpt{"magus manager", "via " + claude, claude, "magus"})
	}
	found := false
	for i := range out {
		if out[i].cmd == cfg.AgentCmd && out[i].launch == launch {
			out[i].note = "default · " + out[i].note
			found = true
		}
	}
	if !found {
		out = append([]agentOpt{{cfg.AgentCmd, "default", cfg.AgentCmd, launch}}, out...)
	}
	return out
}

// openAgents shows "start with…" for the issue in the preview.
func (m *model) openAgents() {
	is, ok := m.current()
	if !ok || m.client == nil {
		return
	}
	m.agentIssue, m.agentOpts, m.agentCur = is, m.agentOptions(), 0
	for i, o := range m.agentOpts {
		if strings.HasPrefix(o.note, "default") {
			m.agentCur = i
		}
	}
	m.screen = scAgent
}

func (m model) onAgentKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch s := k.String(); s {
	case "esc", "q":
		m.screen = scList
	case "up", "k", "ctrl+p":
		m.agentCur = max(0, m.agentCur-1)
	case "down", "j", "ctrl+n":
		m.agentCur = min(len(m.agentOpts)-1, m.agentCur+1)
	case "enter":
		return m, m.startWith(m.agentCur)
	default:
		if len(s) == 1 && s[0] >= '1' && s[0] <= '9' && int(s[0]-'1') < len(m.agentOpts) {
			return m, m.startWith(int(s[0] - '1'))
		}
	}
	return m, nil
}

func (m *model) startWith(i int) tea.Cmd {
	o := m.agentOpts[i]
	m.screen = scList
	m.agentCmd, m.agentLaunch = o.cmd, o.launch // set before: begin may start at once
	return m.begin(m.agentIssue, false)
}

func (m model) agentDialog() string {
	const w = 56
	var b strings.Builder
	nameW := 0
	for _, o := range m.agentOpts {
		nameW = max(nameW, len(o.name))
	}
	for i, o := range m.agentOpts {
		key := sDim.Render(string(rune('1'+i)) + " ")
		if i > 8 {
			key = "  "
		}
		gap := strings.Repeat(" ", nameW-len(o.name)+2)
		line := "  " + key + o.name + gap + sDim.Render(o.note)
		if i == m.agentCur {
			line = sSel.Render("› ") + key + sSel.Render(o.name) + gap + sDim.Render(o.note)
		}
		b.WriteString(ansi.Truncate(line, w-6, "…") + "\n")
	}
	b.WriteString("\n" + sDim.Render("1–9 · enter start · esc cancel"))
	return dialog(m.agentIssue.Identifier+" · start with", b.String(), w)
}
