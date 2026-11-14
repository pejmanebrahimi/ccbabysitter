package supervise

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// Before a fallback starts a background copy, Claude's own list is asked
// whether one is running already. A copy that is running is taken as the
// watch's own rather than resumed a second time; an empty list lets the
// resume go ahead, and a list that never came back changes nothing.
func TestFallbackAsksClaudeBeforeResuming(t *testing.T) {
	id := "3c3c3c3c-0000-4000-8000-000000000001"
	cases := []struct {
		name       string
		agents     string
		wantResume bool
		wantShort  string
		wantState  string
	}{
		{
			name:      "a background copy is running",
			agents:    `[{"pid":9,"id":"4d4d4d4d","kind":"background","status":"idle","sessionId":"` + id + `","name":"demo-a1"}]`,
			wantShort: "4d4d4d4d", wantState: "fallback",
		},
		{
			name:       "nothing is running",
			agents:     "[]",
			wantResume: true, wantShort: "5e5e5e5e", wantState: "fallback",
		},
		{
			name:      "claude does not answer",
			agents:    "error: the background service is not up",
			wantShort: "3c3c3c3c", wantState: "inplace",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, func(args []string) (string, error) {
				a := strings.Join(args, " ")
				switch {
				case a == "agents --json":
					return c.agents, nil
				case strings.HasPrefix(a, "--bg --resume"):
					return "backgrounded \u00b7 5e5e5e5e \u00b7 demo-a1", nil
				}
				return "", nil
			})
			f.d.SettleTimeout = time.Millisecond
			f.writeTranscript(t, "/home/dev/ws", id)
			s := New(*f.d)
			s.addWatchForTest(state.Watch{SessionID: id, ShortID: "3c3c3c3c", Name: "demo-a1", Cwd: "/home/dev/ws",
				OriginHost: claude.HostDesktop, PromiseState: "inplace", HasSavedOptions: true})

			at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
			for i := 1; i <= 2; i++ {
				s.reconcileForTest(observe.Snapshot{At: at.Add(time.Duration(i) * time.Second)})
			}

			if got := countCalls(f, "--bg --resume ") == 1; got != c.wantResume {
				t.Fatalf("resumed %v, want %v: %v", got, c.wantResume, f.r.CallList())
			}
			w, _ := s.watchForTest(id)
			if w.ShortID != c.wantShort || w.PromiseState != c.wantState {
				t.Fatalf("%+v", w)
			}
		})
	}
}

// A running copy that is taken as the watch's own is said once in the
// activity feed, in the words used for a session found running already,
// and not again on every pass that finds it the same way.
func TestAdoptingARunningCopyIsSaidOnce(t *testing.T) {
	id := "3c3c3c3c-0000-4000-8000-000000000002"
	f := newFixture(t, func(args []string) (string, error) {
		if strings.Join(args, " ") == "agents --json" {
			return `[{"pid":9,"id":"4d4d4d4d","kind":"background","status":"busy","sessionId":"` + id + `"}]`, nil
		}
		return "", nil
	})
	f.writeTranscript(t, "/home/dev/ws", id)
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "3c3c3c3c", Name: "demo-a2", Cwd: "/home/dev/ws",
		OriginHost: claude.HostVSCode, PromiseState: "fallback", HasSavedOptions: true})

	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 5; i++ {
		s.reconcileForTest(observe.Snapshot{At: at.Add(time.Duration(i) * time.Second)})
	}
	if n := countCalls(f, "--bg --resume "); n != 0 {
		t.Fatalf("a running copy is never resumed: %v", f.r.CallList())
	}
	n := 0
	for _, e := range f.d.Log.Recent(50, "") {
		if e.Message == "the session is already running in the background, nothing to resume" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("said %d times, want once", n)
	}
	if w, _ := s.watchForTest(id); w.ShortID != "4d4d4d4d" || w.PromiseState != "fallback" {
		t.Fatalf("%+v", w)
	}
}

// A background job keeps its short id when the conversation in it changes,
// as after /clear. When the job the watch knows runs another conversation,
// the watched one is not resumed beside it, which forks a copy every time:
// babysitting follows the job to the conversation it runs now, and Activity
// says so.
func TestAWatchFollowsItsJobToItsNewConversation(t *testing.T) {
	id := "3c3c3c3c-0000-4000-8000-000000000003"
	next := "99999999-0000-4000-8000-000000000003"
	f := newFixture(t, func(args []string) (string, error) {
		if strings.Join(args, " ") == "agents --json" {
			return `[{"pid":9,"id":"4d4d4d4d","kind":"background","status":"idle","sessionId":"` + next + `","name":"next one"}]`, nil
		}
		return "backgrounded \u00b7 5e5e5e5e", nil
	})
	f.writeTranscript(t, "/home/dev/ws", id)
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "4d4d4d4d", Name: "first one", Cwd: "/home/dev/ws",
		OriginHost: claude.HostBackground, PromiseState: "inplace", HasSavedOptions: true})
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 3; i++ {
		s.reconcileForTest(observe.Snapshot{At: at.Add(time.Duration(i) * time.Second)})
	}
	if n := countCalls(f, "--bg --resume "); n != 0 {
		t.Fatalf("the earlier conversation was resumed beside the job: %v", f.r.CallList())
	}
	if _, ok := s.watchForTest(id); ok {
		t.Fatal("the earlier conversation is still babysat")
	}
	w, ok := s.watchForTest(next)
	if !ok || w.ShortID != "4d4d4d4d" || w.Cwd != "/home/dev/ws" {
		t.Fatalf("babysitting did not follow the job: %+v %v", w, ok)
	}
	said := false
	for _, e := range f.d.Log.Recent(20, "") {
		if strings.Contains(e.Message, "now runs another conversation") {
			said = true
		}
	}
	if !said {
		t.Fatalf("Activity does not say the job moved on: %v", f.d.Log.Recent(20, ""))
	}
}

// When the conversation the job runs now is babysat already, the earlier
// conversation's watch is simply let go, with a line, and nothing is
// resumed.
func TestAWatchWhoseJobRunsAnotherBabysatConversationIsLetGo(t *testing.T) {
	id := "3c3c3c3c-0000-4000-8000-000000000004"
	next := "99999999-0000-4000-8000-000000000004"
	f := newFixture(t, func(args []string) (string, error) {
		if strings.Join(args, " ") == "agents --json" {
			return `[{"pid":9,"id":"4d4d4d4d","kind":"background","status":"idle","sessionId":"` + next + `"}]`, nil
		}
		return "backgrounded \u00b7 5e5e5e5e", nil
	})
	f.writeTranscript(t, "/home/dev/ws", id)
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "4d4d4d4d", Cwd: "/home/dev/ws",
		OriginHost: claude.HostBackground, PromiseState: "inplace", HasSavedOptions: true})
	s.addWatchForTest(state.Watch{SessionID: next, ShortID: "4d4d4d4d", Cwd: "/home/dev/ws",
		OriginHost: claude.HostBackground, PromiseState: "inplace", HasSavedOptions: true})
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	running := []claude.Session{{ID: next, ShortID: "4d4d4d4d", PID: 9, Host: claude.HostBackground, Cwd: "/home/dev/ws"}}
	for i := 1; i <= 3; i++ {
		s.reconcileForTest(observe.Snapshot{At: at.Add(time.Duration(i) * time.Second), Sessions: running})
	}
	if n := countCalls(f, "--bg --resume "); n != 0 {
		t.Fatalf("the earlier conversation was resumed beside the job: %v", f.r.CallList())
	}
	if _, ok := s.watchForTest(id); ok {
		t.Fatal("the earlier conversation is still babysat")
	}
	if _, ok := s.watchForTest(next); !ok {
		t.Fatal("the conversation the job runs is no longer babysat")
	}
}

// A resume that goes ahead says in one line what Claude's list held for
// the session or its short id, so a copy started beside another one can be
// traced to what was seen at the time.
func TestAResumeSaysWhatClaudeListed(t *testing.T) {
	id := "3c3c3c3c-0000-4000-8000-000000000004"
	cases := []struct{ agents, want string }{
		{"[]", "resuming: agents listed nothing for this session"},
		{`[{"pid":9,"id":"4d4d4d4d","kind":"background","status":"stopped","sessionId":"99999999-0000-4000-8000-000000000004"},` +
			`{"pid":8,"kind":"interactive","sessionId":"88888888-0000-4000-8000-000000000004"}]`,
			"resuming: agents listed 4d4d4d4d background stopped"},
	}
	for _, c := range cases {
		f := newFixture(t, func(args []string) (string, error) {
			a := strings.Join(args, " ")
			if a == "agents --json" {
				return c.agents, nil
			}
			if strings.HasPrefix(a, "--bg --resume") {
				return "backgrounded \u00b7 5e5e5e5e", nil
			}
			return "", nil
		})
		f.d.SettleTimeout = time.Millisecond
		f.writeTranscript(t, "/home/dev/ws", id)
		s := New(*f.d)
		s.addWatchForTest(state.Watch{SessionID: id, ShortID: "4d4d4d4d", Name: "demo-a4", Cwd: "/home/dev/ws",
			OriginHost: claude.HostDesktop, PromiseState: "inplace", HasSavedOptions: true})
		at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
		for i := 1; i <= 2; i++ {
			s.reconcileForTest(observe.Snapshot{At: at.Add(time.Duration(i) * time.Second)})
		}
		if n := countCalls(f, "--bg --resume "); n != 1 {
			t.Fatalf("%s: %v", c.agents, f.r.CallList())
		}
		// The line is for whoever reads log.txt, not for Activity.
		for _, e := range f.d.Log.Recent(50, "demo-a4") {
			if strings.HasPrefix(e.Message, "resuming: ") {
				t.Errorf("Activity has %q", e.Message)
			}
		}
		data, err := os.ReadFile(filepath.Join(f.logDir, "log.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(data), "demo-a4: resuming: "); n != 1 || !strings.Contains(string(data), "demo-a4: "+c.want+"\n") {
			t.Errorf("%s: log.txt says it %d times, want %q once:\n%s", c.agents, n, c.want, data)
		}
	}
}

// What Claude's list says is copied into the activity feed only as
// printable ASCII, each field capped, so a row cannot carry a control
// byte or an endless string into it.
func TestAgentsSeenKeepsOnlyPrintableText(t *testing.T) {
	id := "3c3c3c3c-0000-4000-8000-000000000005"
	list := []claude.AgentEntry{{PID: 9, ID: "4d4d4d4d\x1b[31m", Kind: "back\x00ground", Status: "id\x07le", SessionID: id}}
	if got := agentsSeen(list, id, ""); got != "agents listed 4d4d4d4d[31m background idle" {
		t.Fatalf("%q", got)
	}
	long := []claude.AgentEntry{{ID: strings.Repeat("a", 100), Kind: "background", SessionID: id}}
	if got := agentsSeen(long, id, ""); got != "agents listed "+strings.Repeat("a", 64)+" background" {
		t.Fatalf("%q", got)
	}
}
