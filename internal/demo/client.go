package demo

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
)

// Client answers like Linear, from the demo workspace in memory. Status
// changes stick until the popup closes.
type Client struct {
	w      world
	mu     sync.Mutex
	issues []linear.Issue
	delay  time.Duration // how long a request "takes": a spinner, not a flash
}

var _ linear.API = (*Client)(nil)

// NewClient answers for the demo workspace with this ID (Acme if unknown).
func NewClient(id string) *Client {
	w := acme
	for _, x := range worlds {
		if x.id == id {
			w = x
		}
	}
	return &Client{w: w, issues: build(w, time.Now()), delay: 150 * time.Millisecond}
}

func (c *Client) wait(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) Viewer(ctx context.Context) (linear.Viewer, error) {
	v := viewer
	v.Organization.ID, v.Organization.Name, v.Organization.URLKey = c.w.id, c.w.name, c.w.urlKey
	return v, c.wait(ctx, c.delay)
}

// listed strips what a list query doesn't fetch, as the real one does.
func listed(is linear.Issue) linear.Issue {
	is.Comments, is.Children, is.Attachments = nil, nil, nil
	return is
}

func (c *Client) Issues(ctx context.Context, mode linear.Mode, scope linear.Scope) ([]linear.Issue, error) {
	if err := c.wait(ctx, c.delay); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []linear.Issue
	for _, is := range c.issues {
		in := is.TabFor(viewer.ID) == mode
		if mode == linear.ModeRecent { // what I have, in any state, and what closed lately
			mine := is.Assignee != nil && is.Assignee.ID == viewer.ID
			closed := is.State.Type == "completed" || is.State.Type == "canceled"
			in = (mine || closed) && time.Since(is.UpdatedAt) < 30*24*time.Hour
		}
		if in && is.InScope(scope) {
			out = append(out, listed(is))
		}
	}
	slices.SortStableFunc(out, func(a, b linear.Issue) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return out, nil
}

func (c *Client) Scopes(ctx context.Context) ([]linear.Scope, error) {
	var out []linear.Scope
	for _, t := range c.w.teams {
		out = append(out, linear.Scope{Kind: "team", ID: t.id, Name: t.key + " · " + t.name})
	}
	for _, p := range c.w.projects {
		out = append(out, linear.Scope{Kind: "project", ID: p.id, Name: p.name})
	}
	return out, c.wait(ctx, c.delay)
}

func (c *Client) find(id string) (int, bool) {
	for i, is := range c.issues {
		if strings.EqualFold(is.Identifier, id) || is.ID == id {
			return i, true
		}
	}
	return 0, false
}

func (c *Client) Issue(ctx context.Context, id string) (linear.Issue, error) {
	if err := c.wait(ctx, c.delay); err != nil {
		return linear.Issue{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	i, ok := c.find(id)
	if !ok {
		return linear.Issue{}, fmt.Errorf("Linear: Entity not found: %s", id)
	}
	return c.issues[i], nil
}

func brief(is linear.Issue) linear.IssueBrief {
	return linear.IssueBrief{ID: is.ID, Identifier: is.Identifier, Title: is.Title, State: is.State}
}

func (c *Client) Briefs(ctx context.Context, ids []string) ([]linear.IssueBrief, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []linear.IssueBrief
	for _, id := range ids {
		if i, ok := c.find(id); ok {
			out = append(out, brief(c.issues[i]))
		}
	}
	return out, nil
}

// Search matches every word in the title or the description, closed issues
// too, like Linear's search.
func (c *Client) Search(ctx context.Context, term string, first int) ([]linear.IssueBrief, error) {
	if err := c.wait(ctx, 4*c.delay); err != nil {
		return nil, err
	}
	words := strings.Fields(strings.ToLower(term))
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []linear.IssueBrief
	for _, is := range c.issues {
		text := strings.ToLower(is.Identifier + " " + is.Title + " " + is.Description)
		if len(words) > 0 && !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(text, w) }) {
			out = append(out, brief(is))
		}
		if len(out) == first {
			break
		}
	}
	return out, nil
}

func (c *Client) TeamStates(ctx context.Context, teamID string) ([]linear.State, error) {
	return slices.Clone(states), c.wait(ctx, c.delay)
}

func (c *Client) UpdateIssue(ctx context.Context, id string, in linear.IssueUpdate) error {
	if err := c.wait(ctx, c.delay); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	i, ok := c.find(id)
	if !ok {
		return fmt.Errorf("Linear: Entity not found: %s", id)
	}
	if in.StateID != "" {
		c.issues[i].State = stateByID(in.StateID)
	}
	if in.AssigneeID != "" {
		c.issues[i].Assignee = &struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}{in.AssigneeID, people[in.AssigneeID]}
	}
	c.issues[i].UpdatedAt = time.Now()
	return nil
}

func (c *Client) NewIssueOptions(ctx context.Context) (linear.NewIssueOptions, error) {
	out := linear.NewIssueOptions{Me: viewer.ID}
	var teamIDs []string
	for _, t := range c.w.teams {
		out.Teams = append(out.Teams, linear.TeamOptions{ID: t.id, Key: t.key, Name: t.name})
		teamIDs = append(teamIDs, t.id)
	}
	for _, p := range c.w.projects {
		out.Projects = append(out.Projects, linear.ProjectOption{ID: p.id, Name: p.name, Teams: teamIDs})
	}
	return out, c.wait(ctx, c.delay)
}

func (c *Client) TeamMembers(ctx context.Context, teamID string) ([]linear.Person, error) {
	var out []linear.Person
	for _, id := range []string{"u-alex", "u-jonas", "u-priya", "u-sam"} {
		out = append(out, linear.Person{ID: id, Name: people[id]})
	}
	return out, c.wait(ctx, c.delay)
}

// CreateIssue adds the issue to the workspace, numbered after the team's last.
func (c *Client) CreateIssue(ctx context.Context, in linear.IssueCreate) (linear.Issue, error) {
	if err := c.wait(ctx, 2*c.delay); err != nil {
		return linear.Issue{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var t team
	for _, x := range c.w.teams {
		if x.id == in.TeamID {
			t = x
		}
	}
	if t.id == "" {
		return linear.Issue{}, fmt.Errorf("Linear: team not found")
	}
	n := 0
	for _, is := range c.issues {
		if is.Team.ID == t.id {
			n = max(n, is.Number)
		}
	}
	id := fmt.Sprintf("%s-%d", t.key, n+1)
	is := linear.Issue{
		ID: "id-" + strings.ToLower(id), Identifier: id, Number: n + 1, Title: in.Title,
		URL:         "https://linear.app/" + c.w.urlKey + "/issue/" + strings.ToLower(id),
		BranchName:  strings.ToLower(id),
		Description: in.Description, Priority: in.Priority, PriorityLabel: prioLabel(in.Priority),
		UpdatedAt: time.Now(), State: stateByID("todo"),
	}
	if in.StateID != "" {
		is.State = stateByID(in.StateID)
	}
	is.Team.ID, is.Team.Key, is.Team.Name = t.id, t.key, t.name
	if in.AssigneeID != "" {
		is.Assignee = &struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}{in.AssigneeID, people[in.AssigneeID]}
	}
	for _, p := range c.w.projects {
		if p.id == in.ProjectID {
			is.Project = &struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}{p.id, p.name}
		}
	}
	c.issues = append(c.issues, is)
	return listed(is), nil
}
