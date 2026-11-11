// Package herdr talks to the running herdr server: through its CLI for most
// things, and through its socket for what the CLI can't do (popups).
package herdr

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Error is an error reported by herdr itself.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("herdr: %s (%s)", e.Message, e.Code) }

// IsCode reports whether err is a herdr error with the given code.
func IsCode(err error, code string) bool {
	if e, ok := err.(*Error); ok {
		return e.Code == code
	}
	return false
}

func bin() string {
	if p := os.Getenv("HERDR_BIN_PATH"); p != "" {
		return p
	}
	return "herdr"
}

type reply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decode(raw []byte, out any) error {
	var r reply
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("herdr: unexpected output: %s", strings.TrimSpace(string(raw)))
	}
	if r.Error != nil {
		return &Error{r.Error.Code, r.Error.Message}
	}
	if out != nil && len(r.Result) > 0 {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// Run runs a herdr CLI command and decodes its `result` into out.
func Run(out any, args ...string) error {
	cmd := exec.Command(bin(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if stdout.Len() > 0 {
		if derr := decode(stdout.Bytes(), out); derr != nil || err == nil {
			return derr
		}
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("herdr %s: %s", args[0], msg)
	}
	return nil
}

func socketPath() string {
	if p := os.Getenv("HERDR_SOCKET_PATH"); p != "" {
		return p
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "herdr", "herdr.sock")
}

// Call sends one request over herdr's socket.
func Call(method string, params, out any) error {
	conn, err := net.DialTimeout("unix", socketPath(), 3*time.Second)
	if err != nil {
		return fmt.Errorf("herdr socket: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if params == nil {
		params = map[string]any{}
	}
	req, err := json.Marshal(map[string]any{"id": "linear-kickoff", "method": method, "params": params})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return err
	}
	line, err := bufio.NewReaderSize(conn, 1<<20).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return fmt.Errorf("herdr %s: %w", method, err)
	}
	return decode(line, out)
}

// OpenPopup opens one of the plugin's pane entrypoints as a popup. The menu
// the action was picked from may still be closing, so ui_busy is retried.
func OpenPopup(pluginID, entrypoint, width, height string, env map[string]string) error {
	params := map[string]any{
		"plugin_id": pluginID, "entrypoint": entrypoint, "placement": "popup",
		"focus": true, "width": width, "height": height, "env": env,
	}
	var err error
	for range 4 {
		if err = Call("plugin.pane.open", params, nil); err == nil || !IsCode(err, "ui_busy") {
			return err
		}
		time.Sleep(150 * time.Millisecond)
	}
	return err
}

// Workspace is an entry of `herdr workspace list`.
type Workspace struct {
	ID       string `json:"workspace_id"`
	Label    string `json:"label"`
	Focused  bool   `json:"focused"`
	Worktree *struct {
		CheckoutPath string `json:"checkout_path"`
		IsLinked     bool   `json:"is_linked_worktree"`
	} `json:"worktree,omitempty"`
}

// CheckoutPath is the git checkout the workspace is on, "" if none.
func (w Workspace) CheckoutPath() string {
	if w.Worktree == nil {
		return ""
	}
	return w.Worktree.CheckoutPath
}

// Pane is an entry of `herdr pane list`.
type Pane struct {
	ID            string `json:"pane_id"`
	Focused       bool   `json:"focused"`
	Agent         string `json:"agent"` // agent running in the pane ("claude", "codex"), "" for a shell
	Cwd           string `json:"cwd"`
	ForegroundCwd string `json:"foreground_cwd"`
}

// Worktree is an entry of `herdr worktree list`.
type Worktree struct {
	Branch          string `json:"branch"`
	Path            string `json:"path"`
	Label           string `json:"label"`
	OpenWorkspaceID string `json:"open_workspace_id"`
	IsLinked        bool   `json:"is_linked_worktree"` // false for the repo's main folder
}

// Opened is what `worktree create` / `worktree open` return.
type Opened struct {
	Workspace struct {
		ID string `json:"workspace_id"`
	} `json:"workspace"`
	RootPane struct {
		ID string `json:"pane_id"`
	} `json:"root_pane"`
}

func Workspaces() ([]Workspace, error) {
	var r struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	err := Run(&r, "workspace", "list")
	return r.Workspaces, err
}

// FocusedWorkspace returns the id of the focused workspace, or "".
func FocusedWorkspace() string {
	ws, err := Workspaces()
	if err != nil {
		return ""
	}
	for _, w := range ws {
		if w.Focused {
			return w.ID
		}
	}
	return ""
}

// WorkspaceHasAgent: some pane of the workspace runs an agent.
func WorkspaceHasAgent(workspace string) (bool, error) {
	var r struct {
		Panes []Pane `json:"panes"`
	}
	if err := Run(&r, "pane", "list", "--workspace", workspace); err != nil {
		return false, err
	}
	for _, p := range r.Panes {
		if p.Agent != "" {
			return true, nil
		}
	}
	return false, nil
}

// WorkspaceCwd is the working directory of the workspace's focused pane.
func WorkspaceCwd(workspace string) (string, error) {
	var r struct {
		Panes []Pane `json:"panes"`
	}
	if err := Run(&r, "pane", "list", "--workspace", workspace); err != nil {
		return "", err
	}
	if len(r.Panes) == 0 {
		return "", fmt.Errorf("workspace %s has no panes", workspace)
	}
	p := r.Panes[0]
	for _, c := range r.Panes {
		if c.Focused {
			p = c
			break
		}
	}
	if p.ForegroundCwd != "" {
		return p.ForegroundCwd, nil
	}
	return p.Cwd, nil
}

// Worktrees lists the worktrees of the repo at cwd.
func Worktrees(cwd string) ([]Worktree, error) {
	var r struct {
		Worktrees []Worktree `json:"worktrees"`
	}
	err := Run(&r, "worktree", "list", "--cwd", cwd)
	return r.Worktrees, err
}

// CreateWorktree creates and focuses a worktree on a new branch.
func CreateWorktree(cwd, branch, base, label string) (Opened, error) {
	args := []string{"worktree", "create", "--cwd", cwd, "--branch", branch, "--label", label, "--focus"}
	if base != "" {
		args = append(args, "--base", base)
	}
	var o Opened
	err := Run(&o, args...)
	return o, err
}

// OpenWorktree opens (or focuses) an existing worktree.
func OpenWorktree(cwd, path, label string) (Opened, error) {
	var o Opened
	err := Run(&o, "worktree", "open", "--cwd", cwd, "--path", path, "--label", label, "--focus")
	return o, err
}

// CreateTab opens a new focused tab in a workspace and returns its root pane.
func CreateTab(workspace, cwd, label string) (string, error) {
	var r struct {
		RootPane struct {
			ID string `json:"pane_id"`
		} `json:"root_pane"`
	}
	err := Run(&r, "tab", "create", "--workspace", workspace, "--cwd", cwd, "--label", label, "--focus")
	return r.RootPane.ID, err
}

func RenameWorkspace(id, name string) error {
	return Run(nil, "workspace", "rename", id, name)
}

func RunInPane(pane, command string) error {
	return Run(nil, "pane", "run", pane, command)
}

// ReportTokens sets display-only metadata tokens on a workspace.
func ReportTokens(workspace, source string, tokens map[string]string) error {
	args := []string{"workspace", "report-metadata", workspace, "--source", source}
	for k, v := range tokens {
		args = append(args, "--token", k+"="+v)
	}
	return Run(nil, args...)
}
