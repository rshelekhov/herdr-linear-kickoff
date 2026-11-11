package ui

import (
	"math"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// Colours. Hues (accent, red, green, …) come in light/dark pairs. Neutrals
// (dim text, selection, guides, the footer bar) are derived from the popup's
// background when it's known, so they sit right on any theme; otherwise they
// fall back to pairs too.
var (
	cAccent lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#5e6ad2", Dark: "#8b93f8"}
	cErr    lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#c4314b", Dark: "#ff6b81"}
	cOK     lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#2d8a4e", Dark: "#5fd38d"}
	cWarm   lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#b85a00", Dark: "#f2994a"}
	cCode   lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#9a5b00", Dark: "#e6b673"}

	cDim   lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#8a8a8a", Dark: "#6c6c6c"}
	cFaint lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#d4d5dc", Dark: "#363b47"} // comment bars
	cSel   lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#e4e6fb", Dark: "#2b2f4a"}
	cBar   lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#ececf1", Dark: "#1b1f2a"}
	cBarFg lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#5c5f66", Dark: "#9097a3"}
)

var (
	sDim, sBold, sAccent, sErr, sOK lipgloss.Style
	sSel, sTabOn, sTabOff, sBar     lipgloss.Style
	sLink, sCode, sMention          lipgloss.Style
)

func init() { buildStyles() }

func buildStyles() {
	sDim = lipgloss.NewStyle().Foreground(cDim)
	sBold = lipgloss.NewStyle().Bold(true)
	sAccent = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sErr = lipgloss.NewStyle().Foreground(cErr)
	sOK = lipgloss.NewStyle().Foreground(cOK)
	sSel = lipgloss.NewStyle().Background(cSel)
	sTabOn = lipgloss.NewStyle().Background(cSel).Bold(true).Padding(0, 1)
	sTabOff = lipgloss.NewStyle().Foreground(cDim).Padding(0, 1)
	sBar = lipgloss.NewStyle().Background(cBar).Foreground(cBarFg).Padding(0, 2)
	sLink = lipgloss.NewStyle().Underline(true)
	sCode = lipgloss.NewStyle().Foreground(cCode)
	sMention = lipgloss.NewStyle().Foreground(cAccent)
}

// applyBackground fits the palette to a known popup background ("#rrggbb"):
// light or dark is decided from the colour itself (asking the terminal may go
// unanswered inside a herdr popup), and neutrals are steps away from it.
func applyBackground(bg string) bool {
	if _, _, _, ok := rgb(bg); !ok {
		return false
	}
	dark := luminance(bg) < 0.4
	lipgloss.SetHasDarkBackground(dark)
	popupBg = bg
	readableMemo = &sync.Map{}
	// Steps toward more contrast; light backgrounds need smaller ones for
	// surfaces and larger ones for text (TestPaletteContrastOnAllThemes).
	step := func(onDark, onLight float64) lipgloss.TerminalColor {
		if dark {
			return lipgloss.Color(toward(bg, onDark, true))
		}
		return lipgloss.Color(toward(bg, onLight, false))
	}
	cDim = step(0.48, 0.52)
	cFaint = step(0.16, 0.12)
	cSel = step(0.14, 0.09)
	cBar = step(0.07, 0.05)
	cBarFg = step(0.62, 0.62)
	buildStyles()
	return true
}

// toward moves a colour f (0..1) of the way to white on a dark background,
// to black on a light one: more contrast either way.
func toward(hex string, f float64, dark bool) string {
	if dark {
		out, _ := lighten(hex, f)
		return out
	}
	r, g, b, _ := rgb(hex)
	mix := func(c uint64) uint64 { return uint64(float64(c)*(1-f) + 0.5) }
	return hexOf(mix(r), mix(g), mix(b))
}

func hexOf(r, g, b uint64) string {
	const digits = "0123456789abcdef"
	out := []byte("#000000")
	for i, c := range []uint64{r, g, b} {
		out[1+2*i], out[2+2*i] = digits[c>>4&0xf], digits[c&0xf]
	}
	return string(out)
}

// luminance is the WCAG relative luminance of "#rrggbb" (0 black … 1 white).
func luminance(hex string) float64 {
	r, g, b, _ := rgb(hex)
	lin := func(c uint64) float64 {
		v := float64(c) / 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// contrast is the WCAG contrast ratio of two colours (1 … 21).
func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// popupBg is the popup's background when known ("" otherwise).
var popupBg string

var readableMemo = &sync.Map{} // colour → its readable version, for popupBg

// minIconContrast: WCAG's minimum for graphics (3:1). Linear's grey state
// colours (Todo #e2e2e2, Backlog #bec2c8) are made for a dark background and
// all but vanish on a light one.
const minIconContrast = 3.0

// readable returns a colour from Linear (a state's) as it can be seen on the
// popup's background: unchanged if it already stands out, else stepped
// toward black on a light background, toward white on a dark one, until it
// does. Without a known background the colour is left as it is.
func readable(hex string) string {
	if popupBg == "" {
		return hex
	}
	if _, _, _, ok := rgb(hex); !ok {
		return hex
	}
	if v, ok := readableMemo.Load(hex); ok {
		return v.(string)
	}
	dark := luminance(popupBg) < 0.4
	out := hex
	for i := 0; i < 20 && contrast(out, popupBg) < minIconContrast; i++ {
		out = toward(out, 0.1, dark)
	}
	readableMemo.Store(hex, out)
	return out
}
