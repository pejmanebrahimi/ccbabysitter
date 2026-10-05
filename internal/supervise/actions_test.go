package supervise

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// headless makes the fixture a machine with no display.
func headless(f *fixture) {
	f.d.Env = func() hosts.Env {
		return hosts.Env{Platform: "linux", Headless: true, CLIFound: true, CLIPresent: true}
	}
}

// Stopping a babysat session carried by our copy runs `claude stop` and
// nothing else, removes the watch before the copy goes, and says where to
// find the session again.
func TestStopStopsTheCopyAndRemovesTheWatch(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555590"
	f := newFixture(t, nil)
	var mu sync.Mutex
	removedFirst := false
	f.r.SetRespond(func(args []string) (string, error) {
		if strings.HasPrefix(strings.Join(args, " "), "stop ") {
			st, ok := f.d.Store.Peek()
			mu.Lock()
			removedFirst = ok && st.Find(id) == nil
			mu.Unlock()
			f.p.SetAlive(9, false)
		}
		return "", nil
	})
	f.p.SetAlive(9, true)
	f.setFiles(bgSession(9, id, "b"))
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "11111111", Name: "demo-a1", Cwd: "/home/dev/ws",
		OriginHost: claude.HostDesktop, PromiseState: "fallback", HasSavedOptions: true})
	if w, _ := firstWatch(s); !w.CanStop || w.State != StateInBackground {
		t.Fatalf("an In background watch offers Stop: %+v", w)
	}

	res := s.Stop(id, ViaPage)
	if !res.OK {
		t.Fatal(res.Message)
	}
	if res.Message != "Stopped. Open it again any time from Not running." {
		t.Fatalf("%q", res.Message)
	}
	calls := f.r.CallList()
	if len(calls) != 1 || calls[0] != "stop 11111111" {
		t.Fatalf("only `claude stop` may run, never rm: %v", calls)
	}
	if len(s.View().Watches) != 0 {
		t.Fatal("the watch is removed")
	}
	mu.Lock()
	defer mu.Unlock()
	if !removedFirst {
		t.Fatal("the watch must be saved as removed before the copy is stopped")
	}
}

// A stop the CLI refuses leaves the watch exactly where it was.
func TestAFailedStopKeepsTheWatch(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555591"
	f := newFixture(t, func(args []string) (string, error) {
		if strings.HasPrefix(strings.Join(args, " "), "stop ") {
			return "error: no such session.", errors.New("exit 1")
		}
		return "", nil
	})
	f.p.SetAlive(9, true)
	f.setFiles(bgSession(9, id, "b"))
	f.d.Obs.RefreshNow()
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "11111111", Cwd: "/home/dev/ws",
		OriginHost: claude.HostVSCode, PromiseState: "fallback", HasSavedOptions: true})
	s.reconcileForTest(f.d.Obs.Current())

	res := s.Stop(id, ViaPage)
	if res.OK || res.Message != "Could not stop 11111111: error: no such session. Stop it by hand with `claude stop 11111111`." {
		t.Fatalf("%+v", res)
	}
	if w, ok := s.watchForTest(id); !ok || w.PromiseState != "fallback" {
		t.Fatalf("the watch must be kept: %+v", w)
	}
	st, _ := f.d.Store.Load()
	if st.Find(id) == nil {
		t.Fatal("and saved again")
	}
}

// With a display, Stop is only for a babysat session our copy carries.
func TestStopIsRefusedOutsideTheBackground(t *testing.T) {
	watched := "11111111-2222-4333-8444-555555555592"
	loose := "22222222-2222-4333-8444-555555555593"
	f := newFixture(t, func([]string) (string, error) { return "", nil })
	f.p.SetAlive(7, true)
	f.p.SetAlive(9, true)
	f.setFiles(sess(7, watched, "interactive", "cli", ""), bgSession(9, loose, "b"))
	f.d.Obs.RefreshNow()
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: watched, ShortID: "11111111", Cwd: "/home/dev/ws",
		OriginHost: claude.HostTerminal, PromiseState: "inplace"})
	s.reconcileForTest(f.d.Obs.Current())

	if res := s.Stop(watched, ViaPage); res.OK || res.Message != "That session has no background copy running." {
		t.Fatalf("%+v", res)
	}
	if res := s.Stop(loose, ViaPage); res.OK || res.Message != StopRefused {
		t.Fatalf("%+v", res)
	}
	for _, sn := range s.View().Sessions {
		if sn.CanStop {
			t.Fatalf("no Stop is offered with a display: %+v", sn)
		}
	}
	if len(f.r.CallList()) != 0 {
		t.Fatalf("nothing may run: %v", f.r.CallList())
	}
}

// On a machine with no display the page is the only way to end a session
// without a shell, so any background session can be stopped, babysat or not.
func TestStopOnAServerStopsAnyBackgroundSession(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555594"
	f := newFixture(t, func([]string) (string, error) { return "", nil })
	headless(f)
	f.p.SetAlive(9, true)
	f.setFiles(bgSession(9, id, "b"))
	f.d.Obs.RefreshNow()
	s := New(*f.d)
	s.reconcileForTest(f.d.Obs.Current())
	if v := s.View(); len(v.Sessions) != 1 || !v.Sessions[0].CanStop {
		t.Fatalf("a server offers Stop for every background session: %+v", v.Sessions)
	}

	res := s.Stop(id, ViaPage)
	if !res.OK || res.Message != StopDone {
		t.Fatalf("%+v", res)
	}
	if calls := f.r.CallList(); len(calls) != 1 || calls[0] != "stop 11111111" {
		t.Fatalf("%v", calls)
	}
}

// With two background copies running, Stop stops the one the watch knows,
// whichever the snapshot lists first.
func TestStopStopsTheWatchesOwnCopy(t *testing.T) {
	id := "11111111-2222-4333-8444-5555555555d1"
	f := newFixture(t, func([]string) (string, error) { return "", nil })
	f.p.SetAlive(9, true)
	f.p.SetAlive(11, true)
	f.setFiles(sess(9, id, "bg", "cli", "0a0b0c0d"), sess(11, id, "bg", "cli", "61616161"))
	f.d.Obs.RefreshNow()
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "61616161", Name: "demo-a1", Cwd: "/home/dev/ws",
		OriginHost: claude.HostDesktop, PromiseState: "fallback", HasSavedOptions: true})

	res := s.Stop(id, ViaPage)
	if !res.OK || res.ShortID != "61616161" {
		t.Fatalf("%+v", res)
	}
	if calls := f.r.CallList(); len(calls) != 1 || calls[0] != "stop 61616161" {
		t.Fatalf("the watch's own copy is the one stopped: %v", calls)
	}
}

// With several copies running and none of them the watch's own, nothing
// tells which one Stop means, so it refuses and leaves the watch alone.
func TestStopRefusesWhenItCannotTellWhichCopy(t *testing.T) {
	id := "11111111-2222-4333-8444-5555555555d2"
	f := newFixture(t, func([]string) (string, error) { return "", nil })
	f.p.SetAlive(9, true)
	f.p.SetAlive(11, true)
	f.setFiles(sess(9, id, "bg", "cli", "0a0b0c0d"), sess(11, id, "bg", "cli", "61616161"))
	f.d.Obs.RefreshNow()
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "99999999", Name: "demo-a1", Cwd: "/home/dev/ws",
		OriginHost: claude.HostDesktop, PromiseState: "fallback", HasSavedOptions: true})

	res := s.Stop(id, ViaPage)
	if res.OK || res.Message != StopAmbiguous {
		t.Fatalf("%+v", res)
	}
	if strings.ContainsAny(res.Message, "();\n") {
		t.Fatalf("one line, no parentheses or semicolons: %q", res.Message)
	}
	if calls := f.r.CallList(); len(calls) != 0 {
		t.Fatalf("nothing may run: %v", calls)
	}
	if w, ok := s.watchForTest(id); !ok || w.PromiseState != "fallback" {
		t.Fatalf("the watch is left as it was: %+v", w)
	}
}

// Unbabysit is for a session still in its own app, or one that is Stuck.
// Once our copy carries the session, or is about to, the way out is to stop
// the copy.
func TestUnbabysitFollowsTheState(t *testing.T) {
	cases := []struct {
		name    string
		promise string
		paused  bool
		files   func(id string) [][]byte
		ok      bool
	}{
		{"watching", "inplace", false, func(id string) [][]byte { return [][]byte{sess(7, id, "interactive", "cli", "")} }, true},
		{"stuck", "paused", true, func(string) [][]byte { return nil }, true},
		{"in background", "fallback", false, func(id string) [][]byte { return [][]byte{bgSession(7, id, "b")} }, false},
		{"starting", "fallback", false, func(string) [][]byte { return nil }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := "11111111-2222-4333-8444-555555555595"
			f := newFixture(t, func([]string) (string, error) { return "[]", nil })
			f.p.SetAlive(7, true)
			f.setFiles(c.files(id)...)
			f.d.Obs.RefreshNow()
			s := New(*f.d)
			s.addWatchForTest(state.Watch{SessionID: id, ShortID: "11111111", Cwd: "/home/dev/ws",
				OriginHost: claude.HostTerminal, PromiseState: c.promise, Paused: c.paused, HasSavedOptions: true})

			res := s.Unbabysit(id, ViaPage)
			if res.OK != c.ok {
				t.Fatalf("%+v", res)
			}
			_, still := s.watchForTest(id)
			if still == c.ok {
				t.Fatalf("the watch is removed exactly when unbabysit is allowed")
			}
			if len(f.r.CallList()) != 0 || len(f.p.CallList()) != 0 {
				t.Fatalf("unbabysit never touches a process: %v %v", f.r.CallList(), f.p.CallList())
			}
		})
	}
}

// Unbabysit closes nothing and starts nothing: the session keeps running in
// its app, and the answer says where that is.
func TestUnbabysitLeavesTheSessionWhereItIs(t *testing.T) {
	id := "11111111-2222-4333-8444-5555555555d3"
	f := newFixture(t, func([]string) (string, error) { return "", nil })
	f.p.SetAlive(7, true)
	f.setFiles(desktopSession(7, id))
	f.d.Obs.RefreshNow()
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "11111111", Name: "demo-a1", Cwd: "/home/dev/ws",
		OriginHost: claude.HostDesktop, PromiseState: "inplace", HasSavedOptions: true})

	res := s.Unbabysit(id, ViaPage)
	if !res.OK || res.Message != "Stopped babysitting demo-a1. It is now in the desktop app." {
		t.Fatalf("%+v", res)
	}
	if len(f.r.CallList()) != 0 || len(f.p.CallList()) != 0 {
		t.Fatalf("nothing is closed or started: %v %v", f.r.CallList(), f.p.CallList())
	}
	if _, still := s.watchForTest(id); still {
		t.Fatal("the watch is gone")
	}
	if !f.p.IsAlive(7) {
		t.Fatal("the app's session keeps running")
	}
}

// The warning that a background copy would not start comes from a
// read-only look at the CLI's own trust flags, and from the home folder.
func TestTheFallbackWarning(t *testing.T) {
	trusted := "11111111-2222-4333-8444-5555555555a0"
	untrusted := "22222222-2222-4333-8444-5555555555a1"
	home := "33333333-2222-4333-8444-5555555555a2"
	background := "44444444-2222-4333-8444-5555555555a3"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	config := `{"projects":{"/home/dev/ws":{"hasTrustDialogAccepted":true},"/home/dev/new":{"hasTrustDialogAccepted":false}}}`
	if err := os.WriteFile(f.d.ClaudeConfig, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	at := func(pid int, id, cwd, kind string) []byte {
		return []byte(fmt.Sprintf(`{"pid":%d,"sessionId":"%s","cwd":"%s","procStart":"7","kind":"%s","entrypoint":"cli"}`,
			pid, id, cwd, kind))
	}
	for pid := 1; pid <= 4; pid++ {
		f.p.SetAlive(pid, true)
	}
	f.setFiles(
		at(1, trusted, "/home/dev/ws/api", "interactive"),
		at(2, untrusted, "/home/dev/new", "interactive"),
		at(3, home, "/home/dev", "interactive"),
		at(4, background, "/home/dev/new", "bg"),
	)
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()

	// The file is read on the stats worker, so the warnings turn up in a
	// view published after that read has come back.
	want := map[string]string{trusted: "", untrusted: UntrustedWarning("/home/dev/new"), home: FallbackHome, background: ""}
	matches := func() bool {
		v := s.View()
		if len(v.Sessions) != len(want) {
			return false
		}
		for _, sn := range v.Sessions {
			if sn.FallbackWarning != want[sn.ID] {
				return false
			}
		}
		return true
	}
	if !waitFor(t, f, matches) {
		for _, sn := range s.View().Sessions {
			t.Errorf("%s in %s: %q, want %q", sn.ShortID, sn.Cwd, sn.FallbackWarning, want[sn.ID])
		}
		t.FailNow()
	}

	res := s.Babysit(untrusted, false, ViaPage)
	if !res.OK || !strings.HasSuffix(res.Message, UntrustedWarning("/home/dev/new")) {
		t.Fatalf("babysitting is still allowed, and says so: %+v", res)
	}
	for _, w := range s.View().Watches {
		if w.SessionID == untrusted && w.FallbackWarning != UntrustedWarning("/home/dev/new") {
			t.Fatalf("the card keeps the warning: %+v", w)
		}
	}
	if len(f.r.CallList()) != 0 {
		t.Fatalf("the check only reads: %v", f.r.CallList())
	}
}

// Conversations from the last two weeks that run nowhere are listed, with
// the command that picks one up again; a live one is not.
func TestNotRunningListsRecentConversations(t *testing.T) {
	live := "11111111-2222-4333-8444-5555555555b0"
	past := "22222222-2222-4333-8444-5555555555b1"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	f.writeTranscript(t, "/home/dev/ws", live)
	dir := filepath.Join(f.d.ProjectsDir, claude.Slug("/home/dev/api"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","cwd":"/home/dev/api"}` + "\n" + `{"type":"custom-title","customTitle":"demo-b2"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, past+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	f.p.SetAlive(7, true)
	f.setFiles(sess(7, live, "interactive", "cli", ""))
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()

	if !waitFor(t, f, func() bool { return len(s.View().NotRunning) == 1 }) {
		t.Fatalf("%+v", s.View().NotRunning)
	}
	p := s.View().NotRunning[0]
	if p.ID != past || p.Name != "demo-b2" || p.Cwd != "/home/dev/api" || p.ResumeCmd != hosts.ResumeCommandIn("/home/dev/api", past) {
		t.Fatalf("%+v", p)
	}
	if time.Since(p.LastActivity) > time.Minute {
		t.Fatalf("last activity is when it was last written: %v", p.LastActivity)
	}
}

// Reading a large projects folder is disk work, and it never holds up an
// action: the list turns up in a later view.
func TestThePastListIsReadAwayFromTheLoop(t *testing.T) {
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	f.d.listPast = func(time.Time, map[string]bool) []claude.PastSession {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return []claude.PastSession{{ID: "11111111-2222-4333-8444-5555555555c0", Name: "demo-a1", Cwd: "/home/dev/ws", LastActivity: time.Now()}}
	}
	s, _, cancel := newSup(t, f)
	defer cancel()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the list was never asked for")
	}
	done := make(chan Result, 1)
	go func() { done <- s.ResumeWatch("11111111-2222-4333-8444-5555555555c1", ViaPage) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("an action waited for the list")
	}
	close(release)
	if !waitFor(t, f, func() bool { return len(s.View().NotRunning) == 1 }) {
		t.Fatalf("the list must turn up in a later view: %+v", s.View().NotRunning)
	}
}

// A copy that died between the page showing Stop and the click leaves
// nothing to stop, and the watch is left to start it again.
func TestStopAfterTheCopyDiedChangesNothing(t *testing.T) {
	id := "11111111-2222-4333-8444-5555555555c2"
	f := newFixture(t, func([]string) (string, error) { return "", nil })
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "11111111", Cwd: "/home/dev/ws",
		OriginHost: claude.HostDesktop, PromiseState: "fallback", HasSavedOptions: true})

	if res := s.Stop(id, ViaPage); res.OK || res.Message != "That session has no background copy running." {
		t.Fatalf("%+v", res)
	}
	if w, ok := s.watchForTest(id); !ok || w.PromiseState != "fallback" {
		t.Fatalf("the watch is left as it was: %+v", w)
	}
	if len(f.r.CallList()) != 0 {
		t.Fatalf("%v", f.r.CallList())
	}
}

// Babysit can switch start at login on in the same breath, exactly as the
// Settings switch does, and leaves it alone when it is on already or was
// not asked for.
func TestBabysitCanSwitchOnStartAtLogin(t *testing.T) {
	first := "11111111-2222-4333-8444-5555555555e0"
	second := "22222222-2222-4333-8444-5555555555e1"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	var asked []bool
	f.d.Autostart = func(enable bool) (string, error) {
		asked = append(asked, enable)
		return "/home/dev/.config/autostart/ccbabysitter", nil
	}
	f.p.SetAlive(7, true)
	f.p.SetAlive(8, true)
	f.setFiles(sess(7, first, "interactive", "cli", ""), sess(8, second, "interactive", "cli", ""))
	f.d.Obs.RefreshNow()
	s := New(*f.d)

	res := s.Babysit(first, true, ViaPage)
	if !res.OK || !strings.HasSuffix(res.Message, " Start at login is on.") {
		t.Fatalf("%+v", res)
	}
	if len(asked) != 1 || !asked[0] || !s.View().Settings.Autostart {
		t.Fatalf("start at login must be switched on once: %v %+v", asked, s.View().Settings)
	}
	if st, _ := f.d.Store.Load(); !st.Settings.Autostart {
		t.Fatal("and saved")
	}
	if res := s.Babysit(second, true, ViaPage); !res.OK || strings.Contains(res.Message, "Start at login") || len(asked) != 1 {
		t.Fatalf("it is on already, so nothing is done about it: %+v %v", res, asked)
	}
}

// A failure to switch start at login on does not undo the babysitting,
// and says why in the same line.
func TestBabysitSaysWhenStartAtLoginCouldNotBeSwitchedOn(t *testing.T) {
	id := "11111111-2222-4333-8444-5555555555e2"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	f.d.Autostart = func(bool) (string, error) { return "", errors.New("the login folder is read-only") }
	f.p.SetAlive(7, true)
	f.setFiles(sess(7, id, "interactive", "cli", ""))
	f.d.Obs.RefreshNow()
	s := New(*f.d)

	res := s.Babysit(id, true, ViaPage)
	if !res.OK || !strings.HasSuffix(res.Message, "Could not change starting at login: the login folder is read-only") {
		t.Fatalf("%+v", res)
	}
	if len(s.View().Watches) != 1 || s.View().Settings.Autostart {
		t.Fatalf("the session is babysat and start at login is still off: %+v", s.View())
	}
}

// A server has no app to hand a session back to, so a watch there may be
// let go in every state but Starting, In background included.
func TestUnbabysitOnAServer(t *testing.T) {
	cases := []struct {
		name    string
		promise string
		files   func(id string) [][]byte
		ok      bool
	}{
		{"in background", "fallback", func(id string) [][]byte { return [][]byte{bgSession(7, id, "b")} }, true},
		{"starting", "fallback", func(string) [][]byte { return nil }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := "11111111-2222-4333-8444-5555555555e3"
			f := newFixture(t, func([]string) (string, error) { return "[]", nil })
			headless(f)
			f.p.SetAlive(7, true)
			f.setFiles(c.files(id)...)
			f.d.Obs.RefreshNow()
			s := New(*f.d)
			s.addWatchForTest(state.Watch{SessionID: id, ShortID: "11111111", Cwd: "/home/dev/ws",
				OriginHost: claude.HostTerminal, PromiseState: c.promise, HasSavedOptions: true})
			s.reconcileForTest(f.d.Obs.Current())
			if w, _ := firstWatch(s); w.CanUnbabysit != c.ok {
				t.Fatalf("the page offers unbabysit exactly when it is allowed: %+v", w)
			}

			if res := s.Unbabysit(id, ViaPage); res.OK != c.ok {
				t.Fatalf("%+v", res)
			}
			if len(f.r.CallList()) != 0 {
				t.Fatalf("unbabysit never touches a process: %v", f.r.CallList())
			}
		})
	}
}

// Every background session, babysat or not, carries the command that
// attaches a terminal to it, and on a server the same command to run from
// another machine over ssh, naming the server's CLI by its full path.
func TestAttachCommandsForBackgroundSessions(t *testing.T) {
	watched := "11111111-2222-4333-8444-5555555555e4"
	loose := "22222222-2222-4333-8444-5555555555e5"
	app := "33333333-2222-4333-8444-5555555555e6"
	for _, onServer := range []bool{false, true} {
		f := newFixture(t, func([]string) (string, error) { return "[]", nil })
		if onServer {
			f.d.Env = func() hosts.Env {
				return hosts.Env{Platform: "linux", Headless: true, CLIFound: true, CLIPresent: true,
					CLIPath: "/home/dev/.local/bin/claude"}
			}
		}
		f.d.SSHTarget = func() string { return "dev@203.0.113.7" }
		for pid := 7; pid <= 9; pid++ {
			f.p.SetAlive(pid, true)
		}
		f.setFiles(bgSession(7, watched, "b"), bgSession(8, loose, "b"), sess(9, app, "interactive", "cli", ""))
		f.d.Obs.RefreshNow()
		s := New(*f.d)
		s.addWatchForTest(state.Watch{SessionID: watched, ShortID: "11111111", Cwd: "/home/dev/ws",
			OriginHost: claude.HostDesktop, PromiseState: "fallback", HasSavedOptions: true})
		s.reconcileForTest(f.d.Obs.Current())

		ssh := func(short string) string {
			if !onServer {
				return ""
			}
			return "ssh -t dev@203.0.113.7 /home/dev/.local/bin/claude attach " + short
		}
		v := s.View()
		if w := v.Watches[0]; w.AttachCmd != "claude attach 11111111" || w.SSHAttachCmd != ssh("11111111") {
			t.Fatalf("server %v: %+v", onServer, w)
		}
		for _, sn := range v.Sessions {
			want, wantSSH := "", ""
			if sn.ID == loose {
				want, wantSSH = "claude attach 22222222", ssh("22222222")
			}
			if sn.AttachCmd != want || sn.SSHAttachCmd != wantSSH {
				t.Fatalf("server %v, %s: %q %q", onServer, sn.ShortID, sn.AttachCmd, sn.SSHAttachCmd)
			}
		}
	}
}

// Reading the CLI's settings file is disk work on a file the CLI rewrites
// often, and it never holds up an action: the warning turns up in a later
// view.
func TestTheTrustFileIsReadAwayFromTheLoop(t *testing.T) {
	untrusted := "22222222-2222-4333-8444-5555555555f0"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	config := `{"projects":{"/home/dev/new":{"hasTrustDialogAccepted":false}}}`
	if err := os.WriteFile(f.d.ClaudeConfig, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	f.p.SetAlive(2, true)
	f.setFiles([]byte(fmt.Sprintf(`{"pid":2,"sessionId":"%s","cwd":"/home/dev/new","procStart":"7","kind":"interactive","entrypoint":"cli"}`, untrusted)))
	f.d.Obs.RefreshNow()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	// Released on every way out, so a failure here never leaves the worker
	// stuck and the loop unable to stop.
	defer free()
	real := &claude.TrustFile{Path: f.d.ClaudeConfig}
	f.d.readTrust = func(now time.Time) claude.Trust {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return real.Read(now)
	}
	s, _, cancel := newSup(t, f)
	defer cancel()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the file was never asked for")
	}
	done := make(chan Result, 1)
	go func() { done <- s.Babysit(untrusted, false, ViaPage) }()
	select {
	case res := <-done:
		if !res.OK || strings.Contains(res.Message, UntrustedWarning("/home/dev/new")) {
			t.Fatalf("nothing is claimed before the file has been read: %+v", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("an action waited for the file")
	}
	free()
	if !waitFor(t, f, func() bool {
		w, ok := firstWatch(s)
		return ok && w.FallbackWarning == UntrustedWarning("/home/dev/new")
	}) {
		t.Fatalf("the warning must turn up in a later view: %+v", s.View().Watches)
	}
}

// A settings file past the size cap is left unread, and then nothing is
// claimed: no warning is shown on a guess.
func TestAnOversizeTrustFileWarnsOfNothing(t *testing.T) {
	untrusted := "22222222-2222-4333-8444-5555555555f1"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	config := `{"projects":{"/home/dev/new":{"hasTrustDialogAccepted":false}}}`
	if err := os.WriteFile(f.d.ClaudeConfig, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(f.d.ClaudeConfig, 65<<20); err != nil {
		t.Fatal(err)
	}
	f.p.SetAlive(2, true)
	f.setFiles([]byte(fmt.Sprintf(`{"pid":2,"sessionId":"%s","cwd":"/home/dev/new","procStart":"7","kind":"interactive","entrypoint":"cli"}`, untrusted)))
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()

	if !waitFor(t, f, func() bool { return s.trustReadForTest() && len(s.View().Sessions) == 1 }) {
		t.Fatal("the file was never looked at")
	}
	if w := s.View().Sessions[0].FallbackWarning; w != "" {
		t.Fatalf("an unread file claims nothing: %q", w)
	}
	if res := s.Babysit(untrusted, false, ViaPage); !res.OK || strings.Contains(res.Message, "Background") {
		t.Fatalf("%+v", res)
	}
}

// newSupervisorWithLiveTerminalSession returns a running supervisor, its
// log, and the id of one live terminal session named name whose session
// file recorded procStart "1790000000000".
func newSupervisorWithLiveTerminalSession(t *testing.T, name string) (*Supervisor, *state.Log, string) {
	t.Helper()
	id := "cccccccc-0000-4000-8000-000000000001"
	f := newFixture(t, nil)
	f.p.SetAlive(7, true)
	f.setFiles([]byte(fmt.Sprintf(`{"pid":7,"sessionId":"%s","cwd":"/home/dev/ws","procStart":"1790000000000","kind":"interactive","entrypoint":"cli","name":"%s","bridgeSessionId":"b"}`, id, name)))
	f.d.Obs.RefreshNow()
	s, _, _ := newSup(t, f)
	return s, f.d.Log, id
}

func logMessages(log *state.Log) []string {
	var got []string
	for _, e := range log.Recent(20, "") {
		got = append(got, e.Message)
	}
	return got
}

func TestActionsFromTheCommandLineSaySoInActivity(t *testing.T) {
	s, log, id := newSupervisorWithLiveTerminalSession(t, "api")
	if r := s.Babysit(id, false, ViaCLI); !r.OK {
		t.Fatalf("babysit: %s", r.Message)
	}
	if r := s.ResumeWatch(id, ViaCLI); !r.OK {
		t.Fatalf("resume: %s", r.Message)
	}
	if r := s.Unbabysit(id, ViaCLI); !r.OK {
		t.Fatalf("unbabysit: %s", r.Message)
	}
	got := logMessages(log)
	for _, w := range []string{
		"babysitting in a terminal window, from the command line",
		"babysitting resumed, from the command line",
		"no longer babysitting, from the command line",
	} {
		if !slices.Contains(got, w) {
			t.Errorf("activity has no %q, has %q", w, got)
		}
	}
}

func TestActionsFromThePageReadAsBefore(t *testing.T) {
	s, log, id := newSupervisorWithLiveTerminalSession(t, "api")
	s.Babysit(id, false, ViaPage)
	got := logMessages(log)
	for _, m := range got {
		if strings.Contains(m, "command line") {
			t.Errorf("a page action mentions the command line: %q", m)
		}
	}
	if !slices.Contains(got, "babysitting in a terminal window") {
		t.Errorf("page wording changed: %q", got)
	}
}

func TestStopFromTheCommandLineSaysSoInActivity(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555595"
	f := newFixture(t, nil)
	headless(f)
	f.r.SetRespond(func(args []string) (string, error) {
		if strings.HasPrefix(strings.Join(args, " "), "stop ") {
			f.p.SetAlive(9, false)
		}
		return "", nil
	})
	f.p.SetAlive(9, true)
	f.setFiles(bgSession(9, id, "b"))
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()
	if res := s.Stop(id, ViaCLI); !res.OK {
		t.Fatal(res.Message)
	}
	if got := logMessages(f.d.Log); !slices.Contains(got, "stopped background copy 11111111, from the command line") {
		t.Errorf("activity: %q", got)
	}
}

func TestViaSuffix(t *testing.T) {
	if ViaCLI.Suffix() != ", from the command line" || ViaPage.Suffix() != "" || Via("other").Suffix() != "" {
		t.Error("Suffix is wrong")
	}
}

func TestViewCarriesProcStartForSessionsAndWatches(t *testing.T) {
	s, _, id := newSupervisorWithLiveTerminalSession(t, "api")
	v := s.View()
	if len(v.Sessions) != 1 || v.Sessions[0].ProcStart != "1790000000000" {
		t.Fatalf("unbabysat session view = %+v", v.Sessions)
	}
	s.Babysit(id, false, ViaPage)
	v = s.View()
	if len(v.Sessions) != 0 || len(v.Watches) != 1 || v.Watches[0].ProcStart != "1790000000000" || v.Watches[0].PID == 0 {
		t.Fatalf("babysat: sessions %+v, watches %+v", v.Sessions, v.Watches)
	}
}

// startAtLoginEntries are the Activity entries about start at login.
func startAtLoginEntries(log *state.Log) []string {
	var out []string
	for _, m := range logMessages(log) {
		if strings.HasPrefix(m, "start at login") {
			out = append(out, m)
		}
	}
	return out
}

func TestSettingsFromTheCommandLineMarkStartAtLogin(t *testing.T) {
	f := newFixture(t, nil)
	f.d.Autostart = func(bool) (string, error) { return "/home/dev/.config/autostart/ccbabysitter", nil }
	s, _, _ := newSup(t, f)
	next := s.View().Settings
	next.Autostart = true
	if r := s.SetSettings(next, ViaCLI); !r.OK {
		t.Fatal(r.Message)
	}
	got := startAtLoginEntries(f.d.Log)
	if len(got) != 1 || !strings.HasPrefix(got[0], "start at login enabled") || !strings.HasSuffix(got[0], ", from the command line") {
		t.Errorf("entries: %q", got)
	}
}

func TestBabysitWithStartAtLoginFromTheCommandLineMarksIt(t *testing.T) {
	f := newFixture(t, nil)
	f.d.Autostart = func(bool) (string, error) { return "/home/dev/.config/autostart/ccbabysitter", nil }
	id := "cccccccc-0000-4000-8000-000000000002"
	f.p.SetAlive(7, true)
	f.setFiles(sess(7, id, "interactive", "cli", ""))
	f.d.Obs.RefreshNow()
	s, _, _ := newSup(t, f)
	if r := s.Babysit(id, true, ViaCLI); !r.OK {
		t.Fatal(r.Message)
	}
	got := startAtLoginEntries(f.d.Log)
	if len(got) != 1 || !strings.HasPrefix(got[0], "start at login enabled") || !strings.HasSuffix(got[0], ", from the command line") {
		t.Errorf("entries: %q", got)
	}
}
