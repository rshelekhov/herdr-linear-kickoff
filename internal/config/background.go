package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

var reHex = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ResolveBackground: "auto" takes herdr's panel_bg from its config (the popup
// frame is filled with it), "none"/"" means the terminal default, anything
// else must be a hex colour.
func ResolveBackground(setting, herdrConfig string) string {
	switch v := strings.TrimSpace(setting); strings.ToLower(v) {
	case "", "none":
		return ""
	case "auto":
		return herdrPanelBg(herdrConfig)
	default:
		if reHex.MatchString(v) {
			return v
		}
		return ""
	}
}

// HerdrConfigPath is herdr's config.toml.
func HerdrConfigPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "herdr", "config.toml")
}

// ThemePanelBg is panel_bg of herdr's built-in themes (herdr v0.9.5,
// src/app/state.rs); "" for "terminal", which uses the terminal's own colours.
var ThemePanelBg = map[string]string{
	"catppuccin":       "#181825",
	"catppuccin-latte": "#eff1f5",
	"terminal":         "",
	"tokyo-night":      "#1a1b26",
	"tokyo-night-day":  "#e1e2e7",
	"dracula":          "#282a36",
	"nord":             "#2e3440",
	"gruvbox":          "#282828",
	"gruvbox-light":    "#fbf1c7",
	"one-dark":         "#282c34",
	"one-light":        "#fafafa",
	"solarized":        "#002b36",
	"solarized-light":  "#fdf6e3",
	"kanagawa":         "#1f1f28",
	"kanagawa-lotus":   "#f2ecbc",
	"rose-pine":        "#191724",
	"rose-pine-dawn":   "#faf4ed",
	"vesper":           "#1a1a1a",
}

// themeAliases mirrors herdr's canonical_theme_name.
var themeAliases = map[string]string{
	"catppuccin-mocha": "catppuccin", "latte": "catppuccin-latte", "light": "catppuccin-latte",
	"tokyonight": "tokyo-night", "tokyo-day": "tokyo-night-day", "tokyonight-day": "tokyo-night-day",
	"gruvbox-dark": "gruvbox", "onedark": "one-dark", "onelight": "one-light",
	"solarized-dark": "solarized", "lotus": "kanagawa-lotus", "rosepine": "rose-pine",
	"rosepine-dawn": "rose-pine-dawn", "dawn": "rose-pine-dawn",
}

func canonicalTheme(name string) string {
	n := strings.NewReplacer(" ", "-", "_", "-").Replace(strings.ToLower(strings.TrimSpace(name)))
	if a, ok := themeAliases[n]; ok {
		return a
	}
	return n
}

// systemDark reports the host's dark appearance, for theme.auto_switch.
var systemDark = func() bool {
	out, err := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle").Output()
	return err == nil && strings.Contains(string(out), "Dark")
}

type panelBgOverride struct {
	PanelBg *string `toml:"panel_bg"`
}

// herdrPanelBg is the colour herdr fills the popup frame with: the theme's
// panel_bg, overridden by theme.custom (and custom.dark / custom.light under
// auto_switch). "" when it's the terminal's own background or not a hex colour.
func herdrPanelBg(path string) string {
	var c struct {
		Theme struct {
			Name       string `toml:"name"`
			AutoSwitch bool   `toml:"auto_switch"`
			DarkName   string `toml:"dark_name"`
			LightName  string `toml:"light_name"`
			Custom     struct {
				PanelBg *string         `toml:"panel_bg"`
				Dark    panelBgOverride `toml:"dark"`
				Light   panelBgOverride `toml:"light"`
			} `toml:"custom"`
		} `toml:"theme"`
	}
	if _, err := toml.DecodeFile(path, &c); err != nil && !os.IsNotExist(err) {
		return ""
	}
	t := c.Theme
	name := t.Name
	var appearance panelBgOverride
	if t.AutoSwitch {
		if systemDark() {
			name, appearance = firstNonEmpty(t.DarkName, name), t.Custom.Dark
		} else {
			name, appearance = firstNonEmpty(t.LightName, name), t.Custom.Light
		}
	}
	bg, ok := ThemePanelBg[canonicalTheme(firstNonEmpty(name, "catppuccin"))]
	if !ok {
		bg = ThemePanelBg["catppuccin"] // herdr falls back to its default theme
	}
	for _, o := range []*string{t.Custom.PanelBg, appearance.PanelBg} {
		if o != nil {
			bg = strings.TrimSpace(*o)
		}
	}
	if reHex.MatchString(bg) {
		return bg
	}
	return "" // "reset", a named colour, …: leave the terminal's background
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
