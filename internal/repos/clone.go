package repos

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/config"
)

// A repo can be cloned from the chooser when it isn't on this machine yet.

var (
	reShorthand = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`) // org/repo
	reURLName   = regexp.MustCompile(`([^/:]+?)(\.git)?/?$`)
)

// CloneSource is what to clone, as typed: a URL or GitHub's org/repo.
type CloneSource struct {
	Raw       string
	Shorthand bool   // org/repo: cloned with gh (private repos work), else https
	Name      string // the folder name it gets
}

// ParseCloneSource checks what was typed. It refuses anything git could read
// as an option or a command-running transport.
func ParseCloneSource(s string) (CloneSource, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return CloneSource{}, errors.New("paste a git URL or org/repo")
	case strings.HasPrefix(s, "-"):
		return CloneSource{}, errors.New("not a git URL")
	case strings.HasPrefix(strings.ToLower(s), "ext::"), strings.HasPrefix(strings.ToLower(s), "fd::"):
		return CloneSource{}, errors.New("that git transport runs commands; not allowed")
	case reShorthand.MatchString(s):
		return CloneSource{Raw: s, Shorthand: true, Name: strings.TrimSuffix(s[strings.Index(s, "/")+1:], ".git")}, nil
	case strings.Contains(s, "://"), strings.HasPrefix(s, "git@"):
		m := reURLName.FindStringSubmatch(s)
		if m == nil || m[1] == "" {
			return CloneSource{}, errors.New("can't tell the repo's name from that URL")
		}
		return CloneSource{Raw: s, Name: m[1]}, nil
	}
	return CloneSource{}, errors.New("paste a git URL (git@…, https://…) or org/repo")
}

// CloneRoot is where a new clone goes: clone_root if set, else next to the
// team's most recent repo, else ~/code.
func CloneRoot(configured string, teamRepos []string) string {
	if configured != "" {
		return config.ExpandHome(configured)
	}
	if len(teamRepos) > 0 {
		return filepath.Dir(teamRepos[0])
	}
	return config.ExpandHome("~/code")
}

// Clone clones src into root/<name> and returns the path. An existing folder
// is used only if it already is that repo. progress gets git's progress lines
// ("Receiving objects:  45% …") as they come; cancelling ctx stops the clone
// and removes what it left behind.
func Clone(ctx context.Context, src CloneSource, root string, progress func(string)) (string, error) {
	dest := filepath.Join(root, src.Name)
	if _, err := os.Stat(dest); err == nil {
		if gitKind(dest) == kindRepo && sameRemote(dest, src) {
			return dest, nil
		}
		return "", fmt.Errorf("%s already exists and isn't that repo", Tilde(dest))
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	var cmd *exec.Cmd
	if _, err := exec.LookPath("gh"); src.Shorthand && err == nil {
		cmd = exec.CommandContext(ctx, "gh", "repo", "clone", src.Raw, dest, "--", "--progress")
	} else {
		url := src.Raw
		if src.Shorthand {
			url = "https://github.com/" + src.Raw + ".git"
		}
		cmd = exec.CommandContext(ctx, "git", "-c", "protocol.ext.allow=never", "clone", "--progress", "--", url, dest)
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0") // fail, don't hang on a password prompt
	// gh runs git as a child: cancelling must stop the whole group, or git
	// keeps writing into the folder that is about to be removed.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return "", errors.New("clone cancelled")
		}
		return "", err
	}
	// git redraws its progress with \r: split on both \r and \n.
	sc := bufio.NewScanner(stderr)
	sc.Split(splitCRLF)
	last := ""
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			last = line
			if progress != nil {
				progress(line)
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		_ = os.RemoveAll(dest) // it didn't exist before: don't leave half a clone
		if ctx.Err() != nil {
			return "", errors.New("clone cancelled")
		}
		return "", fmt.Errorf("clone failed: %s", lastLine(last, err))
	}
	return dest, nil
}

// splitCRLF is bufio.ScanLines that also ends a line at \r.
func splitCRLF(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// sameRemote: the folder's origin points at the source (by repo path).
func sameRemote(dir string, src CloneSource) bool {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return false
	}
	norm := func(s string) string {
		s = strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(s), "/"), ".git"))
		if i := strings.LastIndexAny(s, ":/"); i >= 0 {
			if j := strings.LastIndexAny(s[:i], ":/"); j >= 0 {
				return s[j+1:] // org/repo
			}
		}
		return s
	}
	want := src.Raw
	return norm(string(out)) == norm(want)
}

func lastLine(out string, err error) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return l
	}
	return err.Error()
}

// NavigatorRoots are the [[roots]] of the herdr-navigator plugin, if it's
// configured: folders the user already told herdr their projects live in.
// Only existing ones.
func NavigatorRoots(herdrConfigDir string) []string {
	var c struct {
		Roots []struct {
			Path string `toml:"path"`
		} `toml:"roots"`
	}
	p := filepath.Join(herdrConfigDir, "plugins", "config", "herdr-navigator", "config.toml")
	if _, err := toml.DecodeFile(p, &c); err != nil {
		return nil
	}
	var out []string
	for _, r := range c.Roots {
		d := config.ExpandHome(strings.TrimSpace(r.Path))
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

// Dest is a folder a clone can go into.
type Dest struct {
	Path  string
	Repos int    // repos already in it
	Note  string // why it's offered
}

// CloneDests lists where a clone could go, best first: clone_root, the
// folder of the team's last repo, a folder the URL points at (~/github.com
// for github.com/…, or one named after the org), then the folders holding
// the user's repos (most repos first) and herdr-navigator's roots.
func CloneDests(cloneRoot string, teamRepos, found, navRoots []string, rawURL string) []Dest {
	counts := map[string]int{}
	for _, r := range found {
		counts[filepath.Dir(r)]++
	}
	var out []Dest
	seen := map[string]bool{}
	add := func(p, note string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, Dest{Path: p, Repos: counts[p], Note: note})
	}
	if cloneRoot != "" {
		add(config.ExpandHome(cloneRoot), "clone_root")
	}
	if len(teamRepos) > 0 {
		add(filepath.Dir(teamRepos[0]), "next to the team's repos")
	}
	var dirs []string
	for d := range counts {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool {
		if counts[dirs[i]] != counts[dirs[j]] {
			return counts[dirs[i]] > counts[dirs[j]]
		}
		return dirs[i] < dirs[j]
	})
	// A folder named after the URL's org is the most specific match, then one
	// named after its host.
	host, org := urlHostOrg(rawURL)
	for _, want := range []string{org, host} {
		for _, d := range dirs {
			if want != "" && strings.ToLower(filepath.Base(d)) == want {
				add(d, "matches the URL")
			}
		}
	}
	for _, d := range dirs {
		add(d, "")
	}
	for _, r := range navRoots {
		add(r, "herdr-navigator root")
	}
	return out
}

var reHostOrg = regexp.MustCompile(`^(?:[a-z+]+://)?(?:[^@/]+@)?([^/:]+)[:/]([^/]+)/`)

// urlHostOrg pulls "github.com" and "rshelekhov" out of a clone URL.
func urlHostOrg(raw string) (host, org string) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if reShorthand.MatchString(raw) {
		return "github.com", raw[:strings.Index(raw, "/")]
	}
	if m := reHostOrg.FindStringSubmatch(raw); m != nil {
		return m[1], m[2]
	}
	return "", ""
}
