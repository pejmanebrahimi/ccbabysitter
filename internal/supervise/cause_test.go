package supervise

import (
	"strings"
	"sync"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// The reason a babysat session went down, from where it ran and what the
// desktop app looked like the moment it went missing.
func TestCauseFor(t *testing.T) {
	running := AppsNow{DesktopKnown: true, DesktopRunning: true, DesktopVersion: "2.1.0"}
	gone := AppsNow{DesktopKnown: true, DesktopVersion: "2.1.0"}
	updated := AppsNow{DesktopKnown: true, DesktopVersion: "2.2.0"}
	unknown := AppsNow{DesktopVersion: "installed"}
	for _, c := range []struct {
		host      claude.Host
		apps      AppsNow
		known     string
		restarted bool
		want      string
	}{
		{claude.HostDesktop, updated, "2.1.0", false, "Claude Desktop restarted for an update from 2.1.0 to 2.2.0"},
		{claude.HostDesktop, gone, "2.1.0", false, "Claude Desktop closed"},
		{claude.HostDesktop, updated, "", false, "Claude Desktop closed"},
		{claude.HostDesktop, AppsNow{DesktopKnown: true, DesktopVersion: "installed"}, "2.1.0", false, "Claude Desktop closed"},
		{claude.HostDesktop, running, "2.1.0", false, "the session ended while Claude Desktop kept running"},
		{claude.HostDesktop, AppsNow{DesktopKnown: true, DesktopRunning: true, DesktopVersion: "2.2.0"}, "2.1.0", false, "Claude Desktop restarted for an update from 2.1.0 to 2.2.0"},
		{claude.HostDesktop, unknown, "2.1.0", false, ""},
		{claude.HostVSCode, running, "2.1.0", false, "VS Code closed, or the session ended in it"},
		{claude.HostTerminal, running, "", false, "its terminal closed, or the session was quit"},
		{claude.HostBackground, running, "", false, "the background copy ended"},
		{claude.HostOther, running, "", false, ""},
		{claude.HostDesktop, updated, "2.1.0", true, "the computer restarted"},
		{claude.HostTerminal, running, "", true, "the computer restarted"},
	} {
		if got := causeFor(c.host, c.apps, c.known, c.restarted); got != c.want {
			t.Errorf("causeFor(%v, %+v, %q, %v) = %q, want %q", c.host, c.apps, c.known, c.restarted, got, c.want)
		}
	}
}

// fakeApps is a desktop app a test can change while the loop reads it.
type fakeApps struct {
	mu  sync.Mutex
	now AppsNow
}

func (a *fakeApps) get() AppsNow {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.now
}

func (a *fakeApps) set(now AppsNow) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.now = now
}

// desktopFixture babysits the given sessions, running in Claude Desktop
// 2.1.0, with resumes that succeed.
func desktopFixture(t *testing.T, ids ...string) (*fixture, *Supervisor, *fakeApps) {
	t.Helper()
	f := newFixture(t, nil)
	f.r.SetRespond(func(args []string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case a == "agents --json":
			return "[]", nil
		case strings.HasPrefix(a, "--bg --resume"):
			id := args[2]
			return "backgrounded \u00b7 " + id[:8] + " \u00b7 demo", nil
		}
		return "", nil
	})
	f.d.Env = func() hosts.Env {
		return hosts.Env{Platform: "darwin", CLIFound: true, CLIPresent: true, DesktopInstalled: true, DesktopRunning: true, DesktopVersion: "2.1.0"}
	}
	apps := &fakeApps{now: AppsNow{DesktopKnown: true, DesktopRunning: true, DesktopVersion: "2.1.0"}}
	f.d.Apps = apps.get
	var files [][]byte
	for i, id := range ids {
		f.writeTranscript(t, "/home/dev/ws", id)
		f.p.SetAlive(10+i, true)
		files = append(files, sess(10+i, id, "interactive", "claude-desktop", ""))
	}
	f.setFiles(files...)
	f.d.Obs.RefreshNow()
	s, _, _ := newSup(t, f)
	for _, id := range ids {
		if res := s.Babysit(id, false, ViaPage); !res.OK {
			t.Fatal(res.Message)
		}
	}
	return f, s, apps
}

func activity(f *fixture) []state.Entry { return f.d.Log.Recent(100, "") }

func hasEntry(f *fixture, match func(state.Entry) bool) bool {
	for _, e := range activity(f) {
		if match(e) {
			return true
		}
	}
	return false
}

// An update of Claude Desktop that closes two babysat sessions is named
// once for both, and each rescue gives it as its reason.
func TestDesktopUpdateIsNamedOnceForAllItsSessions(t *testing.T) {
	a := "aaaaaaaa-0000-4000-8000-000000000001"
	b := "bbbbbbbb-0000-4000-8000-000000000001"
	f, _, apps := desktopFixture(t, a, b)
	apps.set(AppsNow{DesktopKnown: true, DesktopVersion: "2.2.0"})
	f.p.SetAlive(10, false)
	f.p.SetAlive(11, false)
	f.setFiles()
	reason := "Claude Desktop restarted for an update from 2.1.0 to 2.2.0"
	if !waitFor(t, f, func() bool {
		n := 0
		for _, e := range activity(f) {
			if e.Automatic && e.Reason == reason {
				n++
			}
		}
		return n == 2
	}) {
		t.Fatalf("both rescues give the update as their reason: %v", activity(f))
	}
	groups := 0
	for _, e := range activity(f) {
		if e.Message == reason+". Bringing back 2 babysat sessions." {
			groups++
		}
	}
	if groups != 1 {
		t.Fatalf("one line names the update for both sessions, got %d: %v", groups, activity(f))
	}
}

// Desktop that looked closed and comes back with a new version had
// restarted for an update, and Activity says so once.
func TestDesktopThatComesBackNewerWasAnUpdate(t *testing.T) {
	a := "aaaaaaaa-0000-4000-8000-000000000002"
	f, _, apps := desktopFixture(t, a)
	apps.set(AppsNow{DesktopKnown: true, DesktopVersion: "2.1.0"})
	f.p.SetAlive(10, false)
	f.setFiles()
	if !waitFor(t, f, func() bool {
		return hasEntry(f, func(e state.Entry) bool { return e.Automatic && e.Reason == "Claude Desktop closed" })
	}) {
		t.Fatalf("the rescue gives the closed app as its reason: %v", activity(f))
	}
	apps.set(AppsNow{DesktopKnown: true, DesktopRunning: true, DesktopVersion: "2.2.0"})
	want := "Claude Desktop is back as 2.2.0: it had restarted for an update from 2.1.0."
	if !waitFor(t, f, func() bool { return hasEntry(f, func(e state.Entry) bool { return e.Message == want }) }) {
		t.Fatalf("no follow-up line: %v", activity(f))
	}
}

// resumeWithRC is a CLI that lists nothing and brings back each session it
// resumes as a background copy with Remote Control on, so a resume ends
// without waiting on a clock the test holds still.
func resumeWithRC(f *fixture) func(args []string) (string, error) {
	var copies [][]byte
	return func(args []string) (string, error) {
		a := strings.Join(args, " ")
		if a == "agents --json" {
			return "[]", nil
		}
		if strings.HasPrefix(a, "--bg --resume") {
			id, pid := args[2], 20+len(copies)
			f.p.SetAlive(pid, true)
			copies = append(copies, bgSession(pid, id, "b"))
			f.setFiles(copies...)
			return "backgrounded \u00b7 " + id[:8] + " \u00b7 demo", nil
		}
		return "", nil
	}
}

// handDesktop is a machine whose clock and passes the test drives, with
// the given sessions babysat in Claude Desktop 2.1.0 and a CLI whose resume
// brings each one back in the background.
func handDesktop(t *testing.T, ids ...string) (*fixture, *Supervisor, *fakeApps, *time.Time) {
	t.Helper()
	f := newFixture(t, nil)
	f.r.SetRespond(resumeWithRC(f))
	apps := &fakeApps{now: AppsNow{DesktopKnown: true, DesktopRunning: true, DesktopVersion: "2.1.0"}}
	f.d.Apps = apps.get
	clock := time.Date(2026, 10, 8, 3, 0, 0, 0, time.UTC)
	f.d.Now = func() time.Time { return clock }
	s := New(*f.d)
	for _, id := range ids {
		f.writeTranscript(t, "/home/dev/ws", id)
		s.addWatchForTest(state.Watch{SessionID: id, ShortID: id[:8], Cwd: "/home/dev/ws",
			OriginHost: claude.HostDesktop, PromiseState: "inplace", HasSavedOptions: true})
	}
	return f, s, apps, &clock
}

// pass moves the clock on and runs one pass over the given sessions.
func pass(s *Supervisor, clock *time.Time, running ...claude.Session) {
	*clock = clock.Add(10 * time.Second)
	s.reconcileForTest(observe.Snapshot{At: *clock, Sessions: running})
}

func inDesktop(id string) claude.Session { return claude.Session{ID: id, Host: claude.HostDesktop} }

func inBackground(id string) claude.Session {
	return claude.Session{ID: id, Host: claude.HostBackground}
}

// Claude Desktop that closes and opens its own sessions again before any is
// brought back needed nothing from anybody, and Activity says nothing
// about it.
func TestDesktopThatReopensItsSessionsGetsNoLine(t *testing.T) {
	a := "aaaaaaaa-0000-4000-8000-000000000004"
	b := "bbbbbbbb-0000-4000-8000-000000000004"
	f, s, apps, clock := handDesktop(t, a, b)
	pass(s, clock, inDesktop(a), inDesktop(b))
	apps.set(AppsNow{DesktopKnown: true, DesktopVersion: "2.1.0"})
	pass(s, clock)
	apps.set(AppsNow{DesktopKnown: true, DesktopRunning: true, DesktopVersion: "2.2.0"})
	pass(s, clock, inDesktop(a), inDesktop(b))
	pass(s, clock, inDesktop(a), inDesktop(b))
	if n := countCalls(f, "--bg --resume "); n != 0 {
		t.Fatalf("nothing is brought back: %v", f.r.CallList())
	}
	for _, e := range activity(f) {
		if strings.Contains(e.Message, "Claude Desktop") || strings.Contains(e.Message, "Bringing back") {
			t.Fatalf("nothing was brought back, so nothing is said: %v", activity(f))
		}
	}
}

// Claude Desktop that looked closed and is back with a new version by the
// time its sessions are brought back had restarted for an update: the
// rescues say so, under one line for both.
func TestDesktopBackNewerBeforeTheRescueNamesTheUpdate(t *testing.T) {
	a := "aaaaaaaa-0000-4000-8000-000000000005"
	b := "bbbbbbbb-0000-4000-8000-000000000005"
	f, s, apps, clock := handDesktop(t, a, b)
	pass(s, clock, inDesktop(a), inDesktop(b))
	apps.set(AppsNow{DesktopKnown: true, DesktopVersion: "2.1.0"})
	pass(s, clock)
	apps.set(AppsNow{DesktopKnown: true, DesktopRunning: true, DesktopVersion: "2.2.0"})
	pass(s, clock)
	pass(s, clock, inBackground(a))
	reason := "Claude Desktop restarted for an update from 2.1.0 to 2.2.0"
	rescues, groups := 0, 0
	for _, e := range activity(f) {
		if e.Automatic && e.Reason == reason {
			rescues++
		}
		if e.Message == reason+". Bringing back 2 babysat sessions." {
			groups++
		}
		if strings.Contains(e.Message, "is back as") {
			t.Fatalf("the rescues already named the update: %v", activity(f))
		}
	}
	if rescues != 2 || groups != 1 {
		t.Fatalf("two rescues and one line for both, got %d and %d: %v", rescues, groups, activity(f))
	}
}

// restartFixture is a terminal session babysat by an earlier run of CC
// Babysitter that saw the computer booted at savedBoot, now started again
// with the computer booted at nowBoot, either zero when not known.
func restartFixture(t *testing.T, id string, savedBoot, nowBoot uint64) (*fixture, *Supervisor, *time.Time) {
	t.Helper()
	f := newFixture(t, nil)
	f.r.SetRespond(resumeWithRC(f))
	f.writeTranscript(t, "/home/dev/ws", id)
	if err := f.d.Store.Save(state.State{
		Version:  "test",
		Settings: state.DefaultSettings(),
		BootTime: savedBoot,
		Watches: []state.Watch{{SessionID: id, ShortID: id[:8], Cwd: "/home/dev/ws",
			OriginHost: claude.HostTerminal, PromiseState: "inplace", HasSavedOptions: true}},
	}); err != nil {
		t.Fatal(err)
	}
	f.d.BootTime = func() uint64 { return nowBoot }
	clock := time.Date(2026, 10, 8, 7, 0, 0, 0, time.UTC)
	f.d.Now = func() time.Time { return clock }
	return f, New(*f.d), &clock
}

func rescueReason(t *testing.T, f *fixture) string {
	t.Helper()
	for _, e := range activity(f) {
		if e.Automatic && strings.HasPrefix(e.Message, "resumed as background") {
			return e.Reason
		}
	}
	t.Fatalf("no rescue: %v", activity(f))
	return ""
}

// A babysat session missing when CC Babysitter starts in another boot than
// the one it last ran in went down with the computer. In the same boot, or
// with no boot time saved, its own app gives the reason.
func TestSessionGoneAfterARestartIsNamed(t *testing.T) {
	const earlier, later = 1_790_000_000, 1_790_050_000
	for _, c := range []struct {
		name            string
		savedBoot, boot uint64
		want            string
	}{
		{"another boot", earlier, later, "the computer restarted"},
		{"the same boot", later, later + 1, "its terminal closed, or the session was quit"},
		{"no boot saved", 0, later, "its terminal closed, or the session was quit"},
		{"boot not known", earlier, 0, "its terminal closed, or the session was quit"},
	} {
		t.Run(c.name, func(t *testing.T) {
			id := "cccccccc-0000-4000-8000-000000000003"
			f, s, clock := restartFixture(t, id, c.savedBoot, c.boot)
			pass(s, clock)
			pass(s, clock)
			if got := rescueReason(t, f); got != c.want {
				t.Fatalf("reason %q, want %q", got, c.want)
			}
		})
	}
}

// The boot time CC Babysitter starts in is saved, for the next start to
// tell whether the computer restarted in between.
func TestTheBootTimeIsSavedAtStart(t *testing.T) {
	f, _, _ := restartFixture(t, "cccccccc-0000-4000-8000-000000000006", 1_790_000_000, 1_790_050_000)
	st, err := f.d.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.BootTime != 1_790_050_000 {
		t.Fatalf("saved boot time %d", st.BootTime)
	}
}

// Claude Desktop ends its sessions a moment before it quits, so a first
// look can find it still running. The second look, before the rescue,
// replaces that with what it tells: the app closed, or is back newer.
func TestASecondLookTellsMoreAboutDesktop(t *testing.T) {
	for _, c := range []struct {
		name   string
		second AppsNow
		want   string
	}{
		{"closed by then", AppsNow{DesktopKnown: true, DesktopVersion: "2.1.0"}, "Claude Desktop closed"},
		{"back newer by then", AppsNow{DesktopKnown: true, DesktopRunning: true, DesktopVersion: "2.2.0"}, "Claude Desktop restarted for an update from 2.1.0 to 2.2.0"},
		{"running all along", AppsNow{DesktopKnown: true, DesktopRunning: true, DesktopVersion: "2.1.0"}, "the session ended while Claude Desktop kept running"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := "aaaaaaaa-0000-4000-8000-000000000007"
			f, s, apps, clock := handDesktop(t, a)
			pass(s, clock, inDesktop(a))
			pass(s, clock)
			apps.set(c.second)
			pass(s, clock)
			if got := rescueReason(t, f); got != c.want {
				t.Fatalf("reason %q, want %q", got, c.want)
			}
		})
	}
}
