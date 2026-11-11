package repos

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/config"
)

// scanDepth: ~/code/org/repo is depth 2; one more level for ~/github.com/org/repo.
const scanDepth = 3

var skipDirs = map[string]bool{"node_modules": true, "vendor": true, "target": true, "dist": true, "build": true}

// Discover finds git repositories under roots (a shallow walk), plus extra
// known paths, de-duplicated and sorted by name.
func Discover(roots, extra []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range extra {
		add(p)
	}
	for _, root := range roots {
		walk(config.ExpandHome(root), 0, add)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(filepath.Base(out[i])) < strings.ToLower(filepath.Base(out[j]))
	})
	return out
}

func walk(dir string, depth int, add func(string)) {
	switch gitKind(dir) {
	case kindRepo:
		add(dir) // don't descend into a repo
		return
	case kindWorktree:
		return // a linked worktree: its main repo is the one to offer
	}
	if depth >= scanDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || skipDirs[name] {
			continue
		}
		walk(filepath.Join(dir, name), depth+1, add)
	}
}

type kind int

const (
	kindNone     kind = iota
	kindRepo          // .git is a git directory (it has HEAD)
	kindWorktree      // .git is a file pointing at another repo's git directory
)

// gitKind looks at dir/.git without running git: a stray empty .git folder
// is not a repo (and the walk goes on into dir).
func gitKind(dir string) kind {
	fi, err := os.Stat(filepath.Join(dir, ".git"))
	switch {
	case err != nil:
		return kindNone
	case !fi.IsDir():
		return kindWorktree
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "HEAD")); err == nil {
		return kindRepo
	}
	return kindNone
}

// RepoRoot returns the main repository for dir: inside a linked worktree,
// the repo it belongs to, since worktrees are made from the main repo.
func RepoRoot(dir string) (string, bool) {
	dir = config.ExpandHome(dir)
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", false
	}
	common := strings.TrimSpace(string(out))
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common), true
	}
	// A bare or unusual layout: fall back to the checkout's own top.
	top, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(top)), true
}

// Complete returns directories matching a typed path prefix, for tab.
func Complete(prefix string) []string {
	p := config.ExpandHome(prefix)
	dir, base := filepath.Split(p)
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		hidden := strings.HasPrefix(e.Name(), ".")
		if e.IsDir() && strings.HasPrefix(e.Name(), base) && (!hidden || strings.HasPrefix(base, ".")) {
			out = append(out, filepath.Join(dir, e.Name())+"/")
		}
	}
	slices.Sort(out)
	return out
}

// CommonPrefix of strings.
func CommonPrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	p := ss[0]
	for _, s := range ss[1:] {
		for !strings.HasPrefix(s, p) {
			p = p[:len(p)-1]
		}
	}
	return p
}

// Tilde shortens a path under home to ~/….
func Tilde(p string) string {
	if home, err := os.UserHomeDir(); err == nil && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + p[len(home):]
	}
	return p
}
