// Package repos decides which local repository an issue's worktree goes in.
//
// A Linear team may span several repos, and the repos an issue touches often
// only become clear while working on it, so there is no default repo per
// team: the plugin is sure only when the issue already has a worktree there or
// a repo is pinned; otherwise the user picks, with a guess preselected.
package repos

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
)

// Store is what the plugin remembers about repos (state/repos.json).
type Store struct {
	path string
	// Teams: team ID → repos ever picked for its issues, most recent first.
	Teams map[string][]string `json:"teams"`
	// TeamPins / ProjectPins: "always use this repo" set in the chooser.
	TeamPins    map[string]string `json:"team_pins"`
	ProjectPins map[string]string `json:"project_pins"`
	// Names: team/project ID → its name, for the settings screen.
	Names map[string]string `json:"names"`
}

func Load(stateDir string) *Store {
	s := &Store{path: filepath.Join(stateDir, "repos.json")}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, s)
	}
	if s.Teams == nil {
		s.Teams = map[string][]string{}
	}
	if s.TeamPins == nil {
		s.TeamPins = map[string]string{}
	}
	if s.ProjectPins == nil {
		s.ProjectPins = map[string]string{}
	}
	if s.Names == nil {
		s.Names = map[string]string{}
	}
	return s
}

func (s *Store) Save() error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o600)
}

// Prune forgets paths that are no longer git repositories (moved, deleted,
// or never were: a folder with a stray .git). It reports whether it changed
// anything.
func (s *Store) Prune() bool {
	changed := false
	ok := func(p string) bool { return gitKind(p) == kindRepo }
	for team, list := range s.Teams {
		kept := slices.DeleteFunc(slices.Clone(list), func(p string) bool { return !ok(p) })
		if len(kept) != len(list) {
			changed = true
			if len(kept) == 0 {
				delete(s.Teams, team)
			} else {
				s.Teams[team] = kept
			}
		}
	}
	for _, pins := range []map[string]string{s.TeamPins, s.ProjectPins} {
		for id, p := range pins {
			if !ok(p) {
				delete(pins, id)
				changed = true
			}
		}
	}
	return changed
}

// Forget drops one repo from a team's list.
func (s *Store) Forget(teamID, repo string) {
	list := slices.DeleteFunc(slices.Clone(s.Teams[teamID]), func(r string) bool { return r == repo })
	if len(list) == 0 {
		delete(s.Teams, teamID)
	} else {
		s.Teams[teamID] = list
	}
}

// Remember puts repo first in the team's list.
func (s *Store) Remember(teamID, repo string) {
	list := slices.DeleteFunc(slices.Clone(s.Teams[teamID]), func(r string) bool { return r == repo })
	s.Teams[teamID] = append([]string{repo}, list...)
}

// Known is every repo the plugin knows of, for worktree lookups and search.
func (s *Store) Known() []string {
	var out []string
	add := func(r string) {
		if r != "" && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	for _, list := range s.Teams {
		for _, r := range list {
			add(r)
		}
	}
	for _, r := range s.TeamPins {
		add(r)
	}
	for _, r := range s.ProjectPins {
		add(r)
	}
	slices.Sort(out)
	return out
}

// Option is one repo offered for an issue.
type Option struct {
	Path   string
	Reason string // why it's preselected, "" otherwise
}

func (o Option) Name() string { return filepath.Base(o.Path) }

// Plan is the decision for one issue: Sure means go without asking.
type Plan struct {
	Sure    bool
	Path    string // the sure repo, or the guess
	Reason  string
	Options []Option // for the chooser, the guess first
}

// Hints are facts about the issue the UI already has.
type Hints struct {
	WorktreeRepo string            // the repo the issue has a worktree in
	ParentRepo   string            // the repo the parent issue has a worktree in
	ConfigRepos  map[string]string // [repos] from config: team key → path
}

// Decide plans where an issue starts.
func (s *Store) Decide(is linear.Issue, h Hints) Plan {
	switch {
	case h.WorktreeRepo != "":
		return Plan{Sure: true, Path: h.WorktreeRepo, Reason: "has a worktree there"}
	case is.Project != nil && s.ProjectPins[is.Project.ID] != "":
		return Plan{Sure: true, Path: s.ProjectPins[is.Project.ID], Reason: "pinned for " + is.Project.Name}
	case s.TeamPins[is.Team.ID] != "":
		return Plan{Sure: true, Path: s.TeamPins[is.Team.ID], Reason: "pinned for " + is.Team.Key}
	case h.ConfigRepos[is.Team.Key] != "":
		return Plan{Sure: true, Path: h.ConfigRepos[is.Team.Key], Reason: "[repos] in config"}
	}

	var opts []Option
	for _, r := range s.Teams[is.Team.ID] {
		opts = append(opts, Option{Path: r})
	}
	if h.ParentRepo != "" && !hasPath(opts, h.ParentRepo) {
		opts = append(opts, Option{Path: h.ParentRepo})
	}
	if len(opts) == 0 {
		return Plan{}
	}

	pick, reason := -1, ""
	text := strings.ToLower(is.Title + "\n" + is.Description + "\n" + strings.Join(is.LabelNames(), " "))
	for i, o := range opts {
		if w := mentioned(o.Name(), is.Team.Key, text); w != "" {
			pick, reason = i, `mentions "`+w+`"`
			break
		}
	}
	if pick < 0 && h.ParentRepo != "" {
		pick, reason = indexOf(opts, h.ParentRepo), "parent's repo"
	}
	if pick < 0 {
		pick, reason = 0, "last used"
	}
	opts[pick].Reason = reason
	guess := opts[pick]
	opts = append(append([]Option{guess}, opts[:pick]...), opts[pick+1:]...)
	return Plan{Path: guess.Path, Reason: reason, Options: opts}
}

func hasPath(opts []Option, p string) bool { return indexOf(opts, p) >= 0 }

func indexOf(opts []Option, p string) int {
	for i, o := range opts {
		if o.Path == p {
			return i
		}
	}
	return -1
}

var reWordSplit = regexp.MustCompile(`[^a-z0-9]+`)

// mentioned returns the distinctive part of a repo's name ("billing" in
// "abc_billing") if the issue's text has it as a word; the team key and short
// or generic parts don't count.
func mentioned(repo, teamKey, text string) string {
	generic := map[string]bool{"app": true, "web": true, "api": true, "service": true, "the": true, "core": true}
	for _, part := range reWordSplit.Split(strings.ToLower(repo), -1) {
		if len(part) < 3 || part == strings.ToLower(teamKey) || generic[part] {
			continue
		}
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(part) + `\b`).MatchString(text) {
			return part
		}
	}
	return ""
}
