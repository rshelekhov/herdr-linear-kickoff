// Package workspace remembers the Linear workspaces the plugin is connected
// to (their keys are in internal/secret) and which one goes with which repo.
package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// WS is a Linear workspace (an organization).
type WS struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	URLKey string `json:"url_key"` // linear.app/<url_key>/…
}

// Registry is saved as workspaces.json in the state dir. Nothing in it is
// secret.
type Registry struct {
	path  string
	List  []WS              `json:"workspaces"`
	Repos map[string]string `json:"repos"` // repo root → workspace ID, the last used there
	Last  string            `json:"last"`  // the workspace last used anywhere
}

func Load(stateDir string) *Registry {
	r := &Registry{path: filepath.Join(stateDir, "workspaces.json")}
	if b, err := os.ReadFile(r.path); err == nil {
		_ = json.Unmarshal(b, r)
	}
	if r.Repos == nil {
		r.Repos = map[string]string{}
	}
	return r
}

func (r *Registry) Save() error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// Get finds a workspace by ID.
func (r *Registry) Get(id string) (WS, bool) {
	for _, w := range r.List {
		if w.ID == id {
			return w, true
		}
	}
	return WS{}, false
}

// ByURLKey finds the workspace of a linear.app link.
func (r *Registry) ByURLKey(key string) (WS, bool) {
	for _, w := range r.List {
		if key != "" && strings.EqualFold(w.URLKey, key) {
			return w, true
		}
	}
	return WS{}, false
}

// Add adds a workspace, or refreshes its name and URL key; the list stays
// sorted by name.
func (r *Registry) Add(w WS) {
	if i := slices.IndexFunc(r.List, func(x WS) bool { return x.ID == w.ID }); i >= 0 {
		r.List[i] = w
	} else {
		r.List = append(r.List, w)
	}
	slices.SortStableFunc(r.List, func(a, b WS) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
}

// Remove forgets a workspace and the repos that went with it.
func (r *Registry) Remove(id string) {
	r.List = slices.DeleteFunc(r.List, func(w WS) bool { return w.ID == id })
	for repo, ws := range r.Repos {
		if ws == id {
			delete(r.Repos, repo)
		}
	}
	if r.Last == id {
		r.Last = ""
	}
}

// For is the workspace to open with in a repo: the one last used there, else
// the one last used anywhere, else the first. "" when there are none.
func (r *Registry) For(repo string) string {
	for _, id := range []string{r.Repos[repo], r.Last} {
		if _, ok := r.Get(id); ok && id != "" {
			return id
		}
	}
	if len(r.List) > 0 {
		return r.List[0].ID
	}
	return ""
}

// Use records that a workspace was used, in a repo if known.
func (r *Registry) Use(repo, id string) {
	r.Last = id
	if repo != "" {
		r.Repos[repo] = id
	}
}
