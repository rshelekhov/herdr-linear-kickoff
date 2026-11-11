// Package kickoff turns a Linear issue into a herdr worktree with an agent
// already working on it.
package kickoff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/branch"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/config"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/herdr"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/repos"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/sidebar"
)

// Env is what a kickoff needs from its surroundings.
type Env struct {
	Cfg             config.Config
	Client          linear.API
	Viewer          linear.Viewer
	SourceWorkspace string // workspace the plugin was opened from
	StateDir        string
	PluginID        string
}

// Options for one start.
type Options struct {
	Type string // overrides {type}; "" = from labels
	Repo string // the repo to start in, as chosen in the UI; "" = RepoFor
	// WorktreeOnly (w): open or create the worktree and stop there — no
	// Linear changes, no agent; a new worktree still gets its packages.
	WorktreeOnly bool
	// AgentCmd and Launch override the configured ones for this start ("A").
	AgentCmd, Launch string
}

// Result says what happened.
type Result struct {
	Branch    string
	Workspace string
	Created   bool
	Notes     []string // non-fatal problems, shown to the user
}

// Report receives progress lines.
type Report func(string)

// RepoFor resolves the repo an issue's worktree belongs to: the team mapping
// first, else the repo of the source workspace.
func RepoFor(env Env, teamKey string) (string, error) {
	if p, ok := env.Cfg.Repos[teamKey]; ok && p != "" {
		root, err := gitRoot(p)
		if err != nil {
			return "", fmt.Errorf("repos.%s = %s: not a git repository", teamKey, p)
		}
		return root, nil
	}
	ws := env.SourceWorkspace
	if ws == "" {
		return "", errors.New("could not determine the current workspace")
	}
	cwd, err := herdr.WorkspaceCwd(ws)
	if err != nil {
		return "", err
	}
	root, err := gitRoot(cwd)
	if err != nil {
		return "", fmt.Errorf("current workspace is not in a git repository (%s); set repos.%s in config.toml", cwd, teamKey)
	}
	return root, nil
}

// BranchName renders the configured branch name for an issue.
func BranchName(env Env, is linear.Issue, typ string) string {
	if typ == "" {
		typ = branch.TypeFor(is.LabelNames(), env.Cfg.TypeMap, env.Cfg.TypeDefault)
	}
	me := env.Viewer.DisplayName
	if me == "" {
		me = env.Viewer.Name
	}
	return branch.Render(env.Cfg.BranchTemplate, branch.Vars{
		Linear: is.BranchName, Identifier: is.Identifier, Team: is.Team.Key, Number: is.Number,
		Title: is.Title, Type: typ, Me: me, SlugMax: env.Cfg.SlugMax,
	})
}

// WorkspaceName names an issue's herdr workspace from workspace_name:
// {ID} ABC-12, {id} abc-12, {title} (up to 40 characters), {team}, {number}.
func WorkspaceName(cfg config.Config, is linear.Issue) string {
	title := []rune(strings.Join(strings.Fields(is.Title), " "))
	if len(title) > 40 { // at a word break, if there's one late enough
		cut := 39
		if i := strings.LastIndex(string(title[:40]), " "); i > 20 {
			cut = len([]rune(string(title[:40])[:i]))
		}
		title = append(title[:cut], '…')
	}
	name := strings.NewReplacer(
		"{ID}", is.Identifier, "{id}", strings.ToLower(is.Identifier), "{title}", string(title),
		"{team}", is.Team.Key, "{number}", strconv.Itoa(is.Number),
	).Replace(cfg.WorkspaceName)
	if name = strings.Join(strings.Fields(name), " "); name == "" {
		return is.Identifier
	}
	return name
}

// FindWorktree finds an existing worktree for the issue: by exact branch,
// else by a branch that mentions the identifier (so a changed template
// doesn't make a second one).
func FindWorktree(wts []herdr.Worktree, branchName, identifier string) (herdr.Worktree, bool) {
	for _, w := range wts {
		if w.Branch == branchName {
			return w, true
		}
	}
	for _, w := range wts {
		if branch.MentionsIssue(w.Branch, identifier) {
			return w, true
		}
	}
	return herdr.Worktree{}, false
}

// Start runs the whole kickoff. Order: worktree, then Linear, then the
// agent, so a failure never leaves Linear changed without a worktree.
func Start(ctx context.Context, env Env, identifier string, opt Options, report Report) (Result, error) {
	var res Result
	if opt.AgentCmd != "" {
		env.Cfg.AgentCmd = opt.AgentCmd
	}
	if opt.Launch != "" {
		env.Cfg.Launch = opt.Launch
	}
	report("Fetching " + identifier + " from Linear…")
	is, err := env.Client.Issue(ctx, identifier)
	if err != nil {
		return res, err
	}
	// home is the issue whose branch and worktree we use: the issue itself, or
	// its parent when sub-issues share the parent's branch.
	home := is
	if SharesParentBranch(env.Cfg, is) {
		report("Fetching parent " + is.Parent.Identifier + "…")
		if home, err = env.Client.Issue(ctx, is.Parent.Identifier); err != nil {
			return res, err
		}
	}
	if err := CheckLaunch(env.Cfg); err != nil && !opt.WorktreeOnly { // before anything is created
		return res, err
	}
	if Magus(env.Cfg) && !opt.WorktreeOnly {
		if _, err := MagusPrompt(is); err != nil { // likewise
			return res, err
		}
	}
	// "Mine or someone else's" and {me} need the viewer; the popup loads it
	// in the background, so Start may run before it has arrived.
	if env.Viewer.ID == "" {
		if env.Viewer, err = env.Client.Viewer(ctx); err != nil {
			return res, fmt.Errorf("couldn't load your Linear account: %w", err)
		}
	}
	repo := opt.Repo
	if repo == "" {
		if repo, err = RepoFor(env, home.Team.Key); err != nil {
			return res, err
		}
	} else if top, ok := repos.RepoRoot(repo); !ok { // a worktree's path → its main repo
		return res, fmt.Errorf("%s is not a git repository", repo)
	} else {
		repo = top
	}
	res.Branch = BranchName(env, home, opt.Type)
	if res.Branch == "" {
		return res, errors.New("branch template produced an empty name")
	}

	wts, err := herdr.Worktrees(repo)
	if err != nil {
		return res, err
	}
	var opened herdr.Opened
	wtPath, wasOpen := "", ""
	if w, ok := FindWorktree(wts, res.Branch, home.Identifier); ok {
		if !w.IsLinked {
			// The repo's main folder is on the issue's branch: that folder is not
			// the issue's worktree (whatever is open there is other work), and
			// git won't check the branch out a second time. Switching it is the
			// user's call — there may be uncommitted work.
			return res, MainCheckoutError{Branch: w.Branch, Path: w.Path}
		}
		res.Branch, wtPath, wasOpen = w.Branch, w.Path, w.OpenWorkspaceID
		report("Opening existing worktree " + w.Branch + "…")
		if opened, err = herdr.OpenWorktree(repo, w.Path, WorkspaceName(env.Cfg, home)); err != nil {
			return res, err
		}
	} else {
		base, note := resolveBase(repo, res.Branch, env.Cfg.Base, report)
		if note != "" {
			res.Notes = append(res.Notes, note)
		}
		report("Creating worktree " + res.Branch + "…")
		if opened, err = herdr.CreateWorktree(repo, res.Branch, base, WorkspaceName(env.Cfg, home)); err != nil {
			return res, err
		}
		res.Created = true
	}
	res.Workspace = opened.Workspace.ID
	if res.Workspace != "" {
		_ = herdr.RenameWorkspace(res.Workspace, WorkspaceName(env.Cfg, home))
	}

	stateName := is.State.Name
	if !opt.WorktreeOnly {
		if s, note := updateLinear(ctx, env, is, report); note != "" {
			res.Notes = append(res.Notes, note)
		} else if s != "" {
			stateName = s
		}
	}

	if env.Cfg.Labels && res.Workspace != "" {
		state := homeState(home, is, stateName)
		_ = herdr.ReportTokens(res.Workspace, "plugin:"+env.PluginID, map[string]string{
			"issue": home.Identifier, "issue_state": state, "issue_title": home.Title,
		})
		rememberForSidebar(env.StateDir, res.Workspace, home, state)
	}

	pane := opened.RootPane.ID
	if opt.WorktreeOnly {
		return res, installOnly(env, is, res, pane, report)
	}
	if !res.Created {
		agentBusy := false
		if wasOpen != "" && home.Identifier == is.Identifier {
			busy, err := herdr.WorkspaceHasAgent(res.Workspace)
			agentBusy = busy || err != nil // unsure: never start a second agent
		}
		switch ExistingWorktreeAction(wasOpen != "", agentBusy, home.Identifier != is.Identifier) {
		case FocusOnly:
			res.Notes = append(res.Notes, "an agent is already working in "+res.Branch+" — nothing sent to it")
			return res, nil
		case NewTab:
			report("Opening a tab for " + is.Identifier + "…")
			if pane, err = herdr.CreateTab(res.Workspace, wtPath, is.Identifier); err != nil {
				return res, err
			}
		case RootPane: // the workspace was just opened: its first pane is a fresh shell
		}
	}
	if pane == "" {
		return res, errors.New("herdr returned no pane for the worktree — agent not started")
	}
	report("Starting the agent…")
	prompt := RenderPrompt(env.Cfg.PromptTemplate, is, res.Branch)
	if Magus(env.Cfg) {
		if prompt, err = MagusPrompt(is); err != nil { // the manager reads the issue itself
			return res, err
		}
	}
	pf, err := writePrompt(env.StateDir, is, prompt)
	if err != nil {
		return res, err
	}
	script := strings.TrimSuffix(pf, ".md") + ".sh"
	if err := os.WriteFile(script, []byte(StartScript(env.Cfg, pf, res.Created)), 0o600); err != nil {
		return res, err
	}
	if err := herdr.RunInPane(pane, AgentCommand(script)); err != nil {
		return res, err
	}
	return res, nil
}

// installOnly installs a new worktree's packages in its first pane, and
// leaves the shell to the user. Nothing for an existing worktree.
func installOnly(env Env, is linear.Issue, res Result, pane string, report Report) error {
	inst := installScript(env.Cfg)
	if !res.Created || inst == "" || pane == "" {
		return nil
	}
	report("Installing packages…")
	dir := filepath.Join(env.StateDir, "prompts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	script := filepath.Join(dir, branch.Clean(is.Identifier)+"-install.sh")
	if err := os.WriteFile(script, []byte("# herdr-linear-kickoff: dependencies\n"+inst), 0o600); err != nil {
		return err
	}
	return herdr.RunInPane(pane, AgentCommand(script))
}

// rememberForSidebar links the workspace's checkout to the issue, so the
// tokens come back after a herdr restart and follow its state (see sidebar).
func rememberForSidebar(stateDir, workspace string, is linear.Issue, state string) {
	wss, err := herdr.Workspaces()
	if err != nil {
		return
	}
	for _, ws := range wss {
		if ws.ID == workspace && ws.CheckoutPath() != "" {
			_, _ = sidebar.Update(stateDir, func(st *sidebar.Store) {
				st.Record(ws.CheckoutPath(), is.ID, is.Identifier, is.Title, state)
			})
			return
		}
	}
}

// Magus: Start launches magus orchestrator's manager instead of a prompt.
func Magus(cfg config.Config) bool {
	return strings.EqualFold(strings.TrimSpace(cfg.Launch), "magus")
}

var (
	reIdentifier = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)
	reIssueURL   = regexp.MustCompile(`^https://linear\.app/[A-Za-z0-9_-]+/issue/[A-Z][A-Z0-9]*-[0-9]+(/[A-Za-z0-9_-]*)?$`)
)

// MagusPrompt is the manager's launch line: /orchestrator:manager <id> <url>.
// No title: anyone in the workspace writes it, and this line is a command,
// not fenced data — the manager reads the title from Linear itself. The ID
// and URL must look exactly like Linear's.
func MagusPrompt(is linear.Issue) (string, error) {
	if !reIdentifier.MatchString(is.Identifier) || !reIssueURL.MatchString(is.URL) {
		return "", fmt.Errorf("unexpected issue ID or URL from Linear (%q, %q): not starting magus", is.Identifier, is.URL)
	}
	return "/orchestrator:manager " + is.Identifier + " " + is.URL, nil
}

// CheckLaunch refuses a magus start that can't work, before a worktree is
// made: magus runs inside Claude Code, with its orchestrator plugin.
func CheckLaunch(cfg config.Config) error {
	if !Magus(cfg) {
		return nil
	}
	if bin := filepath.Base(strings.Fields(cfg.AgentCmd + " x")[0]); bin != "claude" && bin != "claudish" {
		return fmt.Errorf("launch = \"magus\" needs Claude Code: set the agent to claude or claudish (now %q)", cfg.AgentCmd)
	}
	if !OrchestratorInstalled() {
		return errors.New("launch = \"magus\" needs magus orchestrator in Claude Code: /plugin install orchestrator@magus")
	}
	return nil
}

// claudeDir is Claude Code's config folder; a var so tests can point it elsewhere.
var claudeDir = func() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// OrchestratorInstalled: Claude Code lists magus's orchestrator plugin.
func OrchestratorInstalled() bool {
	b, err := os.ReadFile(filepath.Join(claudeDir(), "plugins", "installed_plugins.json"))
	if err != nil {
		return false
	}
	var r struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	if json.Unmarshal(b, &r) != nil {
		return false
	}
	for k := range r.Plugins {
		if strings.HasPrefix(k, "orchestrator@") {
			return true
		}
	}
	return false
}

// MainCheckoutError: the issue's branch is checked out in the repo's main
// folder, so it can't get a worktree of its own.
type MainCheckoutError struct{ Branch, Path string }

func (e MainCheckoutError) Error() string {
	return fmt.Sprintf("%s is checked out in the repo's main folder %s — switch that folder to another branch (e.g. git switch main) and start again to get a separate worktree",
		e.Branch, e.Path)
}

// Action is what Start does with an issue's worktree that already existed.
type Action int

const (
	RootPane  Action = iota // start the agent in the workspace's fresh first pane
	NewTab                  // start it in a new tab, next to what is open there
	FocusOnly               // an agent is already on it: just focus, send nothing
)

// ExistingWorktreeAction: wasOpen — the worktree already had a workspace before
// this start; agentBusy — an agent runs in it; subIssue — a sub-issue working
// in its parent's worktree (it always gets its own tab, even beside an agent).
func ExistingWorktreeAction(wasOpen, agentBusy, subIssue bool) Action {
	switch {
	case !wasOpen:
		return RootPane
	case subIssue:
		return NewTab
	case agentBusy:
		return FocusOnly
	}
	return NewTab
}

// SharesParentBranch: the issue is a sub-issue started on its parent's branch.
func SharesParentBranch(cfg config.Config, is linear.Issue) bool {
	return is.Parent != nil && strings.EqualFold(strings.TrimSpace(cfg.SubIssueBranch), "parent")
}

// homeState is the state shown on the workspace: the started issue's, unless
// the workspace belongs to its parent.
func homeState(home, is linear.Issue, started string) string {
	if home.Identifier == is.Identifier {
		return started
	}
	return home.State.Name
}

// installAuto installs JS dependencies with the package manager the project
// uses (by its lockfile), and skips with a note when that tool isn't there —
// running another one would install differently and leave a stray lockfile.
const installAuto = `pm=
if [ -e bun.lock ] || [ -e bun.lockb ]; then pm=bun
elif [ -e pnpm-lock.yaml ]; then pm=pnpm
elif [ -e yarn.lock ]; then pm=yarn
elif [ -e package-lock.json ]; then pm=npm
fi
if [ -n "$pm" ]; then
  if command -v "$pm" >/dev/null 2>&1; then
    "$pm" install || echo "linear-kickoff: $pm install failed — going on"
  else
    echo "linear-kickoff: this project uses $pm, which isn't installed — skipping dependencies"
  fi
fi
`

// StartScript is the POSIX sh script run in the worktree's pane: install
// dependencies (only in a worktree just created — an existing one has them,
// and may have a dev server or another agent using them), then replace itself
// with the agent (so quitting the agent lands back in the user's shell).
func StartScript(cfg config.Config, promptFile string, install bool) string {
	var b strings.Builder
	b.WriteString("# herdr-linear-kickoff: dependencies, then the agent with the issue as its prompt\n")
	if install {
		b.WriteString(installScript(cfg))
	}
	fmt.Fprintf(&b, "exec %s \"$(cat %s)\"\n", cfg.AgentCmd, ShellQuote(promptFile))
	return b.String()
}

// installScript is the configured install step as sh, "" for none.
func installScript(cfg config.Config) string {
	switch inst := strings.TrimSpace(cfg.Install); {
	case inst == "" || strings.EqualFold(inst, "none"):
		return ""
	case strings.EqualFold(inst, "auto"):
		return installAuto
	case cfg.InstallIf != "":
		return fmt.Sprintf("if [ -e %s ]; then\n  %s || echo \"linear-kickoff: install failed — going on\"\nfi\n",
			ShellQuote(cfg.InstallIf), inst)
	default:
		return inst + " || echo \"linear-kickoff: install failed — going on\"\n"
	}
}

// AgentCommand is the line typed into the pane: running the start script
// with sh works whatever the user's shell is (fish, nushell, …).
func AgentCommand(scriptFile string) string { return "sh " + ShellQuote(scriptFile) }

// ShellQuote quotes s for POSIX sh.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func writePrompt(stateDir string, is linear.Issue, text string) (string, error) {
	dir := filepath.Join(stateDir, "prompts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, branch.Clean(is.Identifier)+".md")
	return p, os.WriteFile(p, []byte(text), 0o600)
}

// updateLinear moves the issue to the team's first started state and assigns
// it to the viewer, as configured. Returns the new state name, or a note when
// something was skipped or failed (the worktree exists by now either way).
func updateLinear(ctx context.Context, env Env, is linear.Issue, report Report) (string, string) {
	if is.State.Type == "completed" || is.State.Type == "canceled" {
		return "", "issue is closed — status left unchanged"
	}
	var in linear.IssueUpdate
	newState := ""
	if env.Cfg.StartAssignsMe && is.Assignee == nil && env.Viewer.ID != "" {
		in.AssigneeID = env.Viewer.ID
	}
	othersIssue := is.Assignee != nil && is.Assignee.ID != env.Viewer.ID
	if env.Cfg.StartSetsInProgress && is.State.Type != "started" && !othersIssue {
		states, err := env.Client.TeamStates(ctx, is.Team.ID)
		if err != nil {
			return "", "could not fetch team states: " + err.Error()
		}
		for _, s := range states {
			if s.Type == "started" {
				in.StateID, newState = s.ID, s.Name
				break
			}
		}
	}
	if in == (linear.IssueUpdate{}) {
		if othersIssue && env.Cfg.StartSetsInProgress && is.State.Type != "started" {
			return "", "issue is assigned to " + is.Assignee.Name + " — status left unchanged"
		}
		return "", ""
	}
	report("Updating the issue in Linear…")
	if err := env.Client.UpdateIssue(ctx, is.ID, in); err != nil {
		return "", "Linear not updated: " + err.Error()
	}
	return newState, ""
}

// resolveBase picks what a new branch starts from: the branch itself on
// origin if someone already pushed it, a local branch as is, else the
// configured base (fetched first).
func resolveBase(repo, name, base string, report Report) (string, string) {
	if git(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+name) == nil {
		return "", ""
	}
	report("git fetch…")
	if git(repo, "fetch", "--quiet", "origin", "+refs/heads/"+name+":refs/remotes/origin/"+name) == nil {
		return "origin/" + name, ""
	}
	switch strings.ToLower(base) {
	case "head":
		return "", ""
	case "":
		if base = defaultBase(repo); base == "" {
			return "", "no origin/HEAD, origin/main or origin/master — branching from the current HEAD"
		}
	}
	if remote, br, ok := strings.Cut(base, "/"); ok && remote == "origin" {
		if err := git(repo, "fetch", "--quiet", remote, br); err != nil {
			return base, "git fetch failed — branching from local copy of " + base
		}
	}
	return base, ""
}

// defaultBase is origin's default branch: origin/HEAD when git knows it,
// else origin/main or origin/master, whichever exists; "" when none does.
func defaultBase(repo string) string {
	if ref, err := gitOut(repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && ref != "" {
		return ref
	}
	for _, ref := range []string{"origin/main", "origin/master"} {
		if git(repo, "rev-parse", "--verify", "--quiet", "refs/remotes/"+ref) == nil {
			return ref
		}
	}
	return ""
}

func git(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd.Run()
}

func gitOut(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// gitRoot is the main checkout of the repo dir is in — for a linked
// worktree, the repo it belongs to, not the worktree (which goes away).
func gitRoot(dir string) (string, error) {
	root, ok := repos.RepoRoot(dir)
	if !ok {
		return "", errors.New("not a git repository")
	}
	return root, nil
}

// IssueWorktrees maps every issue that has a worktree in repo to that
// worktree's branch (list marker and preview). One herdr call per repo, and
// no matching per issue: each branch says which issues it's for.
func IssueWorktrees(repo string) map[string]string {
	out := map[string]string{}
	wts, err := herdr.Worktrees(repo)
	if err != nil {
		return out
	}
	for _, w := range wts {
		if !w.IsLinked { // the main folder is no issue's worktree
			continue
		}
		for _, id := range branch.Issues(w.Branch) {
			if _, ok := out[id]; !ok {
				out[id] = w.Branch
			}
		}
	}
	return out
}
