// Package sidebar keeps herdr's workspace tokens $issue, $issue_state and
// $issue_title in step with Linear, for workspaces opened on an issue.
//
// herdr keeps tokens in memory only, so the plugin remembers which checkout
// belongs to which issue (state/sidebar.json) and reports them again at
// startup; Linear is asked at most every RefreshEvery, on workspace focus.
package sidebar

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/herdr"
	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
)

// RefreshEvery is how stale the cached issue states may get.
const RefreshEvery = 5 * time.Minute

// Link ties a worktree checkout to the issue it was started for.
type Link struct {
	IssueID    string `json:"issue_id"`
	Identifier string `json:"identifier"`
}

// Cached is the last known state of an issue.
type Cached struct {
	Identifier string    `json:"identifier"`
	Title      string    `json:"title"`
	State      string    `json:"state"`
	At         time.Time `json:"at"`
}

// Store is state/sidebar.json.
type Store struct {
	path  string
	Links map[string]Link   `json:"links"`  // checkout path → issue
	Cache map[string]Cached `json:"issues"` // issue ID → last known state
}

// Load reads the store without locking it: for reading only. Changes go
// through Update, so two processes (the popup's Start and a focus-triggered
// sync) can't overwrite each other's.
func Load(stateDir string) *Store {
	s := &Store{path: filepath.Join(stateDir, "sidebar.json")}
	if b, err := os.ReadFile(s.path); err == nil {
		if json.Unmarshal(b, s) != nil {
			// Unreadable: start over, but keep it for a look rather than
			// silently writing an empty store over it.
			*s = Store{path: s.path}
			_ = os.Rename(s.path, s.path+".bad")
		}
	}
	if s.Links == nil {
		s.Links = map[string]Link{}
	}
	if s.Cache == nil {
		s.Cache = map[string]Cached{}
	}
	linear.Sanitize(s)
	return s
}

// Update loads the store under an exclusive lock, applies fn and saves it,
// so concurrent writers each see the others' changes.
func Update(stateDir string, fn func(*Store)) (*Store, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	lf, err := os.OpenFile(filepath.Join(stateDir, "sidebar.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	s := Load(stateDir)
	fn(s)
	return s, s.save()
}

// save writes the store atomically: a crash mid-write leaves the old file.
func (s *Store) save() error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".sidebar-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// Record links a checkout to an issue, with its current state.
func (s *Store) Record(checkout, issueID, identifier, title, state string) {
	s.Links[checkout] = Link{issueID, identifier}
	s.Cache[issueID] = Cached{identifier, title, state, time.Now()}
}

// SetState updates an issue's cached state (after a change in the popup).
func (s *Store) SetState(issueID, state string) bool {
	c, ok := s.Cache[issueID]
	if ok {
		c.State, c.At = state, time.Now()
		s.Cache[issueID] = c
	}
	return ok
}

// Prune forgets checkouts that are gone.
func (s *Store) Prune() {
	for p, l := range s.Links {
		if !pathExists(p) {
			delete(s.Links, p)
			if !s.linked(l.IssueID) {
				delete(s.Cache, l.IssueID)
			}
		}
	}
}

var pathExists = func(p string) bool { _, err := os.Stat(p); return err == nil }

func (s *Store) linked(issueID string) bool {
	for _, l := range s.Links {
		if l.IssueID == issueID {
			return true
		}
	}
	return false
}

// Fetcher loads issue states; *linear.Client in production.
type Fetcher interface {
	Briefs(ctx context.Context, ids []string) ([]linear.IssueBrief, error)
}

// Reporter sets a workspace's tokens; herdr.ReportTokens in production.
type Reporter func(workspace string, tokens map[string]string) error

// Sync reports the tokens of every open workspace that is on a linked
// checkout. Linear is asked only for issues whose cache is older than
// RefreshEvery (or all of them with force); fetch may be nil to use the cache.
// The request runs outside the lock; its results are merged into whatever
// the store holds by then.
func Sync(ctx context.Context, stateDir string, wss []herdr.Workspace, fetch Fetcher, force bool, report Reporter) error {
	stale := Load(stateDir).stale(wss, force)
	var briefs []linear.IssueBrief
	var fetchErr error
	if fetch != nil && len(stale) > 0 {
		briefs, fetchErr = fetch.Briefs(ctx, stale)
		linear.Sanitize(&briefs)
	}
	s, err := Update(stateDir, func(s *Store) {
		s.Prune()
		for _, b := range briefs {
			if s.linked(b.ID) {
				s.Cache[b.ID] = Cached{b.Identifier, b.Title, b.State.Name, time.Now()}
			}
		}
	})
	if err != nil {
		return err
	}
	s.report(wss, report)
	return fetchErr
}

// stale lists the linked issues of open workspaces that Linear should be
// asked about.
func (s *Store) stale(wss []herdr.Workspace, force bool) []string {
	var out []string
	for _, ws := range wss {
		l, ok := s.Links[ws.CheckoutPath()]
		if !ok || slices.Contains(out, l.IssueID) {
			continue
		}
		if c, ok := s.Cache[l.IssueID]; force || !ok || time.Since(c.At) > RefreshEvery {
			out = append(out, l.IssueID)
		}
	}
	return out
}

func (s *Store) report(wss []herdr.Workspace, report Reporter) {
	for _, ws := range wss {
		l, ok := s.Links[ws.CheckoutPath()]
		if !ok {
			continue
		}
		if c, ok := s.Cache[l.IssueID]; ok {
			_ = report(ws.ID, map[string]string{"issue": c.Identifier, "issue_state": c.State, "issue_title": c.Title})
		}
	}
}
