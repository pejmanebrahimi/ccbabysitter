package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTranscriptAt(t *testing.T, projects, folder, id, body string, mod time.Time) {
	t.Helper()
	dir := filepath.Join(projects, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestListPastFindsRecentTranscriptsNewestFirst(t *testing.T) {
	projects := t.TempDir()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	const (
		a    = "11111111-2222-4333-8444-555555555501"
		b    = "22222222-2222-4333-8444-555555555502"
		old  = "33333333-2222-4333-8444-555555555503"
		live = "44444444-2222-4333-8444-555555555504"
	)
	writeTranscriptAt(t, projects, "-home-dev-ws", a,
		`{"type":"user","cwd":"/home/dev/ws","message":{"content":"hi"}}`+"\n"+
			`{"type":"custom-title","customTitle":"demo-a1","sessionId":"`+a+`"}`+"\n", now.Add(-time.Hour))
	writeTranscriptAt(t, projects, "-home-dev-api", b,
		`{"type":"user","cwd":"/home/dev/api","message":{"content":"hi"}}`+"\n", now.Add(-time.Minute))
	writeTranscriptAt(t, projects, "-home-dev-ws", old, `{"type":"user","cwd":"/home/dev/ws"}`+"\n", now.Add(-15*24*time.Hour))
	writeTranscriptAt(t, projects, "-home-dev-ws", live, `{"type":"user","cwd":"/home/dev/ws"}`+"\n", now.Add(-time.Minute))
	writeTranscriptAt(t, projects, "-home-dev-ws", "not-an-id", `{}`+"\n", now)
	if err := os.MkdirAll(filepath.Join(projects, "-home-dev-ws", a, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := ListPast(projects, now.Add(-14*24*time.Hour), 30, map[string]bool{live: true})
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if got[0].ID != b || got[0].Cwd != "/home/dev/api" || got[0].Name != "" {
		t.Fatalf("newest first: %+v", got[0])
	}
	if got[1].ID != a || got[1].Cwd != "/home/dev/ws" || got[1].Name != "demo-a1" {
		t.Fatalf("%+v", got[1])
	}
	if !got[1].LastActivity.Equal(now.Add(-time.Hour)) {
		t.Fatalf("last activity is when the file was last written: %v", got[1].LastActivity)
	}
	// b and live were written at the same moment; the tie goes to the lower
	// id, so the order never depends on how the folder happened to list.
	if capped := ListPast(projects, now.Add(-14*24*time.Hour), 1, nil); len(capped) != 1 || capped[0].ID != b {
		t.Fatalf("at most max, newest first: %+v", capped)
	}
}

// A transcript with no conversation in it, an empty file or one with no
// message, has nothing to resume, so it is left out, and it does not take
// a place from one that does.
func TestListPastLeavesOutConversationsWithNoMessage(t *testing.T) {
	projects := t.TempDir()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	const (
		real   = "11111111-2222-4333-8444-555555555511"
		empty  = "22222222-2222-4333-8444-555555555512"
		noMsg  = "33333333-2222-4333-8444-555555555513"
		answer = "44444444-2222-4333-8444-555555555514"
	)
	writeTranscriptAt(t, projects, "-home-dev-ws", real,
		`{"type":"user","cwd":"/home/dev/ws","message":{"content":"hi"}}`+"\n", now.Add(-time.Hour))
	writeTranscriptAt(t, projects, "-home-dev-ws", empty, "", now.Add(-time.Minute))
	writeTranscriptAt(t, projects, "-home-dev-ws", noMsg,
		`{"type":"file-history-snapshot","messageId":"x"}`+"\n", now.Add(-2*time.Minute))
	writeTranscriptAt(t, projects, "-home-dev-api", answer,
		`{"type":"assistant","cwd":"/home/dev/api","message":{"content":"hello"}}`+"\n", now.Add(-2*time.Hour))

	got := ListPast(projects, now.Add(-14*24*time.Hour), 30, nil)
	if len(got) != 2 || got[0].ID != real || got[1].ID != answer {
		t.Fatalf("want only the two with a message: %+v", got)
	}
	if capped := ListPast(projects, now.Add(-14*24*time.Hour), 1, nil); len(capped) != 1 || capped[0].ID != real {
		t.Fatalf("an empty one takes no place: %+v", capped)
	}
}

// A name set late in a long conversation is still found, without the whole
// file being read.
func TestListPastReadsTheNameFromTheEnd(t *testing.T) {
	projects := t.TempDir()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	const id = "55555555-2222-4333-8444-555555555505"
	filler := `{"type":"assistant","message":{"content":"` + strings.Repeat("x", 1000) + `"}}` + "\n"
	body := `{"type":"user","cwd":"/home/dev/ws"}` + "\n" + strings.Repeat(filler, 300) +
		`{"type":"custom-title","customTitle":"demo-b2"}` + "\n"
	writeTranscriptAt(t, projects, "-home-dev-ws", id, body, now)
	got := ListPast(projects, now.Add(-time.Hour), 30, nil)
	if len(got) != 1 || got[0].Name != "demo-b2" || got[0].Cwd != "/home/dev/ws" {
		t.Fatalf("%+v", got)
	}
}

func TestListPastWithoutAProjectsFolderIsEmpty(t *testing.T) {
	if got := ListPast(filepath.Join(t.TempDir(), "missing"), time.Time{}, 30, nil); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

// A conversation that is not running is named by the same rules as a live
// one: the sidecar title, else a name a person gave, else the AI title,
// never the automatic name Remote Control gives.
func TestListPastNamesByTheSameRulesAsALiveSession(t *testing.T) {
	projects := t.TempDir()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	const (
		ai   = "66666666-2222-4333-8444-555555555506"
		side = "77777777-2222-4333-8444-555555555507"
		last = "88888888-2222-4333-8444-555555555508"
	)
	writeTranscriptAt(t, projects, "-home-dev-ws", ai, `{"type":"user","cwd":"/home/dev/ws"}`+"\n"+
		aiTitleLine("Hooora test")+customTitleLine("my-app-2-94")+agentNameLine("my-app-2-94"), now)
	writeTranscriptAt(t, projects, "-home-dev-ws", side, `{"type":"user","cwd":"/home/dev/ws"}`+"\n"+
		customTitleLine("Checkout fixes"), now.Add(-time.Minute))
	if err := os.MkdirAll(filepath.Join(projects, "-home-dev-ws", side), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projects, "-home-dev-ws", side, "custom-title.json"), []byte(`{"customTitle":"Panel name"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTranscriptAt(t, projects, "-home-dev-ws", last, `{"type":"user","cwd":"/home/dev/ws"}`+"\n"+
		aiTitleLine("Hooora test")+customTitleLine("Checkout fixes"), now.Add(-2*time.Minute))

	got := ListPast(projects, now.Add(-time.Hour), 30, nil)
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	if got[0].Name != "Hooora test" || got[1].Name != "Panel name" || got[2].Name != "Checkout fixes" {
		t.Fatalf("%q %q %q", got[0].Name, got[1].Name, got[2].Name)
	}
}

// A custom-title that ends the head window has its next record somewhere
// unread, so the first record of the tail window says nothing about it.
func TestListPastDoesNotSettleAHeadTitleFromTheTail(t *testing.T) {
	projects := t.TempDir()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	const id = "99999999-2222-4333-8444-555555555509"
	pad := func(n int) string {
		const open, end = `{"type":"assistant","message":{"content":"`, `"}}` + "\n"
		return open + strings.Repeat("x", n-len(open)-len(end)) + end
	}
	start := `{"type":"user","cwd":"/home/dev/ws"}` + "\n" + aiTitleLine("Hooora test")
	custom := customTitleLine("my-app-2-94")
	// The head window ends ten bytes into the agent-name line, so the
	// custom-title is the last whole record it holds.
	head := start + pad(pastEdgeBytes-10-len(start)-len(custom)) + custom
	body := head + agentNameLine("my-app-2-94") + strings.Repeat(pad(1000), 200) + promptLine
	if len(head) != pastEdgeBytes-10 {
		t.Fatalf("head is %d bytes", len(head))
	}
	writeTranscriptAt(t, projects, "-home-dev-ws", id, body, now)
	got := ListPast(projects, now.Add(-time.Hour), 30, nil)
	if len(got) != 1 || got[0].Name != "Hooora test" {
		t.Fatalf("%+v", got)
	}
}
