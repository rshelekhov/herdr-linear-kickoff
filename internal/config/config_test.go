package config

import (
	"os"
	"reflect"
	"testing"

	"github.com/BurntSushi/toml"
)

// The commented sample written on first run must decode to the defaults.
func TestSampleMatchesDefault(t *testing.T) {
	var c Config
	md, err := toml.Decode(sample, &c)
	if err != nil {
		t.Fatal(err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		t.Fatalf("unknown keys in sample: %v", u)
	}
	d := Default()
	c.PromptTemplate = d.PromptTemplate // not in the sample
	if !reflect.DeepEqual(c, d) {
		t.Fatalf("sample != default\n got %+v\nwant %+v", c, d)
	}
}

func TestLoadWritesSample(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.BranchTemplate != "{linear}" || c.TypeMap["bug"] != "fix" || !c.StartAssignsMe {
		t.Fatalf("loaded %+v", c)
	}
}

// A [type_map] in the file is the whole map: a default entry can be dropped.
func TestTypeMapReplacesDefaults(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(Path(dir), []byte("[type_map]\nchore = \"chore\"\n"), 0o600)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.TypeMap["bug"]; ok || c.TypeMap["chore"] != "chore" {
		t.Errorf("type_map: %v", c.TypeMap)
	}
	os.WriteFile(Path(dir), []byte("agent_cmd = \"codex\"\n"), 0o600)
	if c, _ := Load(dir); c.TypeMap["bug"] != "fix" {
		t.Errorf("no [type_map]: defaults lost: %v", c.TypeMap)
	}
}
