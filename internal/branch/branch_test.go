package branch

import "testing"

func TestSlug(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"Optimize the web app for phone", 50, "optimize-the-web-app-for-phone"},
		{"  Fix: login redirect!!  ", 50, "fix-login-redirect"},
		{"Café déjà vu", 50, "cafe-deja-vu"},
		{"Сделать экспорт в Excel", 50, "sdelat-eksport-v-excel"},
		{"Объявление и щука", 50, "obyavlenie-i-shchuka"},
		{"Мой новый ёжик", 50, "moy-novyy-ezhik"},
		{"one two three four five", 12, "one-two"},
		{"", 50, ""},
		{"🚀🚀", 50, ""},
	}
	for _, c := range cases {
		if got := Slug(c.in, c.max); got != c.want {
			t.Errorf("Slug(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestRender(t *testing.T) {
	v := Vars{
		Linear: "abc-964-optimize-the-web-app-for-phone", Identifier: "ABC-964", Team: "ABC", Number: 964,
		Title: "Optimize the web app for phone", Type: "feat", Me: "alex", SlugMax: 50,
	}
	cases := map[string]string{
		"{linear}":             "abc-964-optimize-the-web-app-for-phone",
		"{type}/{slug}":        "feat/optimize-the-web-app-for-phone",
		"{type}/{id}-{slug}":   "feat/abc-964-optimize-the-web-app-for-phone",
		"{me}/{ID}":            "alex/ABC-964",
		"{team}-{number}":      "abc-964",
		"{type}//{unknown}--x": "feat/unknown-x",
	}
	for tmpl, want := range cases {
		if got := Render(tmpl, v); got != want {
			t.Errorf("Render(%q) = %q, want %q", tmpl, got, want)
		}
	}
	v.Linear = ""
	if got := Render("{linear}", v); got != "abc-964-optimize-the-web-app-for-phone" {
		t.Errorf("fallback linear = %q", got)
	}
	v.Type = ""
	if got := Render("{type}/{slug}", v); got != "optimize-the-web-app-for-phone" {
		t.Errorf("empty type = %q", got)
	}
}

func TestTypeFor(t *testing.T) {
	m := map[string]string{"bug": "fix", "feature": "feat"}
	if got := TypeFor([]string{"Frontend", "Bug"}, m, "chore"); got != "fix" {
		t.Errorf("got %q", got)
	}
	if got := TypeFor([]string{"Frontend"}, m, "chore"); got != "chore" {
		t.Errorf("got %q", got)
	}
}

func TestMentionsIssue(t *testing.T) {
	yes := []string{"abc-964-x", "feat/abc-964-x", "alex/ABC-964", "abc-964", "feat/v2-abc-964", "web-12-for-abc-964"}
	no := []string{"abc-9640-x", "xabc-964", "feat/optimize", "", "abc-970-follow-up-to-abc-964", "alex/abc-1-then-abc-964"}
	for _, b := range yes {
		if !MentionsIssue(b, "ABC-964") {
			t.Errorf("%q should mention ABC-964", b)
		}
	}
	for _, b := range no {
		if MentionsIssue(b, "ABC-964") {
			t.Errorf("%q should not mention ABC-964", b)
		}
	}
}
