package demo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/kickoff"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/workspace"
)

func TestClientTabs(t *testing.T) {
	c := NewClient(acme.id)
	c.delay = 0
	ctx := context.Background()
	for _, md := range linear.Modes {
		list, err := c.Issues(ctx, md, linear.Scope{})
		if err != nil || len(list) == 0 {
			t.Fatalf("%s: %d issues, %v", md, len(list), err)
		}
		for _, is := range list {
			if md != linear.ModeRecent && is.TabFor(viewer.ID) != md {
				t.Errorf("%s in %s", is.Identifier, md)
			}
		}
	}
	web, _ := c.Issues(ctx, linear.ModeTeam, linear.Scope{Kind: "team", ID: teamWeb.id})
	for _, is := range web {
		if is.Team.Key != "WEB" {
			t.Errorf("%s in the WEB scope", is.Identifier)
		}
	}
}

func TestClientIssueAndSearch(t *testing.T) {
	c := NewClient(acme.id)
	c.delay = 0
	ctx := context.Background()
	is, err := c.Issue(ctx, "web-214")
	if err != nil || len(is.SubIssues()) != 3 || len(is.SortedComments()) != 2 {
		t.Fatalf("WEB-214: %d sub-issues, %d comments, %v", len(is.SubIssues()), len(is.SortedComments()), err)
	}
	found, _ := c.Search(ctx, "checkout safari", 20)
	if len(found) != 1 || found[0].Identifier != "WEB-162" {
		t.Errorf("search: %+v", found)
	}
	if err := c.UpdateIssue(ctx, is.ID, linear.IssueUpdate{StateID: "review"}); err != nil {
		t.Fatal(err)
	}
	if is, _ = c.Issue(ctx, "WEB-214"); is.State.Name != "In Review" {
		t.Errorf("state after update: %s", is.State.Name)
	}
}

// Setup keeps everything in its temp dir: HOME, the state, the repos.
func TestSetup(t *testing.T) {
	for _, k := range []string{"HOME", "PATH", "HERDR_BIN_PATH"} {
		t.Setenv(k, os.Getenv(k))
	}
	d, cleanup, err := Setup()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	home := os.Getenv("HOME")
	if !strings.HasPrefix(d.StateDir, filepath.Dir(home)) {
		t.Errorf("state %s outside the demo dir %s", d.StateDir, home)
	}
	if _, err := os.Stat(filepath.Join(home, "code", "acme", "web", ".git")); err != nil {
		t.Errorf("web repo: %v", err)
	}
	if got := d.Worktrees(filepath.Join(home, "code", "acme", "web")); got["WEB-214"] == "" {
		t.Errorf("worktrees: %v", got)
	}
	if !kickoff.OrchestratorInstalled() {
		t.Error("magus not set up in the demo home")
	}
}

// Two workspaces: each its own issues, and Setup connects both.
func TestTwoWorkspaces(t *testing.T) {
	ctx := context.Background()
	nw := NewClient(northwind.id)
	nw.delay = 0
	v, _ := nw.Viewer(ctx)
	if v.Organization.Name != "Northwind" || v.Organization.URLKey != "northwind" {
		t.Errorf("viewer org %+v", v.Organization)
	}
	mine, _ := nw.Issues(ctx, linear.ModeMine, linear.Scope{})
	for _, is := range mine {
		if is.Team.Key != "OPS" || !strings.Contains(is.URL, "/northwind/") {
			t.Errorf("%s (%s) in Northwind", is.Identifier, is.URL)
		}
	}
	if _, err := nw.Issue(ctx, "WEB-214"); err == nil {
		t.Error("Acme's issue found in Northwind")
	}

	for _, k := range []string{"HOME", "PATH", "HERDR_BIN_PATH"} {
		t.Setenv(k, os.Getenv(k))
	}
	d, cleanup, err := Setup()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	reg := workspace.Load(d.StateDir)
	if len(reg.List) != 2 || reg.Last != acme.id {
		t.Errorf("registry %+v", reg)
	}
	if d.Connect(northwind.id) != d.Connect(northwind.id) {
		t.Error("a new client per call: status changes would be lost")
	}
}
