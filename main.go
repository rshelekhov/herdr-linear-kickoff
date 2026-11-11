// herdr-linear-kickoff: Linear issue → herdr worktree → an agent already on it.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/config"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/demo"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/herdr"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/kickoff"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/secret"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/sidebar"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/ui"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/workspace"
)

const defaultPluginID = "rs.linear-kickoff"

func pluginID() string {
	if id := os.Getenv("HERDR_PLUGIN_ID"); id != "" {
		return id
	}
	return defaultPluginID
}

func configDir() string {
	if d := os.Getenv("HERDR_PLUGIN_CONFIG_DIR"); d != "" {
		return d
	}
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = "herdr"
	}
	if out, err := exec.Command(bin, "plugin", "config-dir", pluginID()).Output(); err == nil {
		if d := strings.TrimSpace(string(out)); d != "" {
			return d
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "herdr", "plugins", "config", pluginID())
}

func stateDir() string {
	if d := os.Getenv("HERDR_PLUGIN_STATE_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "herdr", "plugins", pluginID())
}

// contextWorkspace is the workspace the action was invoked from.
func contextWorkspace() string {
	if raw := os.Getenv("HERDR_PLUGIN_CONTEXT_JSON"); raw != "" {
		var c struct {
			WorkspaceID string `json:"workspace_id"`
			Workspace   *struct {
				WorkspaceID string `json:"workspace_id"`
				ID          string `json:"id"`
			} `json:"workspace"`
		}
		if json.Unmarshal([]byte(raw), &c) == nil {
			if c.WorkspaceID != "" {
				return c.WorkspaceID
			}
			if c.Workspace != nil {
				if c.Workspace.WorkspaceID != "" {
					return c.Workspace.WorkspaceID
				}
				if c.Workspace.ID != "" {
					return c.Workspace.ID
				}
			}
		}
	}
	if ws := os.Getenv("HERDR_WORKSPACE_ID"); ws != "" {
		return ws
	}
	return herdr.FocusedWorkspace()
}

// clickedURL is the link a link handler was invoked for.
func clickedURL() string {
	if u := os.Getenv("HERDR_PLUGIN_CLICKED_URL"); u != "" {
		return u
	}
	var c struct {
		ClickedURL string `json:"clicked_url"`
	}
	_ = json.Unmarshal([]byte(os.Getenv("HERDR_PLUGIN_CONTEXT_JSON")), &c)
	return c.ClickedURL
}

// syncSidebar reports $issue / $issue_state / $issue_title for workspaces on
// an issue's worktree. At startup it refreshes everything (herdr forgot the
// tokens); on focus it asks Linear only for states older than a few minutes.
func syncSidebar(startup bool) error {
	dir := configDir()
	cfg, _ := config.Load(dir)
	if !cfg.Labels {
		return nil
	}
	wss, err := herdr.Workspaces()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	report := func(ws string, tokens map[string]string) error {
		return herdr.ReportTokens(ws, "plugin:"+pluginID(), tokens)
	}
	// The key is read only if Linear is actually asked: focus events are
	// frequent, and a keychain read is a process of its own.
	return sidebar.Sync(ctx, stateDir(), wss, &lazyLinear{dir: dir, state: stateDir()}, startup, report)
}

// lazyLinear asks every connected workspace about the issues, reading the
// keys on first use.
type lazyLinear struct{ dir, state string }

func (l *lazyLinear) Briefs(ctx context.Context, ids []string) ([]linear.IssueBrief, error) {
	keys := secret.Store{Dir: l.dir}
	var clients []*linear.Client
	for _, w := range workspace.Load(l.state).List {
		if key, _ := keys.Get(w.ID); key != "" {
			clients = append(clients, linear.New(key))
		}
	}
	if len(clients) == 0 { // the single key, before the popup moves it to its workspace
		if key, _ := keys.Legacy(); key != "" {
			clients = append(clients, linear.New(key))
		}
	}
	// An issue ID belongs to one workspace: each is asked for those still
	// unanswered. No key yet: nothing found, and the cached states stay.
	var out []linear.IssueBrief
	left := ids
	var firstErr error
	for _, c := range clients {
		if len(left) == 0 {
			break
		}
		got, err := c.Briefs(ctx, left)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, got...)
		found := map[string]bool{}
		for _, b := range got {
			found[b.ID] = true
		}
		left = slices.DeleteFunc(slices.Clone(left), func(id string) bool { return found[id] })
	}
	if len(out) == 0 {
		return nil, firstErr
	}
	return out, nil
}

// logout forgets every workspace's key (and the pre-workspace one).
func logout() error {
	keys := secret.Store{Dir: configDir()}
	reg := workspace.Load(stateDir())
	var firstErr error
	for _, w := range reg.List {
		if err := keys.Delete(w.ID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := keys.DeleteLegacy(); err != nil && firstErr == nil {
		firstErr = err
	}
	if firstErr != nil {
		return firstErr
	}
	n := len(reg.List)
	reg.List, reg.Repos, reg.Last = nil, map[string]string{}, ""
	if err := reg.Save(); err != nil {
		return err
	}
	_ = herdr.Run(nil, "notification", "show", "Linear kickoff", "--body", "API keys removed")
	fmt.Printf("API keys removed (%d workspaces).\n", n)
	return nil
}

const usage = `herdr-linear-kickoff — Linear issue → worktree → agent

  open     open the issue picker popup (herdr action)
           --goto  start with the "go to issue" input
           --link  open the clicked Linear link's issue
  ui       the popup itself (pane entrypoint)
  sync     refresh the sidebar's issue tokens (herdr hook; --startup: all)
  logout   forget the saved API keys (every workspace)
  demo     the popup on a made-up workspace: no key, nothing of yours
           --background=#rrggbb  the popup's background (else the terminal's)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "open":
		env := map[string]string{"LK_SOURCE_WORKSPACE": contextWorkspace()}
		for _, a := range os.Args[2:] {
			switch a {
			case "--goto": // by-id action: start with the "go to issue" input
				env["LK_GOTO"] = "1"
			case "--link": // a Ctrl-clicked Linear link: open that issue
				if u := clickedURL(); u != "" { // the link: it names its workspace too
					if _, ok := linear.ParseIdentifier(u); ok {
						env["LK_OPEN"] = u
					}
				}
			}
		}
		err = herdr.OpenPopup(pluginID(), "picker", "85%", "80%", env)
	case "ui":
		dir := configDir()
		cfg, cfgErr := config.Load(dir)
		ws := os.Getenv("LK_SOURCE_WORKSPACE")
		if ws == "" {
			ws = contextWorkspace()
		}
		repo, _ := kickoff.RepoFor(kickoff.Env{Cfg: cfg, SourceWorkspace: ws}, "")
		err = ui.Run(ui.Deps{
			Cfg: cfg, CfgErr: cfgErr, Secrets: secret.Store{Dir: dir}, ConfigDir: dir,
			SourceWorkspace: ws, SourceRepo: repo, StateDir: stateDir(), PluginID: pluginID(),
			Background: config.ResolveBackground(cfg.Background, config.HerdrConfigPath()),
			OpenID:     os.Getenv("LK_OPEN"),
			StartGoto:  os.Getenv("LK_GOTO") == "1",
		})
	case "demo":
		d, cleanup, derr := demo.Setup()
		if derr != nil {
			err = derr
			break
		}
		for _, a := range os.Args[2:] { // --background=#rrggbb: as herdr's theme would set it
			if bg, ok := strings.CutPrefix(a, "--background="); ok {
				d.Background = bg
			}
		}
		err = ui.Run(d)
		cleanup()
	case "sync": // a herdr hook: keep the sidebar's issue tokens current
		err = syncSidebar(slices.Contains(os.Args[2:], "--startup"))
	case "logout":
		err = logout()
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "linear-kickoff:", err)
		os.Exit(1)
	}
}
