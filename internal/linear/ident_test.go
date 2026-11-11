package linear

import "testing"

func TestParseIdentifier(t *testing.T) {
	cases := map[string]string{
		"ABC-964":    "ABC-964",
		"abc-964":    "ABC-964",
		"  ABC-964 ": "ABC-964",
		"https://linear.app/acme/issue/ABC-734/improve-tabs": "ABC-734",
		"linear.app/acme/issue/for-5":                        "FOR-5",
		"see ABC-12 please":                                  "ABC-12",
		"https://github.com/acme/repo/pull/ABC-1":            "",
		"964":  "",
		"":     "",
		"ABC-": "",
	}
	for in, want := range cases {
		got, ok := ParseIdentifier(in)
		if got != want || ok != (want != "") {
			t.Errorf("%q → %q %v, want %q", in, got, ok, want)
		}
	}
}

func TestLinkWorkspace(t *testing.T) {
	for in, want := range map[string]string{
		"https://linear.app/Acme/issue/ABC-734/improve-tabs": "acme",
		"linear.app/other-co/issue/X-1":                      "other-co",
		"ABC-734":                                            "",
		"https://github.com/acme/issue/ABC-1":                "",
	} {
		if got := LinkWorkspace(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
