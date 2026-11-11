package repos

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
)

func issue(title, desc string) linear.Issue {
	var is linear.Issue
	is.Identifier, is.Title, is.Description = "ABC-1", title, desc
	is.Team.ID, is.Team.Key = "t-abc", "ABC"
	return is
}

func abcStore(t *testing.T) *Store {
	s := Load(t.TempDir())
	for _, r := range []string{"/c/web", "/c/abc_ios", "/c/abc_admin", "/c/abc_billing"} {
		s.Remember("t-abc", r) // abc_billing ends up first (most recent)
	}
	return s
}

func TestDecideGuesses(t *testing.T) {
	s := abcStore(t)
	cases := []struct {
		is        linear.Issue
		h         Hints
		sure      bool
		path, why string
	}{
		{issue("Fix the billing form", ""), Hints{}, false, "/c/abc_billing", `mentions "billing"`},
		{issue("Tabs", "the admin users list breaks"), Hints{}, false, "/c/abc_admin", `mentions "admin"`},
		{issue("iOS sync", ""), Hints{}, false, "/c/abc_ios", `mentions "ios"`},
		{issue("ABC thing", "nothing specific"), Hints{}, false, "/c/abc_billing", "last used"}, // "abc" alone doesn't count
		{issue("Something", ""), Hints{ParentRepo: "/c/web"}, false, "/c/web", "parent's repo"},
		{issue("Fix the billing form", ""), Hints{WorktreeRepo: "/c/web"}, true, "/c/web", "has a worktree there"},
		{issue("x", ""), Hints{ConfigRepos: map[string]string{"ABC": "/c/web"}}, true, "/c/web", "[repos] in config"},
	}
	for _, c := range cases {
		p := s.Decide(c.is, c.h)
		if p.Sure != c.sure || p.Path != c.path || p.Reason != c.why {
			t.Errorf("%q: got sure=%v %s (%s), want sure=%v %s (%s)", c.is.Title, p.Sure, p.Path, p.Reason, c.sure, c.path, c.why)
		}
		if !p.Sure && (len(p.Options) == 0 || p.Options[0].Path != c.path) {
			t.Errorf("%q: the guess isn't first: %+v", c.is.Title, p.Options)
		}
	}
}

func TestDecidePinsAndUnknownTeam(t *testing.T) {
	s := abcStore(t)
	is := issue("Fix the billing form", "")
	is.Project = &struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}{"p-mobile", "abc-mobile"}
	s.ProjectPins["p-mobile"] = "/c/abc_ios"
	if p := s.Decide(is, Hints{}); !p.Sure || p.Path != "/c/abc_ios" {
		t.Errorf("project pin: %+v", p)
	}
	other := issue("x", "")
	other.Team.ID = "t-new"
	if p := s.Decide(other, Hints{}); p.Sure || len(p.Options) != 0 {
		t.Errorf("unknown team should ask with nothing to offer: %+v", p)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := Load(dir)
	s.Remember("t", "/a")
	s.Remember("t", "/b")
	s.Remember("t", "/a") // moves to front, no duplicate
	s.TeamPins["t"] = "/b"
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2 := Load(dir)
	if strings.Join(s2.Teams["t"], ",") != "/a,/b" || s2.TeamPins["t"] != "/b" {
		t.Errorf("reloaded: %+v", s2)
	}
	if strings.Join(s2.Known(), ",") != "/a,/b" {
		t.Errorf("known: %v", s2.Known())
	}
}

func TestDiscoverAndComplete(t *testing.T) {
	root := t.TempDir()
	mk := func(p string, git bool) {
		os.MkdirAll(filepath.Join(root, p), 0o755)
		if git {
			os.MkdirAll(filepath.Join(root, p, ".git"), 0o755)
			os.WriteFile(filepath.Join(root, p, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
		}
	}
	mk("org/abc_billing", true)
	mk("org/abc_billing/node_modules/pkg", true) // inside a repo: not listed
	mk("org/deep/a/b/too_deep", true)            // beyond the depth
	mk("solo", true)
	mk("node_modules/x", true)
	mk("stray", false) // an empty .git folder is not a repo: the walk goes inside
	os.MkdirAll(filepath.Join(root, "stray", ".git"), 0o755)
	mk("stray/inner", true)
	mk("org/abc_billing-abc-956", false) // a linked worktree: .git is a file
	os.WriteFile(filepath.Join(root, "org/abc_billing-abc-956", ".git"), []byte("gitdir: x"), 0o644)
	got := Discover([]string{root}, []string{"/known/repo"})
	want := []string{filepath.Join(root, "org/abc_billing"), filepath.Join(root, "stray/inner"), "/known/repo", filepath.Join(root, "solo")}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("discover:\n got %v\nwant %v", got, want)
	}
	if c := Complete(filepath.Join(root, "or")); len(c) != 1 || c[0] != filepath.Join(root, "org")+"/" {
		t.Errorf("complete: %v", c)
	}
	if CommonPrefix([]string{"/a/abc_b", "/a/abc_h"}) != "/a/abc_" {
		t.Error("common prefix")
	}
}

func TestRepoRoot(t *testing.T) {
	dir := t.TempDir()
	if _, ok := RepoRoot(dir); ok {
		t.Error("a plain folder isn't a repo")
	}
	if err := exec.Command("git", "init", "-q", dir).Run(); err != nil {
		t.Skip("git unavailable")
	}
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	top, ok := RepoRoot(filepath.Join(dir, "sub"))
	real, _ := filepath.EvalSymlinks(dir)
	if !ok || (top != dir && top != real) {
		t.Errorf("root %q ok=%v", top, ok)
	}
}

func TestRepoRootOfWorktree(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "abc_ios")
	git := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	os.MkdirAll(repo, 0o755)
	git("init", "-q")
	git("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(dir, "abc_ios-abc-956")
	git("worktree", "add", "-q", wt, "-b", "abc-956")
	top, ok := RepoRoot(wt)
	real, _ := filepath.EvalSymlinks(repo)
	if !ok || (top != repo && top != real) {
		t.Errorf("worktree → %q, want the main repo %q", top, repo)
	}
	got := Discover([]string{dir}, nil)
	if len(got) != 1 || filepath.Base(got[0]) != "abc_ios" {
		t.Errorf("discover lists worktrees: %v", got)
	}
}

func TestPrune(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "abc_billing")
	os.MkdirAll(filepath.Join(good, ".git"), 0o755)
	os.WriteFile(filepath.Join(good, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	stray := filepath.Join(root, "github.com") // a folder with an empty .git
	os.MkdirAll(filepath.Join(stray, ".git"), 0o755)

	s := Load(t.TempDir())
	s.Remember("t", stray)
	s.Remember("t", good)
	s.Remember("gone", "/nowhere")
	s.TeamPins["t"] = stray
	if !s.Prune() {
		t.Fatal("nothing pruned")
	}
	if got := s.Teams["t"]; len(got) != 1 || got[0] != good {
		t.Errorf("teams: %v", s.Teams)
	}
	if _, ok := s.Teams["gone"]; ok {
		t.Error("an empty team list kept")
	}
	if len(s.TeamPins) != 0 {
		t.Error("a pin to a non-repo kept")
	}
	if s.Prune() {
		t.Error("a second prune changed something")
	}
}
