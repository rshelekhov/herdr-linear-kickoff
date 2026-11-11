package linear

import (
	"strings"
	"testing"
)

func TestScopeFilter(t *testing.T) {
	cases := []struct {
		s     Scope
		teams bool
		frag  string
		vars  bool
	}{
		{Scope{}, false, "", false},
		{Scope{}, true, "team: { members: { some: { isMe: { eq: true } } } }, ", false},
		{Scope{Kind: "project", ID: "p1"}, false, "project: { id: { eq: $scope } }, ", true},
		{Scope{Kind: "project", ID: "p1"}, true, "project: { id: { eq: $scope } }, team: { members: { some: { isMe: { eq: true } } } }, ", true},
		{Scope{Kind: "team", ID: "t1"}, true, "team: { members: { some: { isMe: { eq: true } } } id: { eq: $scope } }, ", true},
		{Scope{Kind: "team", ID: "t1"}, false, "team: { id: { eq: $scope } }, ", true},
	}
	for _, c := range cases {
		frag, vars := c.s.filter(c.teams)
		if frag != c.frag || (vars != nil) != c.vars {
			t.Errorf("%+v teams=%v: %q %v", c.s, c.teams, frag, vars)
		}
		if strings.Contains(frag, "null") {
			t.Error("null in filter")
		}
	}
}
