package demo

import (
	"fmt"
	"strings"
	"time"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
)

// The demo workspaces: two made-up companies. Acme has two teams and two
// projects, Northwind one of each. Everything here is invented for
// screenshots and trying the plugin without a key.

var viewer = linear.Viewer{ID: "u-alex", Name: "Alex Rivera", DisplayName: "alex"}

var people = map[string]string{
	"u-alex": "Alex Rivera", "u-sam": "Sam Chen", "u-priya": "Priya Nair", "u-jonas": "Jonas Weber",
}

type team struct{ id, key, name string }

var (
	teamWeb = team{"team-web", "WEB", "Web app"}
	teamAPI = team{"team-api", "API", "Platform"}
	teamOps = team{"team-ops", "OPS", "Operations"}
)

type project struct{ id, name string }

var (
	projCheckout   = project{"proj-checkout", "Checkout v2"}
	projOnboarding = project{"proj-onboarding", "Self-serve onboarding"}
	projUpgrade    = project{"proj-upgrade", "Platform upgrade"}
)

// world is one demo workspace.
type world struct {
	id, name, urlKey string
	teams            []team
	projects         []project
	seeds            []seed
}

var (
	acme      = world{"org-acme", "Acme", "acme", []team{teamWeb, teamAPI}, []project{projCheckout, projOnboarding}, seeds}
	northwind = world{"org-northwind", "Northwind", "northwind", []team{teamOps}, []project{projUpgrade}, opsSeeds}
	worlds    = []world{acme, northwind}
)

// Workflow states, as a new Linear team has them.
var states = []linear.State{
	{ID: "triage", Name: "Triage", Type: "triage", Color: "#fc7840", Position: 0},
	{ID: "backlog", Name: "Backlog", Type: "backlog", Color: "#bec2c8", Position: 1},
	{ID: "todo", Name: "Todo", Type: "unstarted", Color: "#e2e2e2", Position: 2},
	{ID: "progress", Name: "In Progress", Type: "started", Color: "#f2c94c", Position: 3},
	{ID: "review", Name: "In Review", Type: "started", Color: "#4cb782", Position: 4},
	{ID: "done", Name: "Done", Type: "completed", Color: "#5e6ad2", Position: 5},
	{ID: "canceled", Name: "Canceled", Type: "canceled", Color: "#95a2b3", Position: 6},
}

func stateByID(id string) linear.State {
	for _, s := range states {
		if s.ID == id {
			return s
		}
	}
	return states[0]
}

type comment struct {
	who, body string
	ago       time.Duration
}

type seed struct {
	id, title, state, who string // who: "" = unassigned
	prio                  int
	label                 string
	proj                  *project
	parent                string
	ago                   time.Duration
	desc                  string
	comments              []comment
	cycle                 int     // 0: none
	due                   int     // days from today; 0: none
	est                   float64 // points; 0: none
	prs                   []pr
}

// pr is a pull request Linear linked to the issue.
type pr struct {
	n             int
	title, status string
}

const h = time.Hour

var seeds = []seed{
	// Mine
	{id: "WEB-214", title: "Save cards for faster checkout", state: "progress", who: "u-alex", prio: 2, label: "Feature", proj: &projCheckout, ago: 2 * h,
		desc: `Returning customers type their card on every order. Let them save a card at checkout and pick it next time.

## Scope

- Save the card after a successful payment (opt-in checkbox)
- Show saved cards above the card form
- Remove a card in **Settings → Billing**

## Out of scope

- More than one default card
- Wallets (Apple Pay, Google Pay)

Card data stays with the payment provider: we keep only the token, the last four digits and the expiry date.`,
		cycle: 14, due: 6, est: 5,
		prs: []pr{{482, "Saved cards on the payment step", "draft"}, {479, "Card vault client", "merged"}},
		comments: []comment{
			{"u-sam", "The vault client from WEB-215 is merged behind the `cards_vault` flag, reuse it.", 26 * h},
			{"u-priya", "Designs for the saved card list are in Figma, on the Checkout v2 page.", 5 * h},
		}},
	{id: "WEB-215", title: "Card vault client", state: "progress", who: "u-alex", prio: 2, label: "Feature", proj: &projCheckout, parent: "WEB-214", ago: 3 * h,
		desc: "A thin client for the provider's card vault: `save`, `list`, `remove`. Tokens only, never card numbers."},
	{id: "WEB-216", title: "Saved card picker on the payment step", state: "todo", who: "u-alex", prio: 3, label: "Feature", proj: &projCheckout, parent: "WEB-214", ago: 20 * h,
		desc: "Radio list of saved cards above the card form; \"Use another card\" opens the form."},
	{id: "WEB-217", title: "Remove a saved card in billing settings", state: "todo", who: "u-sam", prio: 3, label: "Feature", proj: &projCheckout, parent: "WEB-214", ago: 22 * h,
		desc: "A *Remove* action per card, with a confirmation."},
	{id: "WEB-198", title: "Checkout button stays disabled after a failed payment", state: "review", who: "u-alex", prio: 1, label: "Bug", proj: &projCheckout, ago: 1 * h,
		desc: `After a declined card the **Pay** button stays disabled until the page is reloaded.

Steps:
1. Pay with the test card ` + "`4000 0000 0000 0002`" + `
2. See the "card declined" message
3. Fix the card number — the button is still disabled

Expected: the button comes back once the form is valid again.`,
		cycle: 14, due: -1, est: 2,
		prs:      []pr{{488, "Re-enable Pay when the form is valid again", "open"}},
		comments: []comment{{"u-jonas", "Reproduced on staging, Chrome and Safari.", 30 * h}}},
	{id: "API-87", title: "Rate-limit the public webhooks endpoint", state: "progress", who: "u-alex", prio: 2, label: "Improvement", ago: 4 * h,
		desc: "One partner sends bursts of 2k requests a second. Limit per API key, answer `429` with `Retry-After`.\n\n" +
			"| Plan | Limit |\n|---|---:|\n| Free | 10 req/s |\n| Pro | 100 req/s |\n| Enterprise | per contract |\n\n" +
			"```go\nif !limiter.Allow(key) {\n\tw.Header().Set(\"Retry-After\", \"1\")\n\thttp.Error(w, \"slow down\", http.StatusTooManyRequests)\n\treturn\n}\n```"},
	{id: "WEB-221", title: "Dark mode for the billing settings page", state: "todo", who: "u-alex", prio: 3, label: "Feature", ago: 28 * h,
		desc: "The billing page is the last one with hard-coded colors. Move it to the theme tokens."},
	{id: "API-91", title: "Retry failed invoice emails with backoff", state: "todo", who: "u-alex", prio: 3, label: "Bug", ago: 50 * h,
		desc: "When the mail provider is down, invoice emails are lost. Queue them and retry with exponential backoff, up to 24 hours."},
	{id: "WEB-203", title: "Flaky e2e test: coupon applied twice", state: "todo", who: "u-alex", prio: 4, label: "Bug", proj: &projCheckout, ago: 70 * h,
		desc: "`checkout/coupon.spec.ts` fails about once in twenty runs: the coupon shows up twice in the summary."},
	{id: "WEB-230", title: "Address autocomplete in the shipping form", state: "backlog", who: "u-alex", prio: 0, label: "Feature", proj: &projCheckout, ago: 90 * h,
		desc: "Suggest addresses as the customer types."},

	// The team's
	{id: "WEB-209", title: "Estimated delivery date in the order summary", state: "progress", who: "u-priya", prio: 3, label: "Feature", proj: &projCheckout, ago: 6 * h,
		desc: "Show \"Arrives Tue, Mar 12\" under the shipping method, from the carrier's estimate."},
	{id: "API-82", title: "Move sessions to Redis", state: "review", who: "u-jonas", prio: 2, label: "Improvement", ago: 8 * h,
		desc: "Sessions live in Postgres and make up half of its writes."},
	{id: "WEB-225", title: "Onboarding checklist for new workspaces", state: "progress", who: "u-sam", prio: 3, label: "Feature", proj: &projOnboarding, ago: 10 * h,
		desc: "Five steps: invite the team, connect a store, add a product, set up payments, place a test order."},
	{id: "API-89", title: "Invite links expire too early", state: "todo", who: "u-priya", prio: 2, label: "Bug", proj: &projOnboarding, ago: 30 * h,
		desc: "Links are meant to last 7 days but stop working after 24 hours."},
	{id: "WEB-227", title: "Empty state for the projects page", state: "todo", prio: 4, label: "Improvement", proj: &projOnboarding, ago: 40 * h,
		desc: "A new workspace sees an empty table. Show what a project is and a *New project* button."},
	{id: "API-95", title: "500 on /v1/orders with an empty cart", state: "triage", prio: 1, label: "Bug", ago: 3 * h,
		desc: "Reported by support: `POST /v1/orders` with no items returns 500 instead of 422."},
	{id: "WEB-231", title: "Typo in the welcome email subject", state: "triage", prio: 4, label: "Bug", proj: &projOnboarding, ago: 12 * h,
		desc: "\"Welcome to Acme, you're all set up\" — *set up*, not *setup*."},

	// Backlog
	{id: "WEB-180", title: "Keyboard shortcuts for the orders table", state: "backlog", prio: 0, label: "Feature", ago: 300 * h,
		desc: "`j`/`k` to move, `enter` to open, `/` to search."},
	{id: "API-60", title: "GraphQL API for partners", state: "backlog", prio: 3, label: "Feature", ago: 500 * h,
		desc: "Partners ask for one query instead of five REST calls."},
	{id: "WEB-176", title: "Export orders to CSV", state: "backlog", prio: 0, label: "Feature", ago: 400 * h,
		desc: "With the current filters applied."},
	{id: "API-71", title: "Audit log for admin actions", state: "backlog", who: "u-jonas", prio: 3, label: "Feature", ago: 350 * h,
		desc: "Who changed what and when, kept for a year."},
	{id: "WEB-190", title: "Remember the last used payment method", state: "backlog", prio: 4, label: "Improvement", proj: &projCheckout, ago: 200 * h,
		desc: "Preselect it on the next checkout."},

	// Closed: only Linear's search finds them
	{id: "WEB-150", title: "Apple Pay at checkout", state: "done", who: "u-sam", prio: 2, label: "Feature", proj: &projCheckout, ago: 900 * h,
		desc: "Shipped in 3.4."},
	{id: "WEB-162", title: "Checkout page loads twice on Safari", state: "done", who: "u-alex", prio: 2, label: "Bug", proj: &projCheckout, ago: 700 * h,
		desc: "A prefetch and a navigation raced."},
	{id: "API-40", title: "Payment webhooks drop events under load", state: "done", who: "u-jonas", prio: 1, label: "Bug", ago: 1200 * h,
		desc: "Fixed by acknowledging first and processing from the queue."},
	{id: "WEB-171", title: "Keep the old checkout flow behind a flag", state: "canceled", prio: 4, label: "Improvement", proj: &projCheckout, ago: 800 * h,
		desc: "Not needed: everyone is on the new flow."},
}

// Northwind's issues: a smaller workspace to switch to.
var opsSeeds = []seed{
	{id: "OPS-31", title: "Rotate the staging database credentials", state: "progress", who: "u-alex", prio: 2, label: "Improvement", proj: &projUpgrade, ago: 5 * h,
		desc: "The staging credentials haven't changed since the migration. Rotate them and move them to the secrets manager."},
	{id: "OPS-28", title: "Nightly backup skips the uploads bucket", state: "todo", who: "u-alex", prio: 1, label: "Bug", ago: 9 * h,
		desc: "The backup job lists buckets by prefix, and `uploads` doesn't match it."},
	{id: "OPS-35", title: "Move CI runners to arm64", state: "todo", who: "u-jonas", prio: 3, label: "Improvement", proj: &projUpgrade, ago: 30 * h,
		desc: "Cheaper and faster for our builds; two images still need arm64 builds."},
	{id: "OPS-22", title: "Terraform state lock times out", state: "backlog", prio: 3, label: "Bug", ago: 200 * h,
		desc: "Two applies at once leave the lock behind."},
	{id: "OPS-12", title: "Alert on certificate expiry", state: "done", who: "u-alex", prio: 2, label: "Feature", ago: 600 * h,
		desc: "Two weeks ahead, to the on-call channel."},
}

// build turns a workspace's seeds into full issues, with sub-issues and
// comments.
func build(w world, now time.Time) []linear.Issue {
	byID := map[string]int{}
	seeds := w.seeds
	out := make([]linear.Issue, 0, len(seeds))
	for _, s := range seeds {
		key, num := splitID(s.id)
		t := w.teams[0]
		for _, x := range w.teams {
			if x.key == key {
				t = x
			}
		}
		is := linear.Issue{
			ID: "id-" + strings.ToLower(s.id), Identifier: s.id, Number: num, Title: s.title,
			URL:         "https://linear.app/" + w.urlKey + "/issue/" + strings.ToLower(s.id),
			BranchName:  strings.ToLower(s.id),
			Description: s.desc, Priority: s.prio, PriorityLabel: prioLabel(s.prio),
			UpdatedAt: now.Add(-s.ago), State: stateByID(s.state),
		}
		is.Team.ID, is.Team.Key, is.Team.Name = t.id, t.key, t.name
		if s.who != "" {
			is.Assignee = &struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}{s.who, people[s.who]}
		}
		if s.proj != nil {
			is.Project = &struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}{s.proj.id, s.proj.name}
		}
		if s.label != "" {
			is.Labels.Nodes = append(is.Labels.Nodes, struct {
				Name string `json:"name"`
			}{s.label})
		}
		cs := &struct {
			Nodes []linear.Comment `json:"nodes"`
		}{}
		for _, c := range s.comments {
			cm := linear.Comment{Body: c.body, CreatedAt: now.Add(-c.ago)}
			cm.User = &struct {
				Name string `json:"name"`
			}{people[c.who]}
			cs.Nodes = append(cs.Nodes, cm)
		}
		is.Comments = cs
		if s.cycle != 0 {
			is.Cycle = &struct {
				Number int    `json:"number"`
				Name   string `json:"name"`
			}{s.cycle, ""}
		}
		if s.due != 0 {
			is.DueDate = now.AddDate(0, 0, s.due).Format("2006-01-02")
		}
		if s.est != 0 {
			e := s.est
			is.Estimate = &e
		}
		is.Attachments = &struct {
			Nodes []linear.Attachment `json:"nodes"`
		}{}
		for _, p := range s.prs {
			repo := "web"
			if t.key == teamAPI.key {
				repo = "api"
			}
			is.Attachments.Nodes = append(is.Attachments.Nodes, linear.Attachment{
				URL:   fmt.Sprintf("https://github.com/%s/%s/pull/%d", w.urlKey, repo, p.n),
				Title: p.title, SourceType: "github", Metadata: map[string]any{"status": p.status},
			})
		}
		is.CommentProbe = &struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		}{}
		if len(s.comments) > 0 {
			is.CommentProbe.Nodes = make([]struct {
				ID string `json:"id"`
			}, 1)
		}
		is.Children = &struct {
			Nodes []linear.IssueRef `json:"nodes"`
		}{}
		is.ChildProbe = &struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		}{}
		byID[s.id] = len(out)
		out = append(out, is)
	}
	// Parents and sub-issues.
	for i, s := range seeds {
		if s.parent == "" {
			continue
		}
		p := &out[byID[s.parent]]
		c := &out[i]
		c.Parent = &linear.IssueRef{ID: p.ID, Identifier: p.Identifier, Title: p.Title}
		st := c.State
		ref := linear.IssueRef{ID: c.ID, Identifier: c.Identifier, Title: c.Title, Description: c.Description, State: &st}
		if c.Assignee != nil {
			ref.Assignee = &struct {
				Name string `json:"name"`
			}{c.Assignee.Name}
		}
		p.Children.Nodes = append(p.Children.Nodes, ref)
		p.ChildProbe.Nodes = make([]struct {
			ID string `json:"id"`
		}, 1)
	}
	return out
}

func splitID(id string) (string, int) {
	key, n, _ := strings.Cut(id, "-")
	num := 0
	for _, r := range n {
		num = num*10 + int(r-'0')
	}
	return key, num
}

func prioLabel(p int) string {
	return [...]string{"No priority", "Urgent", "High", "Medium", "Low"}[p]
}
