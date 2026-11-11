package linear

import (
	"regexp"
	"strings"
)

var (
	reIssueURL = regexp.MustCompile(`(?i)linear\.app/([^/\s]+)/issue/([a-z][a-z0-9]*-[0-9]+)`)
	reIdent    = regexp.MustCompile(`(?i)(?:^|[^a-z0-9-])([a-z][a-z0-9]*-[0-9]+)(?:$|[^a-z0-9-])`)
)

// ParseIdentifier finds an issue identifier ("ABC-964") in what was typed or
// pasted: the identifier itself, any case, or a linear.app issue link.
func ParseIdentifier(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if m := reIssueURL.FindStringSubmatch(s); m != nil {
		return strings.ToUpper(m[2]), true
	}
	if strings.Contains(s, "://") { // some other link: don't guess from its path
		return "", false
	}
	if m := reIdent.FindStringSubmatch(s); m != nil {
		return strings.ToUpper(m[1]), true
	}
	return "", false
}

// LinkWorkspace is the workspace's URL key in a linear.app issue link
// ("acme" in linear.app/acme/issue/ABC-1), or "" when s isn't one.
func LinkWorkspace(s string) string {
	if m := reIssueURL.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}
