package linear

import (
	"encoding/json"
	"testing"
)

func TestSanitize(t *testing.T) {
	raw := `{"identifier":"ABC-1","title":"x\u001b]52;c;cGF3bmVk\u0007y","description":"line\n\tok\u009b31m",
	  "assignee":{"id":"u","name":"Eve\u001b[2J"},"labels":{"nodes":[{"name":"bug\u001b[0m"}]},
	  "parent":{"identifier":"ABC-0","title":"p\u001b]0;title\u0007"},
	  "comments":{"nodes":[{"body":"hi\u001b[31m red","user":{"name":"Mallory\u001b"}}]}}`
	var is Issue
	if err := json.Unmarshal([]byte(raw), &is); err != nil {
		t.Fatal(err)
	}
	Sanitize(&is)
	checks := map[string]string{
		is.Title:                       "x]52;c;cGF3bmVky",
		is.Description:                 "line\n\tok31m",
		is.Assignee.Name:               "Eve[2J",
		is.Labels.Nodes[0].Name:        "bug[0m",
		is.Parent.Title:                "p]0;title",
		is.Comments.Nodes[0].Body:      "hi[31m red",
		is.Comments.Nodes[0].User.Name: "Mallory",
	}
	for got, want := range checks {
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}
