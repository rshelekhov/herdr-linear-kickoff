package ui

import (
	"fmt"
	"sort"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/config"
)

// hexFor resolves a palette colour the way it renders on this background.
func hexFor(c lipgloss.TerminalColor, dark bool) string {
	switch v := c.(type) {
	case lipgloss.Color:
		return string(v)
	case lipgloss.AdaptiveColor:
		if dark {
			return v.Dark
		}
		return v.Light
	}
	panic(fmt.Sprintf("unexpected colour %T", c))
}

// Every role must stay readable (text) or visible (surfaces, guides) on every
// built-in herdr theme, plus a hand-set panel_bg like the author's.
func TestPaletteContrastOnAllThemes(t *testing.T) {
	saved := []lipgloss.TerminalColor{cDim, cFaint, cSel, cBar, cBarFg}
	defer func() {
		cDim, cFaint, cSel, cBar, cBarFg = saved[0], saved[1], saved[2], saved[3], saved[4]
		lipgloss.SetHasDarkBackground(true)
		popupBg = ""
		buildStyles()
	}()

	bgs := map[string]string{"custom #131721": "#131721"}
	for name, bg := range config.ThemePanelBg {
		if bg != "" {
			bgs[name] = bg
		}
	}
	names := make([]string, 0, len(bgs))
	for n := range bgs {
		names = append(names, n)
	}
	sort.Strings(names)

	type check struct {
		role string
		fg   lipgloss.TerminalColor
		on   func(bg string) string // what it sits on
		min  float64
	}
	onBg := func(bg string) string { return bg }
	onBar := func(string) string { return "" } // filled in per theme
	for _, name := range names {
		bg := bgs[name]
		if !applyBackground(bg) {
			t.Fatalf("%s: background %q rejected", name, bg)
		}
		dark := luminance(bg) < 0.4
		bar := hexFor(cBar, dark)
		onBar = func(string) string { return bar }
		checks := []check{
			{"dim text", cDim, onBg, 3.0},
			{"accent", cAccent, onBg, 3.0},
			{"error", cErr, onBg, 3.0},
			{"ok/worktree", cOK, onBg, 3.0},
			{"priority !", cWarm, onBg, 2.5},
			{"code", cCode, onBg, 3.0},
			{"footer text", cBarFg, onBar, 4.5},
			{"selection", cSel, onBg, 1.18},
			{"footer bar", cBar, onBg, 1.08},
			{"comment bar", cFaint, onBg, 1.25},
			// On a selected row: the dim identifier and the priority marks.
			{"dim on selection", cDim, func(string) string { return hexFor(cSel, dark) }, 2.5},
			{"!! on selection", cErr, func(string) string { return hexFor(cSel, dark) }, 2.5},
			{"! on selection", cWarm, func(string) string { return hexFor(cSel, dark) }, 2.0},
		}
		for _, c := range checks {
			fg, under := hexFor(c.fg, dark), c.on(bg)
			if got := contrast(fg, under); got < c.min {
				t.Errorf("%-16s %-20s %s on %s: contrast %.2f < %.2f", name, c.role, fg, under, got, c.min)
			}
		}
	}
}

// Linear's state colours: every one visible on every theme, and the ones that
// already are left alone.
func TestStateColorsOnAllThemes(t *testing.T) {
	saved := []lipgloss.TerminalColor{cDim, cFaint, cSel, cBar, cBarFg}
	defer func() {
		cDim, cFaint, cSel, cBar, cBarFg = saved[0], saved[1], saved[2], saved[3], saved[4]
		lipgloss.SetHasDarkBackground(true)
		popupBg = ""
		buildStyles()
	}()
	// Linear's defaults: triage, backlog, todo, in progress, in review, done, canceled.
	states := []string{"#fc7840", "#bec2c8", "#e2e2e2", "#f2c94c", "#4cb782", "#5e6ad2", "#95a2b3"}
	for name, bg := range config.ThemePanelBg {
		if bg == "" {
			continue
		}
		applyBackground(bg)
		for _, c := range states {
			got := readable(c)
			if r := contrast(got, bg); r < minIconContrast {
				t.Errorf("%s: %s → %s, contrast %.2f", name, c, got, r)
			}
			if contrast(c, bg) >= minIconContrast && got != c {
				t.Errorf("%s: %s already stands out, changed to %s", name, c, got)
			}
		}
	}
	popupBg = ""
	if readable("#e2e2e2") != "#e2e2e2" {
		t.Error("changed without a known background")
	}
}
