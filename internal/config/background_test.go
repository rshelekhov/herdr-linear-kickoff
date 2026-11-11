package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBackground(t *testing.T) {
	dir := t.TempDir()
	withBg := filepath.Join(dir, "a.toml")
	os.WriteFile(withBg, []byte("[theme]\nname = \"terminal\"\n[theme.custom]\npanel_bg = \"#131721\"\n"), 0o600)
	noBg := filepath.Join(dir, "b.toml")
	os.WriteFile(noBg, []byte("[theme]\nname = \"terminal\"\n"), 0o600)
	named := filepath.Join(dir, "c.toml")
	os.WriteFile(named, []byte("[theme.custom]\npanel_bg = \"reset\"\n"), 0o600)

	cases := []struct{ setting, path, want string }{
		{"auto", withBg, "#131721"},
		{"auto", noBg, ""},  // theme "terminal": the terminal's own background
		{"auto", named, ""}, // not a hex colour: leave the terminal default
		{"auto", filepath.Join(dir, "missing.toml"), "#181825"}, // no config: herdr's default theme
		{"none", withBg, ""},
		{"", withBg, ""},
		{"#0B0E14", withBg, "#0B0E14"},
		{"red", withBg, ""},
		{"#123\x1b]0;x", withBg, ""}, // never let an escape sequence through
	}
	for _, c := range cases {
		if got := ResolveBackground(c.setting, c.path); got != c.want {
			t.Errorf("%q in %s: got %q want %q", c.setting, filepath.Base(c.path), got, c.want)
		}
	}
}

func TestBuiltinThemes(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "c.toml")
		os.WriteFile(p, []byte(body), 0o600)
		return p
	}
	systemDark = func() bool { return true }
	defer func() { systemDark = func() bool { return false } }()

	cases := []struct{ cfg, want string }{
		{"[theme]\nname = \"nord\"\n", "#2e3440"},
		{"[theme]\nname = \"Tokyo Night\"\n", "#1a1b26"},   // spaces, case
		{"[theme]\nname = \"latte\"\n", "#eff1f5"},         // alias
		{"[theme]\nname = \"no-such-theme\"\n", "#181825"}, // herdr's fallback
		{"[ui]\nsidebar_width = 30\n", "#181825"},          // no theme: default
		{"[theme]\nname = \"terminal\"\n", ""},
		{"[theme]\nname = \"nord\"\n[theme.custom]\npanel_bg = \"#101010\"\n", "#101010"},
		{"[theme]\nname = \"nord\"\n[theme.custom]\npanel_bg = \"reset\"\n", ""},
		{"[theme]\nauto_switch = true\ndark_name = \"dracula\"\nlight_name = \"one-light\"\n", "#282a36"},
		{"[theme]\nauto_switch = true\ndark_name = \"dracula\"\n[theme.custom.dark]\npanel_bg = \"#202020\"\n", "#202020"},
	}
	for _, c := range cases {
		if got := ResolveBackground("auto", write(c.cfg)); got != c.want {
			t.Errorf("%q: got %q want %q", c.cfg, got, c.want)
		}
	}
	systemDark = func() bool { return false }
	if got := ResolveBackground("auto", write("[theme]\nauto_switch = true\ndark_name = \"dracula\"\nlight_name = \"one-light\"\n")); got != "#fafafa" {
		t.Errorf("auto_switch light: got %q", got)
	}
}
