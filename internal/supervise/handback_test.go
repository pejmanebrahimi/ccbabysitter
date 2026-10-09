package supervise

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// fakeClock is a clock a test moves by hand. The loop and the test both
// read it, so it is read under a mutex.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// handBackFixture is a machine with a display where one babysat session
// is In background, carried by the copy with pid 9, and one other
// conversation not running is newer than it.
func handBackFixture(t *testing.T, id, other string, origin claude.Host, stopErr error) (*fixture, *Supervisor, *fakeClock) {
	t.Helper()
	clock := &fakeClock{now: time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)}
	f := newFixture(t, func(args []string) (string, error) {
		if strings.HasPrefix(strings.Join(args, " "), "stop ") && stopErr != nil {
			return "error: no such session.", stopErr
		}
		return "", nil
	})
	f.d.Now = clock.Now
	f.d.listPast = func(_ time.Time, skip map[string]bool) []claude.PastSession {
		var out []claude.PastSession
		for _, p := range []claude.PastSession{
			{ID: other, Name: "demo-b2", Cwd: "/home/dev/api", LastActivity: clock.Now()},
			{ID: id, Name: "demo-a1", Cwd: "/home/dev/ws", LastActivity: clock.Now().Add(-time.Hour)},
		} {
			if !skip[p.ID] {
				out = append(out, p)
			}
		}
		return out
	}
	f.p.SetAlive(9, true)
	f.setFiles(bgSession(9, id, "b"))
	f.d.Obs.RefreshNow()
	s, _, _ := newSup(t, f)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "11111111", Name: "demo-a1", Cwd: "/home/dev/ws",
		OriginHost: origin, PromiseState: "fallback", HasSavedOptions: true})
	if w, ok := firstWatch(s); !ok || w.State != StateInBackground {
		t.Fatalf("the watch is In background: %+v", w)
	}
	return f, s, clock
}

// stopTheCopy makes the stopped copy's process go away, as it does once
// `claude stop` has run.
func stopTheCopy(f *fixture) {
	f.p.SetAlive(9, false)
	f.setFiles()
}

// A session handed back to its app sits first in Not running with where
// it went, the newer plain one after it, and only while it runs nowhere.
func TestAHandedBackSessionIsBadgedFirstInNotRunning(t *testing.T) {
	id := "11111111-2222-4333-8444-5555555555d0"
	other := "22222222-2222-4333-8444-5555555555d1"
	f, s, _ := handBackFixture(t, id, other, claude.HostVSCode, nil)

	if res := s.Stop(id, ViaPage); !res.OK {
		t.Fatal(res.Message)
	}
	if to, ok := s.handedBackForTest(id); !ok || to != claude.HostVSCode {
		t.Fatalf("a successful stop is remembered: %q %v", to, ok)
	}
	// A pass that still sees the stopped copy on its way out keeps it.
	s.reconcileForTest(f.d.Obs.Current())
	if _, ok := s.handedBackForTest(id); !ok {
		t.Fatal("the copy being stopped is not the session running again")
	}

	stopTheCopy(f)
	if !waitFor(t, f, func() bool { return len(s.View().NotRunning) == 2 }) {
		t.Fatalf("%+v", s.View().NotRunning)
	}
	nr := s.View().NotRunning
	if nr[0].ID != id || nr[0].HandedBackTo != claude.HostVSCode || nr[1].ID != other || nr[1].HandedBackTo != "" {
		t.Fatalf("the handed back one sorts first with its badge: %+v", nr)
	}
	if nr[0].AttachCmd != "" || nr[0].SSHAttachCmd != "" {
		t.Fatalf("a session handed back to its app is opened there, not attached to: %+v", nr[0])
	}
	if nr[0].ActivityLabel == "" {
		t.Fatalf("the name Activity knew it by is kept: %+v", nr[0])
	}
}

// Each way back names its own app, and one from a session that started
// in the background is a terminal.
func TestAHandBackNamesTheAppItWentTo(t *testing.T) {
	for origin, want := range map[claude.Host]claude.Host{
		claude.HostDesktop:    claude.HostDesktop,
		claude.HostTerminal:   claude.HostTerminal,
		claude.HostBackground: claude.HostTerminal,
	} {
		id := "11111111-2222-4333-8444-5555555555d2"
		_, s, _ := handBackFixture(t, id, "22222222-2222-4333-8444-5555555555d3", origin, nil)
		if res := s.Stop(id, ViaPage); !res.OK {
			t.Fatal(res.Message)
		}
		if to, ok := s.handedBackForTest(id); !ok || to != want {
			t.Errorf("from %s: %q %v", origin, to, ok)
		}
	}
}

// A stop that failed hands nothing back.
func TestAFailedStopHandsNothingBack(t *testing.T) {
	id := "11111111-2222-4333-8444-5555555555d4"
	_, s, _ := handBackFixture(t, id, "22222222-2222-4333-8444-5555555555d5", claude.HostDesktop, errors.New("exit 1"))
	if res := s.Stop(id, ViaPage); res.OK {
		t.Fatalf("%+v", res)
	}
	if _, ok := s.handedBackForTest(id); ok {
		t.Fatal("a failed stop must not be remembered")
	}
}

// Running again anywhere ends the hand-back: the session is under Running
// as usual, and once it stops again its row is plain.
func TestAHandBackEndsWhenTheSessionRunsAgain(t *testing.T) {
	id := "11111111-2222-4333-8444-5555555555d6"
	other := "22222222-2222-4333-8444-5555555555d7"
	f, s, _ := handBackFixture(t, id, other, claude.HostDesktop, nil)
	if res := s.Stop(id, ViaPage); !res.OK {
		t.Fatal(res.Message)
	}
	stopTheCopy(f)
	if !waitFor(t, f, func() bool { nr := s.View().NotRunning; return len(nr) == 2 && nr[0].HandedBackTo != "" }) {
		t.Fatalf("%+v", s.View().NotRunning)
	}

	f.p.SetAlive(12, true)
	f.setFiles(desktopSession(12, id))
	if !waitFor(t, f, func() bool { _, ok := s.handedBackForTest(id); return !ok }) {
		t.Fatal("running again drops the hand-back")
	}
	v := s.View()
	if len(v.Sessions) != 1 || v.Sessions[0].ID != id {
		t.Fatalf("it runs under Running as usual: %+v", v.Sessions)
	}
	for _, p := range v.NotRunning {
		if p.ID == id {
			t.Fatalf("a running session is not in Not running: %+v", v.NotRunning)
		}
	}

	f.p.SetAlive(12, false)
	f.setFiles()
	if !waitFor(t, f, func() bool { nr := s.View().NotRunning; return len(nr) == 2 }) {
		t.Fatalf("%+v", s.View().NotRunning)
	}
	for _, p := range s.View().NotRunning {
		if p.HandedBackTo != "" {
			t.Fatalf("the row is plain once it has run again: %+v", p)
		}
	}
}

// After a day the badge goes and the memory with it.
func TestAHandBackEndsAfterADay(t *testing.T) {
	id := "11111111-2222-4333-8444-5555555555d8"
	other := "22222222-2222-4333-8444-5555555555d9"
	f, s, clock := handBackFixture(t, id, other, claude.HostVSCode, nil)
	if res := s.Stop(id, ViaPage); !res.OK {
		t.Fatal(res.Message)
	}
	stopTheCopy(f)
	if !waitFor(t, f, func() bool { nr := s.View().NotRunning; return len(nr) == 2 && nr[0].HandedBackTo != "" }) {
		t.Fatalf("%+v", s.View().NotRunning)
	}

	clock.add(23 * time.Hour)
	s.reconcileForTest(f.d.Obs.Current())
	if _, ok := s.handedBackForTest(id); !ok {
		t.Fatal("still remembered within the day")
	}

	clock.add(time.Hour)
	if !waitFor(t, f, func() bool { _, ok := s.handedBackForTest(id); return !ok }) {
		t.Fatal("dropped after a day")
	}
	if !waitFor(t, f, func() bool { nr := s.View().NotRunning; return len(nr) == 2 && nr[0].ID == other }) {
		t.Fatalf("the row is plain and in its usual place: %+v", s.View().NotRunning)
	}
	for _, p := range s.View().NotRunning {
		if p.HandedBackTo != "" {
			t.Fatalf("%+v", p)
		}
	}
}

// stoppedForTest reports the short id the supervisor remembers for a
// background session whose copy Stop ended.
func (s *Supervisor) stoppedForTest(id string) (string, bool) {
	var short string
	ok := false
	s.ask(func(context.Context) Result {
		var sc stoppedCopy
		sc, ok = s.stopped[id]
		short = sc.short
		return Result{OK: true}
	})
	return short, ok
}

// A background session stopped on a server, babysat or not, is started
// again from its Not running row with its attach command, here and from
// another machine. The stop itself says only where to find it, and the row
// goes back to its resume command once the session has run again.
func TestAStoppedBackgroundSessionIsAttachedFromNotRunning(t *testing.T) {
	id := "11111111-2222-4333-8444-5555555555da"
	other := "22222222-2222-4333-8444-5555555555db"
	for _, babysat := range []bool{false, true} {
		clock := &fakeClock{now: time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)}
		f := newFixture(t, func([]string) (string, error) { return "", nil })
		f.d.Env = func() hosts.Env {
			return hosts.Env{Platform: "linux", Headless: true, CLIFound: true, CLIPresent: true,
				CLIPath: "/home/dev/.local/bin/claude"}
		}
		f.d.SSHTarget = func() string { return "dev@203.0.113.7" }
		f.d.Now = clock.Now
		f.d.listPast = func(_ time.Time, skip map[string]bool) []claude.PastSession {
			var out []claude.PastSession
			for _, p := range []claude.PastSession{
				{ID: other, Name: "demo-b2", Cwd: "/home/dev/api", LastActivity: clock.Now()},
				{ID: id, Name: "demo-a1", Cwd: "/home/dev/ws", LastActivity: clock.Now().Add(-time.Hour)},
			} {
				if !skip[p.ID] {
					out = append(out, p)
				}
			}
			return out
		}
		f.p.SetAlive(9, true)
		f.setFiles(bgSession(9, id, "b"))
		f.d.Obs.RefreshNow()
		s, _, _ := newSup(t, f)
		// One pass over the snapshot with the session in it makes it one
		// that was there before, not one to adopt when it runs again.
		s.reconcileForTest(f.d.Obs.Current())
		if babysat {
			s.addWatchForTest(state.Watch{SessionID: id, ShortID: "11111111", Name: "demo-a1", Cwd: "/home/dev/ws",
				OriginHost: claude.HostBackground, PromiseState: "inplace", HasSavedOptions: true})
		}

		res := s.Stop(id, ViaPage)
		if !res.OK || res.Message != "Stopped. Open it again any time from Not running." {
			t.Fatalf("babysat %v: %+v", babysat, res)
		}
		if short, ok := s.stoppedForTest(id); !ok || short != "11111111" {
			t.Fatalf("babysat %v: the stop is remembered: %q %v", babysat, short, ok)
		}
		if _, ok := s.handedBackForTest(id); ok {
			t.Fatalf("babysat %v: a session that lives in the background is not handed back", babysat)
		}
		stopTheCopy(f)
		if !waitFor(t, f, func() bool { return len(s.View().NotRunning) == 2 }) {
			t.Fatalf("babysat %v: %+v", babysat, s.View().NotRunning)
		}
		for _, p := range s.View().NotRunning {
			want, wantSSH := "", ""
			if p.ID == id {
				want, wantSSH = "claude attach 11111111", "ssh -t dev@203.0.113.7 /home/dev/.local/bin/claude attach 11111111"
			}
			if p.AttachCmd != want || p.SSHAttachCmd != wantSSH || p.HandedBackTo != "" || p.ResumeCmd == "" {
				t.Fatalf("babysat %v: %+v", babysat, p)
			}
			// The command line finds it by the short id stop answered
			// with, and its Activity by the name it had.
			if p.ID == id && (p.CopyShortID != "11111111" || p.ActivityLabel == "") {
				t.Fatalf("babysat %v: %+v", babysat, p)
			}
		}

		// Attaching starts it again under another process, which ends the
		// memory; once it stops on its own the row is a plain one.
		f.p.SetAlive(12, true)
		f.setFiles(bgSession(12, id, "b"))
		if !waitFor(t, f, func() bool { _, ok := s.stoppedForTest(id); return !ok }) {
			t.Fatalf("babysat %v: running again drops the memory", babysat)
		}
		f.p.SetAlive(12, false)
		f.setFiles()
		if !waitFor(t, f, func() bool { nr := s.View().NotRunning; return len(nr) == 2 }) {
			t.Fatalf("babysat %v: %+v", babysat, s.View().NotRunning)
		}
		for _, p := range s.View().NotRunning {
			if p.AttachCmd != "" || p.SSHAttachCmd != "" {
				t.Fatalf("babysat %v: the row is plain once it has run again: %+v", babysat, p)
			}
		}
	}
}

// With a display there is no machine elsewhere to name, so a stopped
// background session's row carries the attach command alone.
func TestAStoppedRowHasNoSSHLineWithADisplay(t *testing.T) {
	s := &Supervisor{deps: Deps{SSHTarget: func() string { return "dev@203.0.113.7" }}}
	if got := s.sshAttachFor("11111111"); got != "" {
		t.Fatalf("%q", got)
	}
	s.env = hosts.Env{Headless: true, CLIPath: "claude"}
	if got := s.sshAttachFor(""); got != "" {
		t.Fatalf("no short id, no command: %q", got)
	}
}

// A list wanted while another is still being read is asked for again as
// soon as that answer is in, rather than waiting out the interval: the
// answer on its way was asked for against sessions that have changed since.
func TestTheNotRunningListWantedMidReadIsAskedForAgain(t *testing.T) {
	f := newFixture(t, nil)
	s := New(*f.d)
	s.ask(func(context.Context) Result {
		s.pastInFlight, s.pastAt, s.pastLive = true, s.deps.Now(), "gone"
		s.askForPast(s.snap)
		if !s.pastWanted || len(s.statsReqs) != 0 {
			t.Errorf("nothing is posted while a read is in flight, but it is wanted: %v %d", s.pastWanted, len(s.statsReqs))
		}
		s.applyStats(statsResult{isPast: true})
		if s.pastWanted || !s.pastInFlight || len(s.statsReqs) != 1 {
			t.Errorf("the answer brings the next read: %v %v %d", s.pastWanted, s.pastInFlight, len(s.statsReqs))
		}
		return Result{OK: true}
	})
}
