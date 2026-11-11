package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/rshelekhov/herdr-linear-kickoff/internal/linear"
)

func statusModel(t *testing.T) model {
	m := listModel(t)
	m.client = linear.New("k")
	for i := range m.issues {
		m.issues[i].Team.ID = "t1"
	}
	m.teamStates["t1"] = []linear.State{
		{ID: "s-todo", Name: "Todo", Type: "unstarted"},
		{ID: "s-prog", Name: "In Progress", Type: "started"},
		{ID: "s-rev", Name: "In Review", Type: "started"},
		{ID: "s-done", Name: "Done", Type: "completed"},
	}
	cur := &m.issues[m.visible[m.cursor]]
	cur.ID, cur.State = "uuid-964", linear.State{ID: "s-prog", Name: "In Progress", Type: "started"}
	return m
}

func TestStatusChange(t *testing.T) {
	m := statusModel(t)
	m = press(m, "s")
	if m.screen != scStatus || m.stCur != 1 {
		t.Fatalf("screen %v, cursor %d (want on the current status)", m.screen, m.stCur)
	}
	v := m.View()
	for _, s := range []string{"ABC-964 · status", "Todo", "In Review", "Done", "✓"} {
		if !strings.Contains(v, s) {
			t.Errorf("picker lacks %q", s)
		}
	}
	// Same status: nothing sent.
	if mm, _ := m.Update(key("enter")); mm.(model).stBusy || mm.(model).screen != scList {
		t.Error("choosing the current status sent a change")
	}
	m = press(statusModel(t), "s", "down")
	mm, cmd := m.Update(key("enter"))
	m = mm.(model)
	if cmd == nil || !m.stBusy || !strings.Contains(m.View(), "saving…") {
		t.Fatal("no update sent")
	}
	// Linear refuses: stay open with the error.
	mm, _ = m.Update(statusSetMsg{"ABC-964", "uuid-964", m.teamStates["t1"][2], errors.New("nope")})
	m = mm.(model)
	if m.screen != scStatus || !strings.Contains(m.View(), "nope") {
		t.Fatalf("error not shown:\n%s", m.View())
	}
	// Linear accepts: applied, noted, lists reloaded.
	m.fetched["team"] = true
	mm, cmd = m.Update(statusSetMsg{"ABC-964", "uuid-964", m.teamStates["t1"][2], nil})
	m = mm.(model)
	cur, _ := m.current()
	if m.screen != scList || cur.State.Name != "In Review" || cmd == nil {
		t.Errorf("not applied: screen %v state %q", m.screen, cur.State.Name)
	}
	if !strings.Contains(m.View(), "ABC-964 → In Review") || len(m.fetched) != 0 {
		t.Errorf("no note or tabs not marked stale: fetched %v", m.fetched)
	}
}

func TestStatusLoadsStates(t *testing.T) {
	m := statusModel(t)
	delete(m.teamStates, "t1")
	mm, cmd := m.Update(key("s"))
	m = mm.(model)
	if cmd == nil || !strings.Contains(m.View(), "loading statuses") {
		t.Fatal("states not requested")
	}
	mm, _ = m.Update(statesMsg{"t1", []linear.State{{ID: "a", Name: "Todo"}, {ID: "s-prog", Name: "In Progress"}}, nil})
	if mm.(model).stCur != 1 {
		t.Error("cursor not on the current status after loading")
	}
}

func TestStatusLinearOrder(t *testing.T) {
	states := []linear.State{
		{ID: "tr", Name: "Triage", Type: "triage", Position: 0},
		{ID: "dn", Name: "Done", Type: "completed", Position: 5},
		{ID: "ip", Name: "In Progress", Type: "started", Position: 2},
		{ID: "dup", Name: "Duplicate", Type: "canceled", Position: 7},
		{ID: "bl", Name: "Backlog", Type: "backlog", Position: 1},
		{ID: "ca", Name: "Canceled", Type: "canceled", Position: 6},
		{ID: "td", Name: "Todo", Type: "unstarted", Position: 1.5},
		{ID: "ir", Name: "In Review", Type: "started", Position: 3},
	}
	linear.SortWorkflow(states)
	var names []string
	for _, s := range states {
		names = append(names, s.Name)
	}
	want := "Backlog Todo In Progress In Review Done Canceled Duplicate Triage"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("order:\n got %s\nwant %s", got, want)
	}
	keys := stateKeys(states)
	if keys["bl"] != "1" || keys["ip"] != "3" || keys["dup"] != "7" || keys["tr"] != "0" {
		t.Errorf("keys: %v", keys)
	}
}

func TestStatusNumberAndSearch(t *testing.T) {
	m := statusModel(t)
	m = press(m, "s")
	mm, cmd := m.Update(key("3")) // In Review in the test team's order
	if !mm.(model).stBusy || cmd == nil {
		t.Fatal("3 didn't set a status")
	}
	m = press(statusModel(t), "s")
	m = typeText(m, "rev")
	if vis := m.stVisible(); len(vis) != 1 || vis[0].Name != "In Review" || m.stCur != 0 {
		t.Fatalf("search rev: %+v cur %d", vis, m.stCur)
	}
	if mm, _ := m.Update(key("enter")); !mm.(model).stBusy {
		t.Error("enter on the match didn't set it")
	}
}

// A change answered after the dialog moved to another issue applies to the
// issue it was for, and leaves the other dialog open.
func TestStatusAnswerForAnotherIssue(t *testing.T) {
	m := statusModel(t)
	m = press(m, "s", "down")
	mm, _ := m.Update(key("enter")) // ABC-964 → In Review, in flight
	m = press(mm.(model), "esc", "down", "s")
	other := m.stIssue.Identifier
	if m.screen != scStatus || other == "ABC-964" {
		t.Fatalf("dialog not on another issue: %q", other)
	}
	mm, _ = m.Update(statusSetMsg{"ABC-964", "uuid-964", m.teamStates["t1"][2], nil})
	m = mm.(model)
	if m.screen != scStatus || m.stIssue.Identifier != other {
		t.Errorf("the other issue's dialog closed: screen %v on %q", m.screen, m.stIssue.Identifier)
	}
	for _, is := range m.issues {
		if is.Identifier == "ABC-964" && is.State.Name != "In Review" {
			t.Errorf("ABC-964 not updated: %q", is.State.Name)
		}
		if is.Identifier == other && is.State.Name == "In Review" {
			t.Error("the other issue got the new status")
		}
	}
}
