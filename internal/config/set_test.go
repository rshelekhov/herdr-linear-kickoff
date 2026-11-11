package config

import (
	"os"
	"strings"
	"testing"
)

func TestSetKeepsComments(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); err != nil { // writes the commented sample
		t.Fatal(err)
	}
	if err := Set(dir, "agent_cmd", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := Set(dir, "ask_type", true); err != nil {
		t.Fatal(err)
	}
	if err := Set(dir, "branch_template", `{type}/{id}-"{slug}"`); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path(dir))
	s := string(b)
	if !strings.Contains(s, "# Branch name. Placeholders:") || strings.Count(s, "agent_cmd") != 1 {
		t.Errorf("comments lost or key duplicated:\n%s", s)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.AgentCmd != "codex" || !c.AskType || c.BranchTemplate != `{type}/{id}-"{slug}"` {
		t.Errorf("loaded %+v", c)
	}
	if c.TypeMap["bug"] != "fix" { // the tables below are untouched
		t.Errorf("type_map lost: %v", c.TypeMap)
	}
}

// A key missing from the file goes above the tables, not into one.
func TestSetAddsAboveTables(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(Path(dir), []byte("agent_cmd = \"claude\"\n\n# labels to types\n[type_map]\nbug = \"fix\"\n"), 0o600)
	if err := Set(dir, "labels", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path(dir))
	s := string(b)
	if strings.Index(s, "labels = false") > strings.Index(s, "# labels to types") {
		t.Errorf("added below the table:\n%s", s)
	}
	c, _ := Load(dir)
	if c.Labels || c.TypeMap["bug"] != "fix" {
		t.Errorf("loaded %+v", c)
	}
}
