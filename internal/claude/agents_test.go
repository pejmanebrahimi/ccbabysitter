package claude

import (
	"strings"
	"testing"
)

const agentsOut = `[
  {"pid": 120, "cwd": "C:\\a", "kind": "interactive", "startedAt": 1, "sessionId": "11111111-2222-4333-8444-555555555501", "name": "demo-a1"},
  {"pid": 121, "id": "22222222", "cwd": "C:\\ws", "kind": "background", "startedAt": 2, "sessionId": "22222222-2222-4333-8444-555555555502", "name": "demo-b2", "status": "idle", "state": "blocked"}
]`

// agentsOutWithBadRows adds a row with a non-numeric pid and a row with no
// sessionId to the two valid rows above; both bad rows must be skipped
// rather than failing the whole parse or appearing as zero-value entries.
const agentsOutWithBadRows = `[
  {"pid": 120, "cwd": "C:\\a", "kind": "interactive", "startedAt": 1, "sessionId": "11111111-2222-4333-8444-555555555501", "name": "demo-a1"},
  {"pid": 121, "id": "22222222", "cwd": "C:\\ws", "kind": "background", "startedAt": 2, "sessionId": "22222222-2222-4333-8444-555555555502", "name": "demo-b2", "status": "idle", "state": "blocked"},
  {"pid": "not-a-number", "cwd": "C:\\bad", "kind": "interactive", "sessionId": "88888888-9999-4aaa-8bbb-555555555508", "name": "demo-c3"},
  {"pid": 122, "cwd": "C:\\bad", "kind": "interactive", "sessionId": "", "name": "demo-d4"}
]`

func TestParseAgents(t *testing.T) {
	list, err := ParseAgents("Starting background service...\n" + agentsOut)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[1].ID != "22222222" || list[1].Status != "idle" || list[0].ID != "" {
		t.Fatalf("got %+v", list)
	}
	if l, err := ParseAgents("[]"); err != nil || len(l) != 0 {
		t.Fatalf("empty array is an answer: %v %v", l, err)
	}
}

func TestParseAgentsSkipsBadRows(t *testing.T) {
	list, err := ParseAgents(agentsOutWithBadRows)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "demo-a1" || list[1].Name != "demo-b2" {
		t.Fatalf("got %+v, want only the two valid rows", list)
	}
}

func TestParseAgentsNotAnAnswer(t *testing.T) {
	for _, in := range []string{"", "error: could not start background service", "[timeout after 20s] partial", "{\"not\":\"array\"}"} {
		if _, err := ParseAgents(in); err == nil {
			t.Fatalf("expected error for %q: an unanswered check must never look like an empty list", in)
		}
	}
}

// Only a background row that is running counts as a copy of a session:
// a row for another session, a session open in an app, and a background
// row that has stopped do not.
func TestRunningCopy(t *testing.T) {
	id := "22222222-2222-4333-8444-555555555502"
	cases := []struct {
		name string
		row  AgentEntry
		want bool
	}{
		{"idle background", AgentEntry{PID: 9, ID: "4d4d4d4d", SessionID: id, Kind: "background", Status: "idle"}, true},
		{"busy background", AgentEntry{PID: 9, ID: "4d4d4d4d", SessionID: id, Kind: "background", Status: "busy"}, true},
		{"no status", AgentEntry{PID: 9, ID: "4d4d4d4d", SessionID: id, Kind: "background"}, true},
		{"stopped", AgentEntry{PID: 9, ID: "4d4d4d4d", SessionID: id, Kind: "background", Status: "stopped"}, false},
		{"no process", AgentEntry{ID: "4d4d4d4d", SessionID: id, Kind: "background", Status: "idle"}, false},
		{"interactive", AgentEntry{PID: 9, SessionID: id, Kind: "interactive", Status: "idle"}, false},
		{"another session", AgentEntry{PID: 9, ID: "4d4d4d4d", SessionID: "11111111-2222-4333-8444-555555555501", Kind: "background"}, false},
	}
	for _, c := range cases {
		row, ok := RunningCopy([]AgentEntry{c.row}, id)
		if ok != c.want || (ok && row != c.row) {
			t.Errorf("%s: got %+v %v, want %v", c.name, row, ok, c.want)
		}
	}
	// A job that runs another session is that session's, even under the
	// short id the watch knew: a background job keeps its id when the
	// conversation in it changes.
	other := AgentEntry{PID: 9, ID: "4D4D4D4D", SessionID: "11111111-2222-4333-8444-555555555501", Kind: "background", Status: "idle"}
	if _, ok := RunningCopy([]AgentEntry{other}, id); ok {
		t.Error("a job that runs another session is not the watch's copy")
	}
	if Listed(nil, id) || !Listed([]AgentEntry{{SessionID: id, Kind: "interactive"}}, id) {
		t.Error("Listed reports whether any row names the session")
	}
}

// JobOnAnother finds the job a watch knew, by its short id in any case,
// when it runs another conversation, and not when it runs the watch's own,
// however the session id is written.
func TestJobOnAnother(t *testing.T) {
	id := "22222222-2222-4333-8444-5555555555bb"
	other := "33333333-2222-4333-8444-5555555555cc"
	row := AgentEntry{PID: 9, ID: "4D4D4D4D", SessionID: other, Kind: "background", Status: "idle"}
	if got, ok := JobOnAnother([]AgentEntry{row}, id, "4d4d4d4d"); !ok || got != row {
		t.Fatal("the job runs another conversation")
	}
	same := row
	same.SessionID = strings.ToUpper(id)
	if _, ok := JobOnAnother([]AgentEntry{same}, id, "4d4d4d4d"); ok {
		t.Fatal("the job runs the watch's own conversation, written in upper case")
	}
	if _, ok := JobOnAnother([]AgentEntry{row}, id, ""); ok {
		t.Fatal("no short id finds nothing")
	}
}
