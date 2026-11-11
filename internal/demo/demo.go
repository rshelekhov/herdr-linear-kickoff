// Package demo runs the popup on a made-up workspace: no Linear key, no
// herdr, nothing of the user's. It is how the README screenshots are made,
// and a way to try the plugin before connecting it.
package demo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/config"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/kickoff"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/repos"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/ui"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/workspace"
)

// The demo's repos, under ~/code of its own home folder.
var repoNames = []string{"acme/web", "acme/api", "acme/mobile", "acme/design-system", "northwind/infra"}

// The agents the demo machine has.
var demoAgents = []string{"claude", "codex", "gemini", "opencode"}

// Existing worktrees: repo → branches. The list marks these issues, and
// enter opens them instead of making new ones.
var worktrees = map[string][]string{
	"web": {"feat/web-214-save-cards-for-faster-checkout"},
	"api": {"feat/api-87-rate-limit-the-public-webhooks-endpoint"},
}

// Setup makes a home folder in a temp dir with the demo repos, points HOME
// there and cuts herdr off, so nothing on this machine shows up or changes.
// cleanup removes it all.
func Setup() (d ui.Deps, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "linear-kickoff-demo-")
	if err != nil {
		return d, nil, err
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }
	if tmp, err = filepath.EvalSymlinks(tmp); err != nil { // /var → /private/var on macOS
		cleanup()
		return d, nil, err
	}
	home := filepath.Join(tmp, "home")
	paths := map[string]string{}
	for _, r := range repoNames {
		p := filepath.Join(home, "code", r)
		if err = os.MkdirAll(p, 0o755); err == nil {
			err = exec.Command("git", "init", "-q", p).Run()
		}
		if err != nil {
			cleanup()
			return d, nil, err
		}
		paths[filepath.Base(r)] = p
	}
	// The same agents on every machine: stubs on a PATH of their own (with
	// git), and magus installed in the demo's ~/.claude.
	bin := filepath.Join(tmp, "bin")
	_ = os.MkdirAll(bin, 0o755)
	for _, a := range demoAgents {
		_ = os.WriteFile(filepath.Join(bin, a), []byte("#!/bin/sh\n"), 0o755)
	}
	if git, gerr := exec.LookPath("git"); gerr == nil {
		_ = os.Symlink(git, filepath.Join(bin, "git"))
	}
	plugins := filepath.Join(home, ".claude", "plugins")
	_ = os.MkdirAll(plugins, 0o755)
	_ = os.WriteFile(filepath.Join(plugins, "installed_plugins.json"), []byte(`{"plugins":{"orchestrator@magus":[]}}`), 0o644)

	os.Setenv("HOME", home)
	os.Setenv("PATH", bin+":/usr/bin:/bin")
	os.Setenv("HERDR_BIN_PATH", "false") // every herdr call fails quietly
	for _, k := range []string{"HERDR_PLUGIN_CONTEXT_JSON", "HERDR_WORKSPACE_ID", "LINEAR_API_KEY", "CLAUDE_CONFIG_DIR"} {
		os.Unsetenv(k)
	}

	state := filepath.Join(tmp, "state")
	cfgDir := filepath.Join(tmp, "config")
	_ = os.MkdirAll(cfgDir, 0o755)
	// Repos picked before: the chooser offers them for the team.
	rs := repos.Load(state)
	rs.Teams[teamWeb.id] = []string{paths["web"], paths["design-system"]}
	rs.Teams[teamAPI.id] = []string{paths["api"]}
	rs.Teams[teamOps.id] = []string{paths["infra"]}
	rs.Names[teamWeb.id], rs.Names[teamAPI.id], rs.Names[teamOps.id] = teamWeb.key, teamAPI.key, teamOps.key
	if err = rs.Save(); err != nil {
		cleanup()
		return d, nil, err
	}
	// Both workspaces connected, Acme last used.
	reg := workspace.Load(state)
	for _, w := range worlds {
		reg.Add(workspace.WS{ID: w.id, Name: w.name, URLKey: w.urlKey})
	}
	reg.Last = acme.id
	if err = reg.Save(); err != nil {
		cleanup()
		return d, nil, err
	}
	clients := map[string]*Client{} // one per workspace: changes stick while the popup is open
	var mu sync.Mutex

	cfg := config.Default()
	cfg.BranchTemplate = "{type}/{id}-{slug}"
	cfg.RepoRoots = []string{"~/code"}
	cfg.Background = ""
	d = ui.Deps{
		Cfg: cfg, Secrets: noSecrets{}, ConfigDir: cfgDir, StateDir: state, PluginID: "demo",
		Connect: func(id string) linear.API {
			mu.Lock()
			defer mu.Unlock()
			if clients[id] == nil {
				clients[id] = NewClient(id)
			}
			return clients[id]
		},
		Worktrees: func(repo string) map[string]string {
			out := map[string]string{}
			for _, br := range worktrees[filepath.Base(repo)] {
				_, rest, _ := strings.Cut(br, "/")
				parts := strings.SplitN(rest, "-", 3)
				out[strings.ToUpper(parts[0]+"-"+parts[1])] = br
			}
			return out
		},
		Kickoff: fakeKickoff,
	}
	return d, cleanup, nil
}

// fakeKickoff tells what a real start would do, at about its pace.
func fakeKickoff(ctx context.Context, env kickoff.Env, id string, opt kickoff.Options, report kickoff.Report) (kickoff.Result, error) {
	step := func(s string, d time.Duration) error {
		report(s)
		select {
		case <-time.After(d):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	is, err := env.Client.Issue(ctx, id)
	if err != nil {
		return kickoff.Result{}, err
	}
	// The note keeps the result on screen (a real start closes the popup).
	res := kickoff.Result{Branch: kickoff.BranchName(env, is, opt.Type), Created: true,
		Notes: []string{"demo: nothing was created — install the plugin in herdr to start for real"}}
	if err := step("Fetching "+id+" from Linear…", 400*time.Millisecond); err != nil {
		return res, err
	}
	if err := step("Creating worktree "+res.Branch+"…", 900*time.Millisecond); err != nil {
		return res, err
	}
	if err := step("Installing packages…", 900*time.Millisecond); err != nil {
		return res, err
	}
	if opt.WorktreeOnly {
		return res, nil
	}
	if err := step("Opening a tab for "+id+"…", 400*time.Millisecond); err != nil {
		return res, err
	}
	if err := step("Updating the issue in Linear…", 400*time.Millisecond); err != nil {
		return res, err
	}
	_ = env.Client.UpdateIssue(ctx, is.ID, linear.IssueUpdate{StateID: "progress", AssigneeID: viewer.ID})
	err = step("Starting the agent…", 4*time.Second) // the README GIF ends here
	return res, err
}

// noSecrets: the demo has no keys and saves none.
type noSecrets struct{}

func (noSecrets) Get(string) (string, string)        { return "", "" }
func (noSecrets) Set(string, string) (string, error) { return "", nil }
func (noSecrets) Delete(string) error                { return nil }
func (noSecrets) Legacy() (string, string)           { return "", "" }
func (noSecrets) DeleteLegacy() error                { return nil }
