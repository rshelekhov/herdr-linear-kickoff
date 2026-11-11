package repos

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCloneSource(t *testing.T) {
	cases := map[string]string{ // input → folder name, "" = refused
		"git@github.com:acme/abc_billing.git": "abc_billing",
		"https://github.com/acme/abc_ios":     "abc_ios",
		"https://gitlab.com/a/b/c.git/":       "c",
		"acme/tools":                          "tools",
		"--upload-pack=touch /tmp/x":          "",
		"ext::sh -c touch% /tmp/pwned":        "",
		"just words":                          "",
		"":                                    "",
	}
	for in, want := range cases {
		src, err := ParseCloneSource(in)
		if (err == nil) != (want != "") || src.Name != want {
			t.Errorf("%q → %q %v, want %q", in, src.Name, err, want)
		}
	}
	if s, _ := ParseCloneSource("org/repo"); !s.Shorthand {
		t.Error("org/repo isn't shorthand")
	}
}

func TestCloneRoot(t *testing.T) {
	if r := CloneRoot("", []string{"/w/abc_billing", "/x/other"}); r != "/w" {
		t.Errorf("next to the team's repo: %q", r)
	}
	if r := CloneRoot("/custom", []string{"/w/a"}); r != "/custom" {
		t.Errorf("clone_root wins: %q", r)
	}
}

// Clones a local repo by URL; a second clone reuses it; a different folder
// in the way is refused.
func TestClone(t *testing.T) {
	dir := t.TempDir()
	upstream := filepath.Join(dir, "up", "abc_billing")
	os.MkdirAll(upstream, 0o755)
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", upstream}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git: %v %s", err, out)
		}
	}
	run("init", "-q")
	run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "x")

	src, err := ParseCloneSource("file://" + upstream)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "work")
	got, err := Clone(context.Background(), src, root, nil)
	if err != nil || got != filepath.Join(root, "abc_billing") || gitKind(got) != kindRepo {
		t.Fatalf("clone: %q %v", got, err)
	}
	if again, err := Clone(context.Background(), src, root, nil); err != nil || again != got {
		t.Errorf("reuse: %q %v", again, err)
	}
	os.MkdirAll(filepath.Join(root, "other"), 0o755)
	other, _ := ParseCloneSource("git@example.com:x/other.git")
	if _, err := Clone(context.Background(), other, root, nil); err == nil {
		t.Error("cloned over an unrelated folder")
	}
}

func TestCloneProgressAndCancel(t *testing.T) {
	dir := t.TempDir()
	upstream := filepath.Join(dir, "up", "big")
	os.MkdirAll(upstream, 0o755)
	if exec.Command("git", "-C", upstream, "init", "-q").Run() != nil {
		t.Skip("git unavailable")
	}
	for i := range 30 {
		os.WriteFile(filepath.Join(upstream, fmt.Sprint(i)), []byte(strings.Repeat("x", 5000+i)), 0o644)
	}
	exec.Command("git", "-C", upstream, "add", ".").Run()
	exec.Command("git", "-C", upstream, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "x").Run()
	src, _ := ParseCloneSource("file://" + upstream)

	var lines []string
	if _, err := Clone(context.Background(), src, filepath.Join(dir, "a"), func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Error("no progress lines")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before it even starts
	root := filepath.Join(dir, "b")
	if _, err := Clone(ctx, src, root, nil); err == nil {
		t.Fatal("a cancelled clone succeeded")
	}
	if _, err := os.Stat(filepath.Join(root, "big")); err == nil {
		t.Error("a cancelled clone left its folder")
	}
}

func TestCloneDests(t *testing.T) {
	found := []string{"/h/github.com/a", "/h/github.com/b", "/h/github.com/c", "/h/work/abc_ios", "/h/work/abc_billing", "/h/personal/fb", "/h/github.com/acme/claude-code"}
	nav := []string{"/h/work", "/h/personal", "/h/projects"}
	got := CloneDests("", nil, found, nav, "https://github.com/someone/tools.git")
	var paths []string
	for _, d := range got {
		paths = append(paths, d.Path)
	}
	want := "/h/github.com /h/work /h/github.com/acme /h/personal /h/projects"
	if strings.Join(paths, " ") != want {
		t.Errorf("order:\n got %s\nwant %s", strings.Join(paths, " "), want)
	}
	if got[0].Note != "matches the URL" || got[0].Repos != 3 {
		t.Errorf("first: %+v", got[0])
	}
	// A team with repos: next to them comes first; clone_root before all.
	got = CloneDests("/h/custom", []string{"/h/work/abc_ios"}, found, nil, "acme/abc_club")
	if got[0].Path != "/h/custom" || got[1].Path != "/h/work" || got[2].Path != "/h/github.com/acme" || got[3].Path != "/h/github.com" {
		t.Errorf("with team and clone_root: %+v", got[:3])
	}
}

func TestNavigatorRoots(t *testing.T) {
	cfg := t.TempDir()
	work := filepath.Join(t.TempDir(), "work")
	os.MkdirAll(work, 0o755)
	p := filepath.Join(cfg, "plugins", "config", "herdr-navigator")
	os.MkdirAll(p, 0o755)
	os.WriteFile(filepath.Join(p, "config.toml"), []byte("[[roots]]\npath = \""+work+"\"\nmax_depth = 1\n\n[[roots]]\npath = \"/no/such/dir\"\n"), 0o644)
	if got := NavigatorRoots(cfg); len(got) != 1 || got[0] != work {
		t.Errorf("roots: %v", got)
	}
	if NavigatorRoots(t.TempDir()) != nil {
		t.Error("no navigator: expected nothing")
	}
}

// Cancelling a gh clone stops git too, at once: gh runs it as a child, which
// would otherwise hold the output open — and the popup on "cancelling" — until
// the whole clone is done.
func TestCloneCancelStopsChildren(t *testing.T) {
	bin := t.TempDir()
	// A gh whose "git" child says it's running, then works for a while.
	os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\nsh -c \"sleep 2 & echo cloning >&2; wait; mkdir -p \\\"$4/late\\\"\" &\nwait\n"), 0o755)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	src, _ := ParseCloneSource("acme/thing")
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	var cancelled time.Time
	progress := func(string) { // the child is running: cancel now
		if cancelled.IsZero() {
			cancelled = time.Now()
			cancel()
		}
	}
	if _, err := Clone(ctx, src, root, progress); err == nil {
		t.Fatal("a cancelled clone succeeded")
	}
	if cancelled.IsZero() {
		t.Fatal("the child never started")
	}
	if d := time.Since(cancelled); d > time.Second {
		t.Errorf("cancel took %v: waited for the child", d)
	}
	time.Sleep(2500*time.Millisecond - time.Since(cancelled))
	if _, err := os.Stat(filepath.Join(root, "thing")); err == nil {
		t.Error("the clone's child kept writing after cancel")
	}
}
