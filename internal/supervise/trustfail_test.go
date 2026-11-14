package supervise

import (
	"errors"
	"strings"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// A session stuck because Claude Code did not trust its folder comes back
// by itself once the folder is trusted, as when the person answered Yes in
// a terminal, without waiting for Try again, and Activity says so.
func TestAStuckSessionComesBackOnceItsFolderIsTrusted(t *testing.T) {
	id := "5b5b5b5b-0000-4000-8000-000000000002"
	sf := newStartFixture(t, false, func(args []string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case a == "agents --json":
			return "[]", nil
		case strings.HasPrefix(a, "--bg --resume"):
			return "backgrounded \u00b7 5b5b5b5b \u00b7 demo-a3", nil
		}
		return "", nil
	})
	sf.f.writeTranscript(t, sf.project, id)
	sf.s.addWatchForTest(state.Watch{SessionID: id, ShortID: "5b5b5b5b", Name: "demo-a3", Cwd: sf.project,
		OriginHost: claude.HostDesktop, PromiseState: "inplace", HasSavedOptions: true,
		Paused: true, PauseReason: UntrustedPause + ": Resume did not start: Workspace not trusted."})
	at := time.Now()
	sf.s.reconcileForTest(observe.Snapshot{At: at.Add(time.Second)})
	if n := countCalls(sf.f, "--bg --resume "); n != 0 {
		t.Fatalf("resumed before the folder was trusted: %v", sf.f.r.CallList())
	}
	sf.trust(t, sf.project)
	sf.s.deps.Now = func() time.Time { return time.Now().Add(time.Minute) }
	for i := 2; i <= 5; i++ {
		sf.s.reconcileForTest(observe.Snapshot{At: at.Add(time.Duration(i) * time.Second)})
	}
	if n := countCalls(sf.f, "--bg --resume "); n == 0 {
		t.Fatalf("not brought back once trusted: %v", sf.f.r.CallList())
	}
	said := false
	for _, e := range sf.f.d.Log.Recent(30, "") {
		if e.Automatic && e.Reason == "its folder is trusted now" {
			said = true
		}
	}
	if !said {
		t.Fatalf("Activity does not say why it was brought back: %v", sf.f.d.Log.Recent(30, ""))
	}
}

// A folder the claude CLI does not trust does not become trusted a few
// seconds later, so the first "Workspace not trusted" answer makes the
// session stuck at once, without two more tries, and Activity says the
// session is not running anywhere and what it needs.
func TestAnUntrustedFolderStopsTheRescueAtOnce(t *testing.T) {
	id := "5b5b5b5b-0000-4000-8000-000000000001"
	f := newFixture(t, func(args []string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case a == "agents --json":
			return "[]", nil
		case strings.HasPrefix(a, "--bg --resume"):
			return "Workspace not trusted. Run `claude` in /home/dev/ws once and accept the trust prompt, then retry.", errors.New("exit status 1")
		}
		return "", nil
	})
	f.writeTranscript(t, "/home/dev/ws", id)
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "5b5b5b5b", Name: "demo-a3", Cwd: "/home/dev/ws",
		OriginHost: claude.HostDesktop, PromiseState: "inplace", HasSavedOptions: true})
	at := time.Now()
	for i := 1; i <= 40; i++ {
		s.reconcileForTest(observe.Snapshot{At: at.Add(time.Duration(i) * time.Second)})
	}
	if n := countCalls(f, "--bg --resume "); n != 1 {
		t.Fatalf("want one try, got %d: %v", n, f.r.CallList())
	}
	w, ok := s.watchForTest(id)
	if !ok || !w.Paused || !strings.Contains(w.PauseReason, "does not trust") {
		t.Fatalf("not stuck with the trust reason: %+v %v", w, ok)
	}
	said := false
	for _, e := range f.d.Log.Recent(20, "") {
		if e.Automatic && strings.Contains(e.Message, "not running anywhere") {
			said = true
		}
	}
	if !said {
		t.Fatalf("Activity does not say the session is not running anywhere: %v", f.d.Log.Recent(20, ""))
	}
}

// On a machine with no display, TrustFolder answers Claude Code's question
// whether to trust the folder with Yes, as New session does, and a session
// stuck there for it comes back.
func TestTrustFolderOnAServerAnswersTheQuestion(t *testing.T) {
	id := "5b5b5b5b-0000-4000-8000-000000000003"
	sf := newStartFixture(t, true, func(args []string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case a == "agents --json":
			return "[]", nil
		case strings.HasPrefix(a, "--bg --resume"):
			return "backgrounded \u00b7 5b5b5b5b", nil
		}
		return "", nil
	})
	sf.f.writeTranscript(t, sf.project, id)
	sf.s.addWatchForTest(state.Watch{SessionID: id, ShortID: "5b5b5b5b", Cwd: sf.project,
		OriginHost: claude.HostBackground, PromiseState: "inplace", HasSavedOptions: true,
		Paused: true, PauseReason: UntrustedPause + ": Workspace not trusted."})
	opened := ""
	sf.s.deps.OpenTrustTerminal = func(dir string) (string, error) { opened = dir; return "Terminal", nil }
	res := sf.s.TrustFolder(sf.project, ViaPage)
	if !res.OK || len(sf.accept) != 1 || sf.accept[0] != sf.project || opened != "" {
		t.Fatalf("%+v, answered %v, opened %q", res, sf.accept, opened)
	}
	sf.s.reconcileForTest(observe.Snapshot{At: time.Now().Add(time.Second)})
	if n := countCalls(sf.f, "--bg --resume "); n == 0 {
		t.Fatalf("the stuck session was not brought back: %v", sf.f.r.CallList())
	}
}

// On a machine with a display, TrustFolder opens a terminal in the folder
// with Claude Code running, for the person to answer, and says what to do.
func TestTrustFolderWithADisplayOpensATerminal(t *testing.T) {
	sf := newStartFixture(t, false, nil)
	opened := ""
	sf.s.deps.OpenTrustTerminal = func(dir string) (string, error) { opened = dir; return "Terminal", nil }
	res := sf.s.TrustFolder(sf.project, ViaPage)
	if !res.OK || opened != sf.project || len(sf.accept) != 0 || !strings.Contains(res.Message, "Answer Yes") {
		t.Fatalf("%+v, opened %q, answered %v", res, opened, sf.accept)
	}
}

// A folder trusted already needs nothing, and the home folder is refused.
func TestTrustFolderRefusesOrSkips(t *testing.T) {
	sf := newStartFixture(t, true, nil)
	sf.trust(t, sf.project)
	if res := sf.s.TrustFolder(sf.project, ViaPage); !res.OK || len(sf.accept) != 0 || !strings.Contains(res.Message, "already") {
		t.Fatalf("trusted already: %+v %v", res, sf.accept)
	}
	sf.f.d.Home = sf.project
	sf.s.deps.Home = sf.project
	if res := sf.s.TrustFolder(sf.project, ViaPage); res.OK {
		t.Fatalf("the home folder was trusted: %+v", res)
	}
}
