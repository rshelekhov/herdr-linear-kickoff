package sidebar

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/herdr"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
)

type fakeLinear struct {
	asked  [][]string
	briefs map[string]linear.IssueBrief
}

func (f *fakeLinear) Briefs(_ context.Context, ids []string) ([]linear.IssueBrief, error) {
	f.asked = append(f.asked, ids)
	var out []linear.IssueBrief
	for _, id := range ids {
		out = append(out, f.briefs[id])
	}
	return out, nil
}

func ws(id, checkout string) herdr.Workspace {
	w := herdr.Workspace{ID: id}
	if checkout != "" {
		w.Worktree = &struct {
			CheckoutPath string `json:"checkout_path"`
			IsLinked     bool   `json:"is_linked_worktree"`
		}{checkout, true}
	}
	return w
}

func TestSync(t *testing.T) {
	dir := t.TempDir()
	Update(dir, func(s *Store) {
		s.Record("/wt/abc-964", "u964", "ABC-964", "Optimize", "In Progress")
		s.Record("/wt/abc-734", "u734", "ABC-734", "Tabs", "Todo")
		c := s.Cache["u734"]
		c.At = time.Now().Add(-time.Hour) // stale
		s.Cache["u734"] = c
	})
	// Prune drops links to missing checkouts: these are made-up paths.
	exists := func(string) bool { return true }
	old := pathExists
	pathExists = exists
	defer func() { pathExists = old }()

	lin := &fakeLinear{briefs: map[string]linear.IssueBrief{
		"u734": {ID: "u734", Identifier: "ABC-734", Title: "Tabs", State: linear.State{Name: "In Review"}},
		"u964": {ID: "u964", Identifier: "ABC-964", Title: "Optimize", State: linear.State{Name: "In Progress"}},
	}}
	got := map[string]map[string]string{}
	report := func(w string, tok map[string]string) error { got[w] = tok; return nil }
	wss := []herdr.Workspace{ws("w1", "/wt/abc-964"), ws("w2", "/wt/abc-734"), ws("w3", "/code/other"), ws("w4", "")}

	if err := Sync(context.Background(), dir, wss, lin, false, report); err != nil {
		t.Fatal(err)
	}
	if len(lin.asked) != 1 || len(lin.asked[0]) != 1 || lin.asked[0][0] != "u734" {
		t.Errorf("asked Linear for %v, want only the stale u734", lin.asked)
	}
	if got["w1"]["issue_state"] != "In Progress" || got["w2"]["issue_state"] != "In Review" || got["w2"]["issue"] != "ABC-734" {
		t.Errorf("tokens: %v", got)
	}
	if _, ok := got["w3"]; ok {
		t.Error("an unlinked workspace got tokens")
	}
	if Load(dir).Cache["u734"].State != "In Review" {
		t.Error("the fetched state wasn't saved")
	}

	// Fresh cache: no request at all.
	lin.asked = nil
	Sync(context.Background(), dir, wss, lin, false, report)
	if len(lin.asked) != 0 {
		t.Errorf("asked Linear with a fresh cache: %v", lin.asked)
	}
	// Startup forces a refresh.
	Sync(context.Background(), dir, wss, lin, true, report)
	if len(lin.asked) != 1 || len(lin.asked[0]) != 2 {
		t.Errorf("forced sync asked %v", lin.asked)
	}
}

// A link recorded while a sync waits on Linear survives the sync's save.
func TestSyncKeepsALinkRecordedMeanwhile(t *testing.T) {
	dir := t.TempDir()
	old := pathExists
	pathExists = func(string) bool { return true }
	defer func() { pathExists = old }()
	Update(dir, func(s *Store) { s.Record("/wt/a", "ua", "ABC-1", "A", "Todo") })
	wss := []herdr.Workspace{ws("w1", "/wt/a")}
	fetch := fetcherFunc(func(context.Context, []string) ([]linear.IssueBrief, error) {
		// Start records a new worktree while Linear answers.
		Update(dir, func(s *Store) { s.Record("/wt/b", "ub", "ABC-2", "B", "In Progress") })
		return []linear.IssueBrief{{ID: "ua", Identifier: "ABC-1", Title: "A", State: linear.State{Name: "Done"}}}, nil
	})
	if err := Sync(context.Background(), dir, wss, fetch, true, func(string, map[string]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	s := Load(dir)
	if s.Links["/wt/b"].Identifier != "ABC-2" {
		t.Errorf("the link recorded during the sync was lost: %+v", s.Links)
	}
	if s.Cache["ua"].State != "Done" {
		t.Errorf("fetched state: %+v", s.Cache["ua"])
	}
}

func TestLoadKeepsAnUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "sidebar.json"), []byte("{broken"), 0o600)
	if s := Load(dir); len(s.Links) != 0 {
		t.Error("links from a broken file")
	}
	if _, err := os.Stat(filepath.Join(dir, "sidebar.json.bad")); err != nil {
		t.Error("the broken file wasn't kept aside")
	}
}

type fetcherFunc func(context.Context, []string) ([]linear.IssueBrief, error)

func (f fetcherFunc) Briefs(ctx context.Context, ids []string) ([]linear.IssueBrief, error) {
	return f(ctx, ids)
}

func TestSetStateAndPrune(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "abc-964")
	os.MkdirAll(live, 0o755)
	_, err := Update(dir, func(s *Store) {
		s.Record(live, "u964", "ABC-964", "Optimize", "Todo")
		s.Record(filepath.Join(dir, "gone"), "u1", "ABC-1", "Old", "Done")
		if !s.SetState("u964", "In Review") || s.Cache["u964"].State != "In Review" {
			t.Error("SetState")
		}
		s.Prune()
		if _, ok := s.Links[filepath.Join(dir, "gone")]; ok {
			t.Error("a removed checkout kept")
		}
		if _, ok := s.Cache["u1"]; ok {
			t.Error("the cache of an unlinked issue kept")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if s2 := Load(dir); s2.Links[live].Identifier != "ABC-964" {
		t.Errorf("reloaded: %+v", s2.Links)
	}
}
