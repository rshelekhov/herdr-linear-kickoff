// Package branch renders branch names from a template.
package branch

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Vars are the values a template can use.
type Vars struct {
	Linear     string // Linear's suggested branchName
	Identifier string // ABC-964
	Team       string // ABC
	Number     int    // 964
	Title      string
	Type       string // feat, fix, …
	Me         string // Linear username
	SlugMax    int
}

var cyrillic = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e", 'ж': "zh",
	'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o",
	'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u", 'ф': "f", 'х': "kh", 'ц': "ts",
	'ч': "ch", 'ш': "sh", 'щ': "shch", 'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu",
	'я': "ya", 'і': "i", 'ї': "yi", 'є': "ye", 'ґ': "g",
}

// Slug makes a lowercase, dash-separated ASCII slug of at most max chars,
// cut at a dash so words stay whole. Cyrillic is transliterated.
func Slug(s string, max int) string {
	var b strings.Builder
	dash := false
	sep := func() {
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	for _, r := range strings.ToLower(s) {
		if t, ok := cyrillic[r]; ok {
			b.WriteString(t)
			dash = false
			continue
		}
		wrote := false
		for _, d := range norm.NFKD.String(string(r)) {
			if d < unicode.MaxASCII && (unicode.IsLetter(d) || unicode.IsDigit(d)) {
				b.WriteRune(d)
				wrote = true
			}
		}
		if wrote {
			dash = false
		} else if !unicode.Is(unicode.Mn, r) {
			sep()
		}
	}
	out := strings.Trim(b.String(), "-")
	if max > 0 && len(out) > max {
		out = out[:max]
		if i := strings.LastIndexByte(out, '-'); i > max/2 {
			out = out[:i]
		}
		out = strings.Trim(out, "-")
	}
	return out
}

var (
	multiSlash = regexp.MustCompile(`/+`)
	multiDash  = regexp.MustCompile(`-{2,}`)
	badRef     = regexp.MustCompile(`[^A-Za-z0-9._/\-]+`)
)

// Render fills the template. Unknown placeholders are left as is and then
// cleaned out, and the result is made a valid git ref.
func Render(tmpl string, v Vars) string {
	linear := v.Linear
	if linear == "" {
		linear = strings.ToLower(v.Identifier)
		if s := Slug(v.Title, v.SlugMax); s != "" {
			linear += "-" + s
		}
	}
	r := strings.NewReplacer(
		"{linear}", linear,
		"{id}", strings.ToLower(v.Identifier),
		"{ID}", strings.ToUpper(v.Identifier),
		"{team}", strings.ToLower(v.Team),
		"{TEAM}", strings.ToUpper(v.Team),
		"{number}", strconv.Itoa(v.Number),
		"{slug}", Slug(v.Title, v.SlugMax),
		"{type}", v.Type,
		"{me}", Slug(v.Me, 0),
	)
	return Clean(r.Replace(tmpl))
}

// Clean turns s into something git accepts as a branch name.
func Clean(s string) string {
	s = badRef.ReplaceAllString(s, "-")
	s = multiSlash.ReplaceAllString(s, "/")
	s = multiDash.ReplaceAllString(s, "-")
	s = strings.ReplaceAll(s, "..", ".")
	s = strings.ReplaceAll(s, "/-", "/")
	s = strings.ReplaceAll(s, "-/", "/")
	s = strings.Trim(s, "-./")
	s = strings.TrimSuffix(s, ".lock")
	return s
}

// TypeFor picks {type} from the issue's labels via the map, else def.
func TypeFor(labels []string, m map[string]string, def string) string {
	for _, l := range labels {
		for k, v := range m {
			if strings.EqualFold(strings.TrimSpace(l), k) {
				return v
			}
		}
	}
	return def
}

var reIssueToken = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])([a-z][a-z0-9]*)-([0-9]+)`)

// Issues are the issues a branch is for: per team key, the first identifier
// of that team in it ("feat/abc-964-x" → ABC-964). A later one is only
// mentioned: "abc-970-follow-up-to-abc-964" is ABC-970's branch, not ABC-964's.
func Issues(branch string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range reIssueToken.FindAllStringSubmatch(branch, -1) {
		team := strings.ToUpper(m[1])
		if seen[team] {
			continue
		}
		seen[team] = true
		num := strings.TrimLeft(m[2], "0")
		if num == "" {
			num = "0"
		}
		out = append(out, team+"-"+num)
	}
	return out
}

// MentionsIssue reports whether branch is identifier's branch, as in Issues
// ("me/abc-964-x" is ABC-964's; "abc-9640" and "abc-970-to-abc-964" are not).
func MentionsIssue(branch, identifier string) bool {
	id := strings.ToUpper(identifier)
	for _, b := range Issues(branch) {
		if b == id {
			return true
		}
	}
	return false
}
