package ui

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Code blocks and tables in issue descriptions.

// highlight colours a code block by its language (from the fence, else a
// guess). Only the text colours are used: the popup's background stays.
func highlight(code, lang string) []string {
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		return plainCode(code)
	}
	// Light only when the popup's background is known to be light: asking
	// the terminal may go unanswered inside a herdr popup.
	name := "github-dark"
	if popupBg != "" && luminance(popupBg) >= 0.4 {
		name = "github"
	}
	style := styles.Get(name)
	it, err := chroma.Coalesce(lexer).Tokenise(nil, code)
	if err != nil {
		return plainCode(code)
	}
	var b strings.Builder
	for _, tok := range it.Tokens() {
		e := style.Get(tok.Type)
		st := lipgloss.NewStyle()
		if e.Colour.IsSet() {
			st = st.Foreground(lipgloss.Color(e.Colour.String()))
		}
		if e.Bold == chroma.Yes {
			st = st.Bold(true)
		}
		// Styles end at line breaks: each line is rendered on its own.
		parts := strings.Split(tok.Value, "\n")
		for i, p := range parts {
			if i > 0 {
				b.WriteString("\n")
			}
			if p != "" {
				b.WriteString(st.Render(p))
			}
		}
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return lines
}

func plainCode(code string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(code, "\n"), "\n") {
		out = append(out, sCode.Render("  "+l))
	}
	return out
}

// isTableRow: a markdown table line, "| a | b |".
func isTableRow(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "|") && strings.Count(t, "|") >= 2
}

func tableCells(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimSuffix(strings.TrimPrefix(t, "|"), "|")
	cells := strings.Split(t, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// isRule: the "|---|:---:|" line under a table's header.
func isRule(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, "-: ") != "" || !strings.Contains(c, "-") {
			return false
		}
	}
	return len(cells) > 0
}

// renderTable lays a table out in aligned columns: the header bold, a thin
// rule under it, cells up to a fair width each.
func renderTable(rows []string) []string {
	var grid [][]string
	header := false
	for i, r := range rows {
		c := tableCells(r)
		if i == 1 && isRule(c) {
			header = true
			continue
		}
		for j := range c {
			c[j] = inline(c[j])
		}
		grid = append(grid, c)
	}
	cols := 0
	for _, r := range grid {
		cols = max(cols, len(r))
	}
	widths := make([]int, cols)
	for _, r := range grid {
		for j, c := range r {
			widths[j] = min(max(widths[j], lipgloss.Width(c)), 40)
		}
	}
	var out []string
	for i, r := range grid {
		var cells []string
		for j := range cols {
			c := ""
			if j < len(r) {
				c = ansi.Truncate(r[j], widths[j], "…")
			}
			if header && i == 0 {
				c = sBold.Render(c)
			}
			cells = append(cells, c+strings.Repeat(" ", widths[j]-lipgloss.Width(c)))
		}
		out = append(out, strings.TrimRight(strings.Join(cells, "  "), " "))
		if header && i == 0 {
			var rule []string
			for _, w := range widths {
				rule = append(rule, strings.Repeat("─", w))
			}
			out = append(out, sDim.Render(strings.Join(rule, "  ")))
		}
	}
	return out
}
