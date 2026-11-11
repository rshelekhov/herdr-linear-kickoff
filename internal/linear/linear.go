// Package linear is a small client for Linear's GraphQL API.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const endpoint = "https://api.linear.app/graphql"

// ErrUnauthorized means the API key was rejected.
var ErrUnauthorized = errors.New("Linear rejected the API key")

// API is what the plugin asks of Linear: Client is the real one, the demo
// mode has its own.
type API interface {
	Viewer(ctx context.Context) (Viewer, error)
	Issues(ctx context.Context, mode Mode, scope Scope) ([]Issue, error)
	Scopes(ctx context.Context) ([]Scope, error)
	Issue(ctx context.Context, id string) (Issue, error)
	Briefs(ctx context.Context, ids []string) ([]IssueBrief, error)
	Search(ctx context.Context, term string, first int) ([]IssueBrief, error)
	TeamStates(ctx context.Context, teamID string) ([]State, error)
	UpdateIssue(ctx context.Context, id string, in IssueUpdate) error
	NewIssueOptions(ctx context.Context) (NewIssueOptions, error)
	TeamMembers(ctx context.Context, teamID string) ([]Person, error)
	CreateIssue(ctx context.Context, in IssueCreate) (Issue, error)
}

var _ API = (*Client)(nil)

type Client struct {
	key  string
	http *http.Client
}

func New(key string) *Client {
	return &Client{key: key, http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) post(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.key)
	return c.http.Do(req)
}

func (c *Client) do(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	resp, err := c.post(ctx, body)
	// A read that hit a dropped connection (a stale keep-alive, a network
	// switch) is asked once more; a mutation isn't: it may have gone through.
	// Not after a timeout: that would only double the wait.
	if err != nil && ctx.Err() == nil && !os.IsTimeout(err) && !strings.HasPrefix(strings.TrimSpace(query), "mutation") {
		select {
		case <-time.After(300 * time.Millisecond):
			resp, err = c.post(ctx, body)
		case <-ctx.Done():
		}
	}
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) { // not the "Post https://…" part
			err = ue.Err
		}
		return fmt.Errorf("Linear is unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	var r struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(raw, &r)
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if len(r.Errors) > 0 {
		e := r.Errors[0]
		if e.Extensions.Code == "AUTHENTICATION_ERROR" {
			return ErrUnauthorized
		}
		return fmt.Errorf("Linear: %s", Clean(e.Message))
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Linear: HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(r.Data, out); err != nil {
		return err
	}
	Sanitize(out) // text from Linear is printed to a terminal
	return nil
}

type Viewer struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	Organization struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		URLKey string `json:"urlKey"` // linear.app/<urlKey>/…
	} `json:"organization"`
}

func (c *Client) Viewer(ctx context.Context) (Viewer, error) {
	var r struct {
		Viewer Viewer `json:"viewer"`
	}
	err := c.do(ctx, `query { viewer { id name displayName organization { id name urlKey } } }`, nil, &r)
	return r.Viewer, err
}

type State struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"` // triage, backlog, unstarted, started, completed, canceled
	Color    string  `json:"color"`
	Position float64 `json:"position"`
}

type Comment struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	User      *struct {
		Name string `json:"name"`
	} `json:"user"`
}

// IssueRef is a related issue (parent or sub-issue) as shown alongside another.
type IssueRef struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	State       *State `json:"state,omitempty"`
	Assignee    *struct {
		Name string `json:"name"`
	} `json:"assignee,omitempty"`
}

type Issue struct {
	ID            string    `json:"id"`
	Identifier    string    `json:"identifier"`
	Number        int       `json:"number"`
	Title         string    `json:"title"`
	URL           string    `json:"url"`
	BranchName    string    `json:"branchName"`
	Description   string    `json:"description"`
	Priority      int       `json:"priority"` // 0 none, 1 urgent … 4 low
	PriorityLabel string    `json:"priorityLabel"`
	UpdatedAt     time.Time `json:"updatedAt"`
	State         State     `json:"state"`
	Team          struct {
		ID   string `json:"id"`
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"team"`
	Assignee *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"assignee"`
	Project *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Comments *struct {
		Nodes []Comment `json:"nodes"`
	} `json:"comments,omitempty"`
	DueDate  string   `json:"dueDate,omitempty"` // "2026-03-12"
	Estimate *float64 `json:"estimate,omitempty"`
	Cycle    *struct {
		Number int    `json:"number"`
		Name   string `json:"name"`
	} `json:"cycle,omitempty"`
	// Attachments: links Linear keeps with the issue, pull requests among
	// them (loaded with the full issue only).
	Attachments *struct {
		Nodes []Attachment `json:"nodes"`
	} `json:"attachments,omitempty"`
	Parent   *IssueRef `json:"parent"`
	Children *struct {
		Nodes []IssueRef `json:"nodes"`
	} `json:"children,omitempty"`
	// ChildProbe is the list query's "any sub-issues?" check.
	ChildProbe *struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	} `json:"childProbe,omitempty"`
	// CommentProbe is the list query's "any comments?" check (first: 1, ids only).
	CommentProbe *struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	} `json:"commentProbe,omitempty"`
}

// InScope reports whether the issue belongs to a scope.
func (i Issue) InScope(s Scope) bool {
	switch s.Kind {
	case "team":
		return i.Team.ID == s.ID
	case "project":
		return i.Project != nil && i.Project.ID == s.ID
	}
	return s.All()
}

// TeamScope is the scope of the issue's team, named as Scopes names it.
func (i Issue) TeamScope() Scope {
	name := i.Team.Key
	if i.Team.Name != "" {
		name += " · " + i.Team.Name
	}
	return Scope{Kind: "team", ID: i.Team.ID, Name: name}
}

// TabFor is the list the issue shows up in for this viewer, or "" when it's
// in none (done or canceled).
func (i Issue) TabFor(viewerID string) Mode {
	switch i.State.Type {
	case "completed", "canceled":
		return ""
	case "backlog":
		if i.Assignee != nil && i.Assignee.ID == viewerID {
			return ModeMine
		}
		return ModeBacklog
	}
	if i.Assignee != nil && i.Assignee.ID == viewerID {
		return ModeMine
	}
	return ModeTeam
}

// HasChildren reports whether the issue has sub-issues; known as in HasComments.
func (i Issue) HasChildren() (has, known bool) {
	if i.Children != nil {
		return len(i.Children.Nodes) > 0, true
	}
	if i.ChildProbe == nil {
		return false, false
	}
	return len(i.ChildProbe.Nodes) > 0, true
}

// SubIssues returns the loaded sub-issues (nil until the full issue is fetched).
func (i Issue) SubIssues() []IssueRef {
	if i.Children == nil {
		return nil
	}
	return i.Children.Nodes
}

// HasComments reports whether the issue has comments; known is false when the
// issue came without the probe (e.g. an older cache).
func (i Issue) HasComments() (has, known bool) {
	if i.CommentProbe == nil {
		return false, false
	}
	return len(i.CommentProbe.Nodes) > 0, true
}

// LabelNames returns the issue's label names.
func (i Issue) LabelNames() []string {
	out := make([]string, 0, len(i.Labels.Nodes))
	for _, l := range i.Labels.Nodes {
		out = append(out, l.Name)
	}
	return out
}

// SortedComments returns comments oldest first.
func (i Issue) SortedComments() []Comment {
	if i.Comments == nil {
		return nil
	}
	cs := append([]Comment(nil), i.Comments.Nodes...)
	sort.SliceStable(cs, func(a, b int) bool { return cs[a].CreatedAt.Before(cs[b].CreatedAt) })
	return cs
}

const issueFields = `id identifier number title url branchName description priority priorityLabel updatedAt
dueDate estimate cycle { number name }
state { id name type color position } team { id key name } assignee { id name } project { id name } labels { nodes { name } }
parent { id identifier title }`

// Mode is an issue list.
type Mode string

const (
	ModeMine    Mode = "mine"    // assigned to me, open
	ModeTeam    Mode = "team"    // "All": my teams, triage / todo / in progress, anyone's
	ModeBacklog Mode = "backlog" // my teams' backlog
	ModeRecent  Mode = "recent"  // what I created, have or follow, updated lately, any state
)

// Modes in display order.
var Modes = []Mode{ModeMine, ModeTeam, ModeBacklog, ModeRecent}

// ParseMode returns the mode named s, or ModeMine.
func ParseMode(s string) Mode {
	for _, m := range Modes {
		if v := strings.ToLower(strings.TrimSpace(s)); string(m) == v || (v == "all" && m == ModeTeam) {
			return m
		}
	}
	return ModeMine
}

const listFields = issueFields + `
	  commentProbe: comments(first: 1) { nodes { id } }
	  childProbe: children(first: 1) { nodes { id } }`

// myTeams matches issues of teams the viewer is a member of.
const myTeams = `team: { members: { some: { isMe: { eq: true } } } }`

// Scope narrows a list to one project or one team; the zero value is "all".
type Scope struct {
	Kind string `json:"kind"` // "project", "team" or ""
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s Scope) All() bool { return s.ID == "" }

// Key names the scope in cache files.
func (s Scope) Key() string {
	if s.All() {
		return ""
	}
	return s.Kind + "-" + s.ID
}

// scopeFilter is the filter fragment and its variable, if the scope is set.
// Unset scopes add nothing: a null comparator could mean "field is empty".
func (s Scope) filter(teams bool) (frag string, vars map[string]any) {
	team := ""
	if teams {
		team = `members: { some: { isMe: { eq: true } } }`
	}
	switch {
	case s.Kind == "team" && s.ID != "":
		team = strings.TrimSpace(team + ` id: { eq: $scope }`)
		vars = map[string]any{"scope": s.ID}
	case s.Kind == "project" && s.ID != "":
		frag = `project: { id: { eq: $scope } }, `
		vars = map[string]any{"scope": s.ID}
	}
	if team != "" {
		frag += `team: { ` + team + ` }, `
	}
	return frag, vars
}

// Issues returns the list for a mode and scope, up to 150 most recently updated.
func (c *Client) Issues(ctx context.Context, mode Mode, scope Scope) ([]Issue, error) {
	frag, vars := scope.filter(mode != ModeMine)
	decl := ""
	if vars != nil {
		decl = `($scope: ID!)`
	}
	if mode == ModeMine {
		q := `query` + decl + ` { viewer { assignedIssues(first: 150, orderBy: updatedAt,
	  filter: { ` + frag + `state: { type: { nin: ["completed", "canceled"] } } }) { nodes { ` + listFields + ` } } } }`
		var r struct {
			Viewer struct {
				AssignedIssues struct {
					Nodes []Issue `json:"nodes"`
				} `json:"assignedIssues"`
			} `json:"viewer"`
		}
		err := c.do(ctx, q, vars, &r)
		return r.Viewer.AssignedIssues.Nodes, err
	}
	if mode == ModeRecent {
		// Linear has no "touched by me": created, assigned or subscribed to
		// (commenting subscribes) is close, in any state, last 30 days.
		q := `query` + decl + ` { issues(first: 50, orderBy: updatedAt,
	  filter: { ` + frag + `updatedAt: { gt: "-P30D" }, or: [{ creator: { isMe: { eq: true } } },
	    { assignee: { isMe: { eq: true } } }, { subscribers: { some: { isMe: { eq: true } } } }] }) { nodes { ` + listFields + ` } } }`
		var r struct {
			Issues struct {
				Nodes []Issue `json:"nodes"`
			} `json:"issues"`
		}
		err := c.do(ctx, q, vars, &r)
		return r.Issues.Nodes, err
	}
	states := `["triage", "unstarted", "started"]`
	if mode == ModeBacklog {
		states = `["backlog"]`
	}
	q := `query` + decl + ` { issues(first: 150, orderBy: updatedAt,
	  filter: { ` + frag + `state: { type: { in: ` + states + ` } } }) { nodes { ` + listFields + ` } } }`
	var r struct {
		Issues struct {
			Nodes []Issue `json:"nodes"`
		} `json:"issues"`
	}
	err := c.do(ctx, q, vars, &r)
	return r.Issues.Nodes, err
}

// Scopes lists what a list can be narrowed to: the viewer's teams (when
// there's more than one), then their open projects, most recently updated
// first.
func (c *Client) Scopes(ctx context.Context) ([]Scope, error) {
	q := `query { viewer { teams { nodes { id key name } } }
	  projects(first: 100, orderBy: updatedAt, filter: {
	    accessibleTeams: { some: { members: { some: { isMe: { eq: true } } } } },
	    status: { type: { nin: ["completed", "canceled"] } } }) { nodes { id name } } }`
	var r struct {
		Viewer struct {
			Teams struct {
				Nodes []struct{ ID, Key, Name string } `json:"nodes"`
			} `json:"teams"`
		} `json:"viewer"`
		Projects struct {
			Nodes []struct{ ID, Name string } `json:"nodes"`
		} `json:"projects"`
	}
	if err := c.do(ctx, q, nil, &r); err != nil {
		return nil, err
	}
	var out []Scope
	if teams := r.Viewer.Teams.Nodes; len(teams) > 1 {
		for _, t := range teams {
			out = append(out, Scope{Kind: "team", ID: t.ID, Name: t.Key + " · " + t.Name})
		}
	}
	for _, p := range r.Projects.Nodes {
		out = append(out, Scope{Kind: "project", ID: p.ID, Name: p.Name})
	}
	return out, nil
}

// Issue loads one issue by identifier (ABC-964) or id, with its comments.
func (c *Client) Issue(ctx context.Context, id string) (Issue, error) {
	q := `query($id: String!) { issue(id: $id) { ` + issueFields + `
	  comments(first: 50) { nodes { body createdAt user { name } } }
	  attachments(first: 25) { nodes { url title subtitle sourceType metadata } }
	  parent { id identifier title description }
	  children(first: 50) { nodes { id identifier title state { id name type color position } assignee { name } } } } }`
	var r struct {
		Issue *Issue `json:"issue"`
	}
	if err := c.do(ctx, q, map[string]any{"id": id}, &r); err != nil {
		return Issue{}, err
	}
	if r.Issue == nil {
		return Issue{}, fmt.Errorf("issue %s not found", id)
	}
	return *r.Issue, nil
}

// IssueBrief is the little the sidebar shows about an issue.
type IssueBrief struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	State      State  `json:"state"`
}

// Briefs loads several issues by their IDs (UUIDs) in one request.
func (c *Client) Briefs(ctx context.Context, ids []string) ([]IssueBrief, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	q := `query($ids: [ID!]) { issues(first: 100, filter: { id: { in: $ids } }) {
	  nodes { id identifier title state { id name type color } } } }`
	var r struct {
		Issues struct {
			Nodes []IssueBrief `json:"nodes"`
		} `json:"issues"`
	}
	err := c.do(ctx, q, map[string]any{"ids": ids}, &r)
	return r.Issues.Nodes, err
}

// Search is Linear's full-text issue search (titles, descriptions, any team,
// closed issues too), best matches first.
func (c *Client) Search(ctx context.Context, term string, first int) ([]IssueBrief, error) {
	q := `query($t: String!, $n: Int) { searchIssues(term: $t, first: $n) {
	  nodes { id identifier title state { id name type color } } } }`
	var r struct {
		SearchIssues struct {
			Nodes []IssueBrief `json:"nodes"`
		} `json:"searchIssues"`
	}
	err := c.do(ctx, q, map[string]any{"t": term, "n": first}, &r)
	return r.SearchIssues.Nodes, err
}

// TeamStates returns a team's workflow states ordered by position.
func (c *Client) TeamStates(ctx context.Context, teamID string) ([]State, error) {
	q := `query($team: ID!) { workflowStates(first: 100, filter: { team: { id: { eq: $team } } }) {
	  nodes { id name type color position } } }`
	var r struct {
		WorkflowStates struct {
			Nodes []State `json:"nodes"`
		} `json:"workflowStates"`
	}
	if err := c.do(ctx, q, map[string]any{"team": teamID}, &r); err != nil {
		return nil, err
	}
	s := r.WorkflowStates.Nodes
	sort.SliceStable(s, func(a, b int) bool { return s[a].Position < s[b].Position })
	return s, nil
}

// IssueUpdate is the subset of IssueUpdateInput we change.
type IssueUpdate struct {
	StateID    string `json:"stateId,omitempty"`
	AssigneeID string `json:"assigneeId,omitempty"`
}

func (c *Client) UpdateIssue(ctx context.Context, id string, in IssueUpdate) error {
	q := `mutation($id: String!, $input: IssueUpdateInput!) { issueUpdate(id: $id, input: $input) { success } }`
	var r struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	if err := c.do(ctx, q, map[string]any{"id": id, "input": in}, &r); err != nil {
		return err
	}
	if !r.IssueUpdate.Success {
		return errors.New("Linear did not apply the change")
	}
	return nil
}

// StateRank orders state types for display: work in flight first.
func StateRank(t string) int {
	switch strings.ToLower(t) {
	case "started":
		return 0
	case "unstarted":
		return 1
	case "triage":
		return 2
	case "backlog":
		return 3
	default:
		return 4
	}
}

// SortWorkflow orders states the way Linear's status menu does: by type
// (backlog, todo, started, done, canceled, then triage), then by position.
func SortWorkflow(states []State) {
	rank := map[string]int{"backlog": 0, "unstarted": 1, "started": 2, "completed": 3, "canceled": 4, "triage": 5}
	sort.SliceStable(states, func(a, b int) bool {
		ra, rb := rank[states[a].Type], rank[states[b].Type]
		if ra != rb {
			return ra < rb
		}
		return states[a].Position < states[b].Position
	})
}

// PriorityRank orders priorities: urgent first, "no priority" last.
func PriorityRank(p int) int {
	if p == 0 {
		return 5
	}
	return p
}

// SortIssues sorts by state type, state position, priority, then most recently updated.
func SortIssues(is []Issue) {
	sort.SliceStable(is, func(a, b int) bool {
		x, y := is[a], is[b]
		if ra, rb := StateRank(x.State.Type), StateRank(y.State.Type); ra != rb {
			return ra < rb
		}
		if x.State.Position != y.State.Position {
			return x.State.Position < y.State.Position
		}
		if pa, pb := PriorityRank(x.Priority), PriorityRank(y.Priority); pa != pb {
			return pa < pb
		}
		return x.UpdatedAt.After(y.UpdatedAt)
	})
}

// Attachment is a link on an issue: a pull request, a Slack thread, a file.
type Attachment struct {
	URL        string         `json:"url"`
	Title      string         `json:"title"`
	Subtitle   string         `json:"subtitle"`
	SourceType string         `json:"sourceType"`
	Metadata   map[string]any `json:"metadata"`
}

// PR is a pull (or merge) request linked to an issue.
type PR struct {
	Number int
	Title  string
	URL    string
	Status string // "open", "draft", "merged", "closed", or "" when unknown
}

var rePRURL = regexp.MustCompile(`^https://(?:github\.com/[^/]+/[^/]+/pull|gitlab\.com/.+/-/merge_requests)/(\d+)`)

// PullRequests are the issue's linked pull requests, in Linear's order.
func (i Issue) PullRequests() []PR {
	if i.Attachments == nil {
		return nil
	}
	var out []PR
	for _, a := range i.Attachments.Nodes {
		m := rePRURL.FindStringSubmatch(a.URL)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		pr := PR{Number: n, Title: a.Title, URL: a.URL}
		if st, ok := a.Metadata["status"].(string); ok {
			pr.Status = strings.ToLower(Clean(st))
		}
		if d, ok := a.Metadata["draft"].(bool); ok && d && pr.Status == "open" {
			pr.Status = "draft"
		}
		out = append(out, pr)
	}
	return out
}

// Person is a workspace member.
type Person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TeamOptions is a team a new issue can go to.
type TeamOptions struct {
	ID, Key, Name string
}

// ProjectOption is an open project and the teams it's in.
type ProjectOption struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Teams []string `json:"teams"`
}

// NewIssueOptions: my teams and open projects, for the new issue form.
type NewIssueOptions struct {
	Me       string
	Teams    []TeamOptions
	Projects []ProjectOption
}

// NewIssueOptions loads the teams and projects; a team's states and
// members come separately (TeamStates, TeamMembers), for the team chosen:
// all of them in one query is more than Linear allows.
func (c *Client) NewIssueOptions(ctx context.Context) (NewIssueOptions, error) {
	q := `query { viewer { id teams { nodes { id key name } } }
	  projects(first: 100, orderBy: updatedAt, filter: {
	    accessibleTeams: { some: { members: { some: { isMe: { eq: true } } } } },
	    status: { type: { nin: ["completed", "canceled"] } } }) { nodes { id name teams(first: 10) { nodes { id } } } } }`
	var r struct {
		Viewer struct {
			ID    string `json:"id"`
			Teams struct {
				Nodes []struct{ ID, Key, Name string } `json:"nodes"`
			} `json:"teams"`
		} `json:"viewer"`
		Projects struct {
			Nodes []struct {
				ID, Name string
				Teams    struct {
					Nodes []struct{ ID string } `json:"nodes"`
				} `json:"teams"`
			} `json:"nodes"`
		} `json:"projects"`
	}
	if err := c.do(ctx, q, nil, &r); err != nil {
		return NewIssueOptions{}, err
	}
	out := NewIssueOptions{Me: r.Viewer.ID}
	for _, t := range r.Viewer.Teams.Nodes {
		out.Teams = append(out.Teams, TeamOptions{ID: t.ID, Key: t.Key, Name: t.Name})
	}
	for _, p := range r.Projects.Nodes {
		po := ProjectOption{ID: p.ID, Name: p.Name}
		for _, t := range p.Teams.Nodes {
			po.Teams = append(po.Teams, t.ID)
		}
		out.Projects = append(out.Projects, po)
	}
	return out, nil
}

// TeamMembers: a team's active members, by name.
func (c *Client) TeamMembers(ctx context.Context, teamID string) ([]Person, error) {
	q := `query($team: String!) { team(id: $team) { members(first: 250) { nodes { id name active } } } }`
	var r struct {
		Team struct {
			Members struct {
				Nodes []struct {
					ID, Name string
					Active   bool
				} `json:"nodes"`
			} `json:"members"`
		} `json:"team"`
	}
	if err := c.do(ctx, q, map[string]any{"team": teamID}, &r); err != nil {
		return nil, err
	}
	var out []Person
	for _, p := range r.Team.Members.Nodes {
		if p.Active {
			out = append(out, Person{p.ID, p.Name})
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}

// IssueCreate is a new issue; empty fields are Linear's defaults.
type IssueCreate struct {
	TeamID      string `json:"teamId"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	StateID     string `json:"stateId,omitempty"`
	Priority    int    `json:"priority,omitempty"`
	AssigneeID  string `json:"assigneeId,omitempty"`
	ProjectID   string `json:"projectId,omitempty"`
}

// CreateIssue files a new issue and returns it as the list shows issues.
func (c *Client) CreateIssue(ctx context.Context, in IssueCreate) (Issue, error) {
	q := `mutation($input: IssueCreateInput!) { issueCreate(input: $input) { success issue { ` + listFields + ` } } }`
	var r struct {
		IssueCreate struct {
			Success bool   `json:"success"`
			Issue   *Issue `json:"issue"`
		} `json:"issueCreate"`
	}
	if err := c.do(ctx, q, map[string]any{"input": in}, &r); err != nil {
		return Issue{}, err
	}
	if !r.IssueCreate.Success || r.IssueCreate.Issue == nil {
		return Issue{}, errors.New("Linear did not create the issue")
	}
	return *r.IssueCreate.Issue, nil
}
