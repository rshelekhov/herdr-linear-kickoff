package ui

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/config"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
)

func settingsModel(t *testing.T) model {
	m := listModel(t)
	m.d.ConfigDir = t.TempDir()
	if _, err := config.Load(m.d.ConfigDir); err != nil { // the commented sample
		t.Fatal(err)
	}
	m.d.Secrets = &fakeSecrets{key: "lin_api_abcdefghijklmnop1234"}
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return press(mm.(model), ",")
}

func rowIndex(t *testing.T, m model, id string) int {
	for i, r := range m.settingRows() {
		if r.id == id {
			return i
		}
	}
	t.Fatalf("no row %q", id)
	return -1
}

func TestSettingsScreen(t *testing.T) {
	m := settingsModel(t)
	if m.screen != scSettings {
		t.Fatal(", doesn't open settings")
	}
	v := m.View()
	for _, s := range []string{"Settings", "Workspaces", "Acme", "Add a workspace…", "lin_api_…1234", "Branch name", "{linear}  → abc-964", "Agent", "claude", "Install packages", "by the project's lockfile"} {
		if !strings.Contains(v, s) {
			t.Errorf("settings lack %q", s)
		}
	}
	if strings.Contains(v, "abcdefghijklmnop") {
		t.Error("the API key isn't masked")
	}
}

func TestSettingsChangesAreSaved(t *testing.T) {
	installed := map[string]bool{"claude": true, "codex": true, "qwen": true}
	lookPath = func(bin string) (string, error) {
		if installed[bin] {
			return "/bin/" + bin, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { lookPath = exec.LookPath })

	m := settingsModel(t)
	m.setCur = rowIndex(t, m, "agent")
	m = press(m, "enter")
	v := m.View()
	if m.pick == nil || !strings.Contains(v, "qwen") || strings.Contains(v, "gemini") || !strings.Contains(v, "custom…") {
		t.Fatalf("agent picker should list installed agents only:\n%s", v)
	}
	for m.pick.opts[m.pick.cur].label != "qwen" {
		m = press(m, "down")
	}
	m = press(m, "enter")
	if m.d.Cfg.AgentCmd != "qwen -i" {
		t.Errorf("agent %q, want qwen -i (its interactive flag)", m.d.Cfg.AgentCmd)
	}

	// Branch name: presets with this issue's branch for each.
	m.setCur = rowIndex(t, m, "branch")
	m = press(m, "enter")
	if v := m.View(); !strings.Contains(v, "{type}/{id}-{slug}") || !strings.Contains(v, "feat/abc-964-optimize") {
		t.Fatalf("branch presets lack examples:\n%s", v)
	}
	m = press(m, "down", "enter") // {type}/{id}-{slug}
	if m.d.Cfg.BranchTemplate != "{type}/{id}-{slug}" {
		t.Fatalf("template %q", m.d.Cfg.BranchTemplate)
	}
	// With {type} in the template, "Ask branch type" appears.
	m.setCur = rowIndex(t, m, "asktype")
	m = press(m, "enter")

	// Custom… edits inline.
	m.setCur = rowIndex(t, m, "branch")
	m = press(m, "enter")
	for m.pick.opts[m.pick.cur].label != "custom…" {
		m = press(m, "down")
	}
	m = press(m, "enter")
	if m.setEditing != "branch" {
		t.Fatal("custom… doesn't edit")
	}
	m.setIn.SetValue("{me}-{id}")
	m = press(m, "enter")

	c, err := config.Load(m.d.ConfigDir)
	if err != nil || c.AgentCmd != "qwen -i" || !c.AskType || c.BranchTemplate != "{me}-{id}" {
		t.Errorf("not saved: %+v %v", c, err)
	}
}

func TestSettingsHidesAskTypeWithoutType(t *testing.T) {
	m := settingsModel(t) // template {linear}: no {type}
	for _, r := range m.settingRows() {
		switch r.id {
		case "asktype", "tab", "background", "labels":
			t.Errorf("row %q shouldn't be on the screen", r.id)
		}
	}
}

func TestLastTabRemembered(t *testing.T) {
	m := listModel(t)
	m.client = linear.New("k")
	m = press(m, "3") // Backlog
	m2 := newModel(Deps{Cfg: m.d.Cfg, StateDir: m.d.StateDir, Secrets: &fakeSecrets{key: "k"}})
	if m2.mode != linear.ModeBacklog {
		t.Errorf("reopened on %s, want backlog", m2.mode)
	}
}

func TestSettingsRemoveKey(t *testing.T) {
	m := settingsModel(t)
	m.setCur = rowIndex(t, m, "ws:w1")
	m = press(m, "x")
	if k, _ := m.d.Secrets.Get("w1"); k != "" || m.screen != scKey || len(m.reg.List) != 0 {
		t.Errorf("workspace not removed: key %q, screen %v, %d left", k, m.screen, len(m.reg.List))
	}
}

func TestSettingsLaunch(t *testing.T) {
	m := settingsModel(t)
	m.setCur = rowIndex(t, m, "launch")
	if !strings.Contains(m.View(), "the agent gets the whole issue") {
		t.Error("prompt launch not explained")
	}
	m = press(m, "enter")
	if m.d.Cfg.Launch != "magus" {
		t.Fatalf("launch %q", m.d.Cfg.Launch)
	}
	if v := m.View(); !strings.Contains(v, "magus") {
		t.Errorf("settings:\n%s", v)
	}
}

func TestSettingsWorkspaceName(t *testing.T) {
	m := settingsModel(t)
	m.setCur = rowIndex(t, m, "wsname")
	m = press(m, "enter")
	if m.pick == nil || !strings.Contains(m.View(), "{ID} · {title}") {
		t.Fatalf("no presets:\n%s", m.View())
	}
	m = press(m, "down", "down", "enter") // {ID} · {title}
	if m.d.Cfg.WorkspaceName != "{ID} · {title}" {
		t.Fatalf("workspace_name %q", m.d.Cfg.WorkspaceName)
	}
	if c, _ := config.Load(m.d.ConfigDir); c.WorkspaceName != "{ID} · {title}" {
		t.Errorf("not saved: %q", c.WorkspaceName)
	}
}

func TestSettingsAbout(t *testing.T) {
	var opened, copied string
	openURL = func(u string) { opened = u }
	copyText = func(s string) { copied = s }
	t.Cleanup(func() { openURL, copyText = openURLDefault, copyTextDefault })

	m := settingsModel(t)
	m.d.Version = "0.1.0 · 883abc6"
	m.setCur = rowIndex(t, m, "repo")
	v := m.View()
	for _, s := range []string{"About", "Version", "0.1.0 · 883abc6", "github.com/rshelekhov/herdr-linear-kickoff", "y copy the link"} {
		if !strings.Contains(v, s) {
			t.Errorf("settings lack %q", s)
		}
	}
	m = press(m, "enter")
	if opened != repoURL {
		t.Errorf("enter opened %q, want the repo", opened)
	}
	m = press(m, "y")
	if copied != repoURL || !strings.Contains(m.View(), "copied "+repoURL) {
		t.Errorf("y copied %q and should say so", copied)
	}
	if m = press(m, "up"); strings.Contains(m.View(), "copied") {
		t.Error("the note outlives the next key")
	}
}
