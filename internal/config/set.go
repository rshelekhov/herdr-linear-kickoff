package config

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

var reTable = regexp.MustCompile(`^\s*\[`)

// Set changes one top-level key in config.toml, keeping the rest of the file
// (comments included) as it is. A key that isn't there is added above the
// first [table]: in TOML everything below a table header belongs to it.
func Set(dir, key string, value any) error {
	line, err := encodeKey(key, value)
	if err != nil {
		return err
	}
	p := Path(dir)
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		b, err = []byte(sample), nil
	}
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	reKey := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `\s*=`)
	firstTable := len(lines)
	for i, l := range lines {
		if reTable.MatchString(l) {
			firstTable = i
			break
		}
	}
	done := false
	for i := 0; i < firstTable; i++ {
		if reKey.MatchString(lines[i]) {
			lines[i], done = line, true
			break
		}
	}
	if !done {
		at := firstTable
		for at > 0 && strings.HasPrefix(strings.TrimSpace(lines[at-1]), "#") {
			at-- // above the comments that introduce the table
		}
		lines = append(lines[:at], append([]string{line, ""}, lines[at:]...)...)
	}
	out := strings.Join(lines, "\n")
	var check Config // never write a file Load would reject
	if _, err := toml.Decode(out, &check); err != nil {
		return fmt.Errorf("config.toml would not parse: %w", err)
	}
	tmp := p + ".tmp"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, []byte(out), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// encodeKey renders `key = value` the way TOML wants it.
func encodeKey(key string, value any) (string, error) {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(map[string]any{key: value}); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}
