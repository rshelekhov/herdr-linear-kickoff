package linear

import "testing"

func TestPullRequests(t *testing.T) {
	var is Issue
	if is.PullRequests() != nil {
		t.Error("PRs out of nothing")
	}
	is.Attachments = &struct {
		Nodes []Attachment `json:"nodes"`
	}{[]Attachment{
		{URL: "https://github.com/acme/web/pull/482", Title: "Save cards", Metadata: map[string]any{"status": "Open", "draft": true}},
		{URL: "https://acme.slack.com/archives/C1/p2", Title: "thread"},
		{URL: "https://github.com/acme/web/pull/479", Title: "Vault client", Metadata: map[string]any{"status": "merged"}},
		{URL: "https://gitlab.com/acme/api/-/merge_requests/12", Title: "MR"},
	}}
	prs := is.PullRequests()
	if len(prs) != 3 {
		t.Fatalf("%+v", prs)
	}
	if prs[0].Number != 482 || prs[0].Status != "draft" || prs[1].Status != "merged" || prs[2].Number != 12 || prs[2].Status != "" {
		t.Errorf("%+v", prs)
	}
}
