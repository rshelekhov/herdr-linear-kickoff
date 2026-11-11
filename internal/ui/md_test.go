package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func plain(s string) string { return ansi.Strip(s) }

func TestRenderMarkdown(t *testing.T) {
	cases := []struct{ in, want string }{
		// Linear's autolinks: text equals the target → one short link.
		{"* [https://app.example.com/inbox?id=ckq7rbtzx00010l8a4mn2vwhe](<https://app.example.com/inbox?id=ckq7rbtzx00010l8a4mn2vwhe>)",
			"• app.example.com/inbox?id=ckq7rbt…0010l8a4mn2vwhe"},
		{"![Screenshot 2026-01-05 at 10.30.48.png](https://uploads.linear.app/1a2b3c4d/5e6f7a8b/9c0d1e2f)",
			"▣ Screenshot 2026-01-05 at 10.30.48.png"},
		{"![](https://uploads.linear.app/a/b/shot.png)", "▣ shot.png"},
		{"see [the doc](https://notion.so/x) now", "see the doc now"},
		{"@sam1 про календарь", "@sam1 про календарь"},
		{"## Steps", "Steps"},
		{"**bold** and `code` and __under__", "bold and code and under"},
		{"- [ ] todo\n- [x] done", "☐ todo\n☑ done"},
		{"> quoted", "│ quoted"},
		{"a\n\n\n\nb", "a\n\nb"},
		{"```\n**raw**\n```", "  **raw**"},
		{`a\_b \[x\]`, "a_b [x]"},
		{"mail me@example.com", "mail me@example.com"},
	}
	for _, c := range cases {
		if got := plain(renderMarkdown(c.in)); got != c.want {
			t.Errorf("%q\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestRelTime(t *testing.T) {
	now := time.Date(2026, 11, 10, 13, 0, 0, 0, time.Local)
	cases := map[time.Time]string{
		now.Add(-30 * time.Second): "just now",
		now.Add(-25 * time.Minute): "25m ago",
		now.Add(-3 * time.Hour):    "3h ago",
		now.Add(-20 * time.Hour):   "yesterday",
		now.AddDate(0, -2, 0):      "Sep 10",
		now.AddDate(-1, 0, 0):      "Nov 10, 2025",
	}
	for at, want := range cases {
		if got := relTime(at, now); got != want {
			t.Errorf("%v: got %q want %q", at, got, want)
		}
	}
}

func TestLighten(t *testing.T) {
	if got, ok := lighten("#131721", 0.07); !ok || got != "#242731" {
		t.Errorf("lighten = %q %v", got, ok)
	}
	if got, _ := lighten("#000000", 1); got != "#ffffff" {
		t.Errorf("lighten black fully = %q", got)
	}
	if _, ok := lighten("red", 0.1); ok {
		t.Error("accepted a non-hex colour")
	}
}

func TestMarkdownTableAndCode(t *testing.T) {
	src := "Plans:\n\n| Plan | Price |\n|---|---:|\n| Free | $0 |\n| Team with a long name | $8 |\n\n```go\nfunc main() {}\n```"
	out := renderMarkdown(src)
	plain := ansi.Strip(out)
	for _, s := range []string{"Plan                   Price", "Free                   $0", "──", "  func main() {}"} {
		if !strings.Contains(plain, s) {
			t.Errorf("lacks %q:\n%s", s, plain)
		}
	}
	if strings.Contains(plain, "|") || strings.Contains(plain, "```") {
		t.Errorf("markdown syntax left:\n%s", plain)
	}
	// Colours aren't checked: tests run without a colour profile.
}
