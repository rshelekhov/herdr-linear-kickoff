package kickoff

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
)

// RenderPrompt fills the prompt template in one pass, so text from the issue
// that happens to contain "{title}" is never expanded again.
//
// Anyone who can edit an issue or comment on it controls the text placed
// between the data tags, and could close a tag early ("</issue-description>")
// to make what follows read as instructions. So every data tag gets a random
// suffix for this prompt (<issue-description-3f9a1c7e>), one that appears
// nowhere in the issue's text.
func RenderPrompt(tmpl string, is linear.Issue, branchName string) string {
	desc := strings.TrimSpace(is.Description)
	if desc == "" {
		desc = "(no description)"
	}
	comments := FormatComments(is.SortedComments())
	untrusted := is.Title + desc + comments + parentText(is.Parent) + subIssuesText(is.SubIssues())
	fence := newFence(untrusted)

	var tags []string
	for _, t := range dataTags {
		tags = append(tags, "<"+t+">", "<"+t+"-"+fence+">", "</"+t+">", "</"+t+"-"+fence+">")
	}
	tmpl = strings.NewReplacer(tags...).Replace(tmpl) // the template's own tags only

	return strings.NewReplacer(
		"{identifier}", is.Identifier,
		"{title}", is.Title,
		"{url}", is.URL,
		"{branch}", branchName,
		"{description}", desc,
		"{comments}", comments,
		"{parent}", FormatParent(is.Parent, fence),
		"{subissues}", FormatSubIssues(is.SubIssues(), fence),
	).Replace(tmpl)
}

// dataTags wrap untrusted text in prompts.
var dataTags = []string{"issue-title", "issue-description", "issue-comments", "parent-issue", "sub-issues"}

// newFence is a random suffix that does not occur in the untrusted text.
func newFence(untrusted string) string {
	for {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		f := hex.EncodeToString(b)
		if !strings.Contains(untrusted, f) {
			return f
		}
	}
}

func parentText(p *linear.IssueRef) string {
	if p == nil {
		return ""
	}
	return p.Title + p.Description
}

func subIssuesText(cs []linear.IssueRef) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(c.Title)
	}
	return b.String()
}

// FormatComments renders comments oldest first.
func FormatComments(cs []linear.Comment) string {
	if len(cs) == 0 {
		return "(no comments)"
	}
	var b strings.Builder
	for i, c := range cs {
		if i > 0 {
			b.WriteString("\n\n")
		}
		who := "?"
		if c.User != nil {
			who = c.User.Name
		}
		fmt.Fprintf(&b, "— %s, %s:\n%s", who, c.CreatedAt.Format("2006-01-02"), strings.TrimSpace(c.Body))
	}
	return b.String()
}

// FormatParent renders the parent issue as a tagged block, or "" for a
// top-level issue, so the template line disappears.
func FormatParent(p *linear.IssueRef, fence string) string {
	if p == nil {
		return ""
	}
	desc := strings.TrimSpace(p.Description)
	if desc == "" {
		desc = "(no description)"
	}
	return fmt.Sprintf("This is a sub-issue. Its parent, %s, for context:\n<parent-issue-%s>\n%s\n\n%s\n</parent-issue-%s>\n\n",
		p.Identifier, fence, p.Title, desc, fence)
}

// FormatSubIssues lists sub-issues with their states, or "" if there are none.
func FormatSubIssues(cs []linear.IssueRef, fence string) string {
	if len(cs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("It has sub-issues:\n<sub-issues-" + fence + ">\n")
	for _, c := range cs {
		state := ""
		if c.State != nil {
			state = " [" + c.State.Name + "]"
		}
		fmt.Fprintf(&b, "- %s%s %s\n", c.Identifier, state, c.Title)
	}
	b.WriteString("</sub-issues-" + fence + ">\n\n")
	return b.String()
}
