package workspace

import "testing"

func TestRegistry(t *testing.T) {
	dir := t.TempDir()
	r := Load(dir)
	if r.For("/r/web") != "" {
		t.Fatal("a workspace out of nothing")
	}
	r.Add(WS{ID: "o2", Name: "Zeta", URLKey: "zeta"})
	r.Add(WS{ID: "o1", Name: "acme", URLKey: "acme"})
	r.Add(WS{ID: "o2", Name: "Zeta Inc", URLKey: "zeta"}) // renamed: refreshed, not doubled
	if len(r.List) != 2 || r.List[0].ID != "o1" || r.List[1].Name != "Zeta Inc" {
		t.Fatalf("list %+v", r.List)
	}
	if r.For("/r/web") != "o1" {
		t.Error("no history: the first one")
	}
	r.Use("/r/api", "o2")
	if r.For("/r/web") != "o2" || r.For("/r/api") != "o2" {
		t.Error("last used anywhere")
	}
	r.Use("/r/web", "o1")
	if r.For("/r/api") != "o2" || r.For("/r/web") != "o1" || r.For("/r/other") != "o1" {
		t.Error("per repo")
	}
	if w, ok := r.ByURLKey("ZETA"); !ok || w.ID != "o2" {
		t.Error("by URL key")
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	r = Load(dir)
	if len(r.List) != 2 || r.For("/r/api") != "o2" {
		t.Fatalf("reloaded %+v", r)
	}
	r.Remove("o2")
	if _, ok := r.Get("o2"); ok || r.Repos["/r/api"] != "" || r.For("/r/api") != "o1" {
		t.Errorf("removed %+v", r)
	}
}
