package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
)

// The search palette ("/" and ":"): one box over the list that finds an
// issue in every loaded tab as you type, then — after a pause — in Linear
// itself (closed issues, other teams, descriptions). An issue ID or a
// linear.app link opens that issue directly.

const (
	palSearchAfter = 300 * time.Millisecond // pause before asking Linear
	palMinRemote   = 3                      // runes typed before asking Linear
	palRemoteMax   = 20
	palRows        = 12 // the results area: always this tall, so the box never jumps
)

type (
	palTickMsg   string // the query to search Linear for, if still current
	palResultMsg struct {
		q       string
		results []linear.IssueBrief
		err     error
	}
)

type palKind int

const (
	palGoto   palKind = iota // "open ABC-123": a typed ID or link
	palLocal                 // in a loaded tab
	palRemote                // found by Linear's search
)

type palItem struct {
	kind  palKind
	id    string
	title string
	state linear.State
	mode  linear.Mode // the loaded tab it's in, if any
	idHL  []int       // matched rune positions in id
	tHL   []int       // matched rune positions in title
	score int
}

// openPalette shows the palette, empty.
func (m *model) openPalette() tea.Cmd {
	m.going, m.gotoErr, m.palCur = true, "", 0
	m.gotoIn.SetValue("")
	return m.gotoIn.Focus()
}

func (m *model) closePalette() {
	m.going, m.gotoOnly = false, false
	m.gotoIn.Blur()
}

func (m model) palQuery() string { return strings.TrimSpace(m.gotoIn.Value()) }

// palItems are the palette's rows for the current query, best first:
// the typed ID, the loaded tabs' matches, then Linear's.
func (m model) palItems() []palItem {
	q := m.palQuery()
	if q == "" {
		return nil // nothing typed, nothing to show
	}
	var out []palItem
	seen := map[string]bool{}
	gotoID, isID := linear.ParseIdentifier(q)

	// Loaded tabs, the current one first.
	modes := []linear.Mode{m.mode}
	for _, md := range linear.Modes {
		if md != m.mode {
			modes = append(modes, md)
		}
	}
	var local []palItem
	where := map[string]linear.Mode{} // every loaded issue's tab
	for mi, md := range modes {
		list := m.lists[md]
		if md == m.mode {
			list = m.issues // what's on screen, even before it's cached
		}
		for _, is := range list {
			if _, ok := where[is.Identifier]; !ok {
				where[is.Identifier] = md
			}
			if seen[is.Identifier] {
				continue
			}
			score, idHL, tHL, ok := matchIssue(q, is.Identifier, is.Title)
			if !ok {
				continue
			}
			it := palItem{kind: palLocal, id: is.Identifier, title: is.Title, state: is.State, mode: md,
				score: score, idHL: idHL, tHL: tHL}
			it.score -= mi // ties: the current tab first
			seen[is.Identifier] = true
			local = append(local, it)
		}
	}
	slices.SortStableFunc(local, func(a, b palItem) int { return b.score - a.score })

	if isID && !seen[gotoID] {
		out = append(out, palItem{kind: palGoto, id: gotoID, title: "open " + gotoID})
	}
	out = append(out, local...)
	for _, r := range m.palRemote[q] {
		if seen[r.Identifier] || (isID && r.Identifier == gotoID) {
			continue
		}
		seen[r.Identifier] = true
		_, idHL, tHL, _ := matchIssue(q, r.Identifier, r.Title)
		// Matched by its description, say, but in a tab: shown and opened as there.
		out = append(out, palItem{kind: palRemote, id: r.Identifier, title: r.Title, state: r.State, mode: where[r.Identifier], idHL: idHL, tHL: tHL})
	}
	return out
}

// palTyped follows an edit of the query: back to the top match, and Linear
// is asked after a pause if the query is long enough and new.
func (m *model) palTyped() tea.Cmd {
	m.palCur = 0
	q := m.palQuery()
	if _, isID := linear.ParseIdentifier(q); isID || utf8.RuneCountInString(q) < palMinRemote || m.client == nil {
		return nil
	}
	if _, done := m.palRemote[q]; done {
		return nil
	}
	return tea.Tick(palSearchAfter, func(time.Time) tea.Msg { return palTickMsg(q) })
}

func (m *model) palSearch(q string) tea.Cmd {
	if q != m.palQuery() || !m.going || m.palAsking[q] {
		return nil // typed on since, or closed, or already asked
	}
	m.palAsking[q] = true
	c := m.client
	return m.tag(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		res, err := c.Search(ctx, q, palRemoteMax)
		return palResultMsg{q, res, err}
	})
}

func (m *model) palResult(msg palResultMsg) {
	delete(m.palAsking, msg.q)
	if msg.err != nil {
		if msg.q == m.palQuery() {
			m.gotoErr = "Linear search failed: " + errText(msg.err)
		}
		return
	}
	m.palRemote[msg.q] = msg.results
}

func (m model) onPaletteKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.palItems()
	switch k.String() {
	case "esc":
		if m.gotoOnly {
			return m, tea.Quit
		}
		m.closePalette()
		return m, nil
	case "up", "ctrl+p", "ctrl+k":
		m.palCur = max(0, m.palCur-1)
		return m, nil
	case "down", "ctrl+n", "ctrl+j":
		m.palCur = min(len(items)-1, m.palCur+1)
		return m, nil
	case "enter":
		if m.palCur >= len(items) {
			if q := m.palQuery(); q != "" {
				m.gotoErr = "nothing found for " + q
			}
			return m, nil
		}
		it := items[m.palCur]
		m.closePalette()
		return m, m.palOpen(it)
	}
	var cmd tea.Cmd
	before := m.gotoIn.Value()
	m.gotoIn, cmd = m.gotoIn.Update(k)
	if m.gotoIn.Value() != before {
		m.gotoErr = ""
		cmd = tea.Batch(cmd, m.palTyped())
	}
	return m, cmd
}

// palOpen shows the chosen issue: in its tab when it's loaded, otherwise
// fetched and shown where it lives (as a link would).
func (m *model) palOpen(it palItem) tea.Cmd {
	if it.mode == "" {
		m.openWS = linear.LinkWorkspace(m.palQuery()) // a pasted link may name another workspace
		return m.openByID(it.id)
	}
	m.drill, m.subFocus, m.pv.scroll = nil, false, 0
	if it.mode == m.mode {
		return m.openByID(it.id)
	}
	m.selectID = it.id
	cmd := m.switchMode(it.mode)
	m.selectPending()
	return cmd
}

var modeShort = map[linear.Mode]string{linear.ModeMine: "Mine", linear.ModeTeam: "All", linear.ModeBacklog: "Backlog", linear.ModeRecent: "Recent"}

func (m model) paletteDialog() string {
	w := min(88, max(44, m.w-8))
	inner := w - 4 // padding (the border is outside the width)
	items := m.palItems()
	q := m.palQuery()

	var b strings.Builder
	count := ""
	nLocal := 0
	for _, it := range items {
		if it.kind == palLocal {
			nLocal++
		}
	}
	if q != "" {
		count = sDim.Render(fmt.Sprintf("%d in lists", nLocal))
	}
	in := m.gotoIn.View()
	b.WriteString(in + strings.Repeat(" ", max(1, inner-lipgloss.Width(in)-lipgloss.Width(count))) + count + "\n")
	if m.gotoErr != "" {
		b.WriteString(sErr.Render(ansi.Truncate(m.gotoErr, inner, "…")) + "\n")
	}
	b.WriteString("\n")

	// Rows, scrolled so the cursor stays in view, in an area of fixed
	// height: the box keeps its size from the first key to the last.
	rows := max(5, min(palRows, m.h-12))
	var lines []string
	cursorLine := 0
	header := ""
	for i, it := range items {
		h := ""
		if it.kind == palRemote {
			h = "In Linear"
		}
		if h != header {
			lines = append(lines, "", sBold.Render(h))
			header = h
		}
		if i == m.palCur {
			cursorLine = len(lines)
		}
		lines = append(lines, m.palRow(it, i == m.palCur, inner))
	}
	if _, isID := linear.ParseIdentifier(q); !isID && utf8.RuneCountInString(q) >= palMinRemote && m.palRemote[q] == nil {
		lines = append(lines, "", sDim.Render("searching Linear…"))
	} else if q != "" && len(items) == 0 {
		lines = append(lines, sDim.Render("nothing found"))
	} else if q == "" {
		lines = append(lines,
			sDim.Render("Type part of a title to search the loaded tabs;"),
			sDim.Render("from 3 letters Linear is searched too."),
			sDim.Render("An ID (ABC-123) or a linear.app link opens that issue."))
	}
	start := 0
	if cursorLine >= rows {
		start = cursorLine - rows + 1
	}
	end := min(len(lines), start+rows)
	shown := lines[start:end]
	for len(shown) < rows { // a fixed height: typing doesn't resize the box
		shown = append(shown, "")
	}
	b.WriteString(strings.Join(shown, "\n"))
	b.WriteString("\n\n" + sDim.Render("↑↓ choose · enter open · esc close"))
	return dialog("Search", b.String(), w)
}

// palRow: state icon, ID, title with the matched letters lit, and where it
// is (its tab, or its state when it came from Linear). Every segment carries
// base so the selection background spans the row.
func (m model) palRow(it palItem, sel bool, w int) string {
	base := lipgloss.NewStyle()
	mark := "  "
	if sel {
		base = sSel
		mark = "› "
	}
	hl := base.Foreground(cAccent).Bold(true)
	tag := ""
	switch it.kind {
	case palLocal:
		tag = modeShort[it.mode]
	case palRemote:
		tag = it.state.Name
		if it.mode != "" {
			tag = modeShort[it.mode]
		}
	}
	head := base.Render(mark)
	if it.kind == palGoto {
		head += base.Render("→ ")
		return padRow(head+hl.Render(it.title), w, base)
	}
	head += stateDotOn(it.state, base) + base.Render(" ")
	head += lit(it.id, it.idHL, base, hl) + base.Render(strings.Repeat(" ", max(1, 10-len(it.id))))
	tagW := 0
	if tag != "" {
		tagW = lipgloss.Width(tag) + 2
	}
	room := max(0, w-lipgloss.Width(head)-tagW)
	title := it.title
	if utf8.RuneCountInString(title) > room {
		title = string([]rune(title)[:max(0, room-1)]) + "…"
	}
	row := head + lit(title, it.tHL, base, hl)
	row += base.Render(strings.Repeat(" ", max(0, w-lipgloss.Width(row)-tagW)))
	if tag != "" {
		row += base.Render("  ") + base.Foreground(cDim).Render(tag)
	}
	return ansi.Truncate(row, w, "")
}

func padRow(s string, w int, base lipgloss.Style) string {
	return ansi.Truncate(s+base.Render(strings.Repeat(" ", max(0, w-lipgloss.Width(s)))), w, "")
}

// lit renders s with the runes at pos in hl, the rest in base.
func lit(s string, pos []int, base, hl lipgloss.Style) string {
	if len(pos) == 0 {
		return base.Render(s)
	}
	on := map[int]bool{}
	for _, p := range pos {
		on[p] = true
	}
	var b strings.Builder
	var run []rune
	runLit := false
	flush := func() {
		if len(run) == 0 {
			return
		}
		if runLit {
			b.WriteString(hl.Render(string(run)))
		} else {
			b.WriteString(base.Render(string(run)))
		}
		run = run[:0]
	}
	for i, r := range []rune(s) {
		if on[i] != runLit {
			flush()
			runLit = on[i]
		}
		run = append(run, r)
	}
	flush()
	return b.String()
}

// matchIssue matches the query against "ID title". Best: the query as one
// piece; then every word somewhere; then the letters in order (fuzzy).
// Returns the matched rune positions in id and in title.
func matchIssue(q, id, title string) (score int, idHL, tHL []int, ok bool) {
	text := []rune(strings.ToLower(id + " " + title))
	split := func(pos []int) {
		n := utf8.RuneCountInString(id) + 1
		for _, p := range pos {
			if p < n-1 {
				idHL = append(idHL, p)
			} else if p >= n {
				tHL = append(tHL, p-n)
			}
		}
	}
	ql := []rune(strings.ToLower(strings.TrimSpace(q)))
	if len(ql) == 0 {
		return 0, nil, nil, true
	}
	if i := runeIndex(text, ql, 0); i >= 0 {
		score = 3000 - i
		if i == 0 || !isWordRune(text[i-1]) {
			score += 500 // starts a word
		}
		split(span(i, len(ql)))
		return score, idHL, tHL, true
	}
	words := strings.Fields(string(ql))
	if len(words) > 1 {
		var pos []int
		for _, w := range words {
			i := runeIndex(text, []rune(w), 0)
			if i < 0 {
				pos = nil
				break
			}
			pos = append(pos, span(i, utf8.RuneCountInString(w))...)
		}
		if pos != nil {
			split(pos)
			return 2000, idHL, tHL, true
		}
	}
	// Fuzzy: the letters in order; fewer gaps score higher.
	var pos []int
	j, gaps := 0, 0
	for i := 0; i < len(text) && j < len(ql); i++ {
		if ql[j] == ' ' {
			j++
			i--
			continue
		}
		if text[i] == ql[j] {
			if len(pos) > 0 && pos[len(pos)-1] != i-1 {
				gaps++
			}
			pos = append(pos, i)
			j++
		}
	}
	// Letters strewn over the whole title match nearly anything: keep it to
	// ones close together ("exrp" → "EXport RePorts").
	if j < len(ql) || len(ql) < 3 || pos[len(pos)-1]-pos[0] >= 3*len(ql) {
		return 0, nil, nil, false
	}
	split(pos)
	return 1000 - gaps*20 - pos[0], idHL, tHL, true
}

func span(i, n int) []int {
	out := make([]int, n)
	for k := range out {
		out[k] = i + k
	}
	return out
}

func runeIndex(s, sub []rune, from int) int {
	for i := from; i+len(sub) <= len(s); i++ {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

func isWordRune(r rune) bool {
	return r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r > 127
}
