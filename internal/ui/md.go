package ui

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A light markdown cleanup for the preview: it strips the syntax noise
// (duplicated link targets, image URLs, bullets on their own line) rather than
// laying the text out like a document.

var (

	// One alternation, applied in a single pass, so styled output is never re-matched.
	reInline = regexp.MustCompile(
		`!\[([^\]]*)\]\(<?([^)>\s]*)>?[^)]*\)` + // 1,2 image
			`|\[([^\]]+)\]\(<?([^)>\s]*)>?[^)]*\)` + // 3,4 link
			"|`([^`]+)`" + // 5 code
			`|\*\*([^*]+)\*\*|__([^_]+)__` + // 6,7 bold
			`|(https?://[^\s)>\]]+)` + // 8 bare url
			`|(^|[\s(])(@[\w.\-]+)`) // 9,10 mention
	reEscape  = regexp.MustCompile(`\\([\\*_\[\]()#>+\-.!` + "`" + `])`)
	reHeading = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	reBullet  = regexp.MustCompile(`^(\s*)[*+-]\s+(.*)$`)
	reTask    = regexp.MustCompile(`^\[( |x|X)\]\s+(.*)$`)
	reQuote   = regexp.MustCompile(`^>\s?(.*)$`)
	reRule    = regexp.MustCompile(`^\s*([-*_])(\s*[-*_]){2,}\s*$`)
)

// renderMarkdown turns Linear markdown into styled terminal text.
func renderMarkdown(src string) string {
	src = strings.ReplaceAll(strings.TrimSpace(src), "\r\n", "\n")
	var out []string
	inCode, lang := false, ""
	var code, table []string
	flushTable := func() {
		if len(table) > 0 {
			out = append(out, renderTable(table)...)
			table = nil
		}
	}
	for _, line := range strings.Split(src, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") {
			if inCode {
				out = append(out, highlight(strings.Join(code, "\n"), lang)...)
				code = nil
			} else {
				flushTable()
				lang = strings.TrimSpace(strings.TrimPrefix(t, "```"))
			}
			inCode = !inCode
			continue
		}
		if inCode {
			code = append(code, line)
			continue
		}
		if isTableRow(line) {
			table = append(table, line)
			continue
		}
		flushTable()
		switch {
		case reRule.MatchString(line):
			out = append(out, sDim.Render("───"))
		case reHeading.MatchString(line):
			out = append(out, sBold.Render(inline(reHeading.FindStringSubmatch(line)[1])))
		case reQuote.MatchString(line):
			out = append(out, sDim.Render("│ ")+inline(reQuote.FindStringSubmatch(line)[1]))
		case reBullet.MatchString(line):
			m := reBullet.FindStringSubmatch(line)
			mark, text := "•", m[2]
			if t := reTask.FindStringSubmatch(text); t != nil {
				mark, text = "☐", t[2]
				if t[1] != " " {
					mark = "☑"
				}
			}
			out = append(out, m[1]+sDim.Render(mark)+" "+inline(text))
		default:
			out = append(out, inline(line))
		}
	}
	flushTable()
	if inCode { // an unclosed fence: still code
		out = append(out, highlight(strings.Join(code, "\n"), lang)...)
	}
	// Collapse runs of blank lines.
	var b []string
	for _, l := range out {
		if strings.TrimSpace(l) == "" && len(b) > 0 && strings.TrimSpace(b[len(b)-1]) == "" {
			continue
		}
		b = append(b, l)
	}
	return strings.Join(b, "\n")
}

func inline(s string) string {
	s = reInline.ReplaceAllStringFunc(s, func(tok string) string {
		g := reInline.FindStringSubmatch(tok)
		switch {
		case strings.HasPrefix(tok, "!["):
			name := g[1]
			if name == "" {
				name = lastPathPart(g[2])
			}
			return sDim.Render("▣ " + name)
		case g[3] != "":
			text := unescape(g[3])
			if text == g[4] || strings.HasPrefix(text, "http") {
				return sLink.Render(shortURL(g[4]))
			}
			return sLink.Render(text)
		case g[5] != "":
			return sCode.Render(g[5])
		case g[6] != "" || g[7] != "":
			return sBold.Render(g[6] + g[7])
		case g[8] != "":
			return sLink.Render(shortURL(g[8]))
		case g[10] != "":
			return g[9] + sMention.Render(g[10])
		}
		return tok
	})
	return unescape(s)
}

func unescape(s string) string { return reEscape.ReplaceAllString(s, "$1") }

// shortURL drops the scheme and trailing slash and caps the length, cutting
// the middle: links often differ only in their trailing id.
func shortURL(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	u = strings.TrimSuffix(strings.TrimPrefix(u, "www."), "/")
	if r := []rune(u); len(r) > 48 {
		u = string(r[:32]) + "…" + string(r[len(r)-15:])
	}
	return u
}

func lastPathPart(u string) string {
	u = strings.TrimSuffix(u, "/")
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return u[i+1:]
	}
	return "image"
}

// relTime renders a timestamp the way chat apps do.
func relTime(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour && t.Day() == now.Day():
		return strconv.Itoa(int(d.Hours())) + "h ago"
	case d < 48*time.Hour && t.Day() == now.AddDate(0, 0, -1).Day():
		return "yesterday"
	case t.Year() == now.Year():
		return t.Local().Format("Jan 2")
	}
	return t.Local().Format("Jan 2, 2006")
}

// rgb parses "#rrggbb".
func rgb(hex string) (r, g, b uint64, ok bool) {
	if len(hex) != 7 || hex[0] != '#' {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return v >> 16 & 0xff, v >> 8 & 0xff, v & 0xff, true
}

// lighten mixes a "#rrggbb" colour with white by f (0..1).
func lighten(hex string, f float64) (string, bool) {
	if len(hex) != 7 || hex[0] != '#' {
		return "", false
	}
	v, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return "", false
	}
	mix := func(c uint64) uint64 { return c + uint64(float64(255-c)*f+0.5) }
	r, g, b := mix(v>>16&0xff), mix(v>>8&0xff), mix(v&0xff)
	return "#" + strconv.FormatUint(r<<16|g<<8|b|1<<24, 16)[1:], true
}
