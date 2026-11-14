package supervise

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// When a watch went to the background is written down the first time its
// copy starts, and a copy started again after it died does not move it.
func TestBackgroundSinceIsSetOnceAndKeptAcrossRestarts(t *testing.T) {
	id := "6f6f6f6f-0000-4000-8000-000000000001"
	f := newFixture(t, nil)
	f.r.SetRespond(func(args []string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case a == "agents --json":
			return "[]", nil
		case strings.HasPrefix(a, "--bg --resume"):
			f.p.SetAlive(11, true)
			f.setFiles(bgSession(11, id, "session_01TESTBRIDGE02"))
			return "backgrounded \u00b7 6f6f6f6f \u00b7 demo-a1", nil
		}
		return "", nil
	})
	clock := time.Date(2026, 9, 25, 8, 46, 0, 0, time.UTC)
	f.d.Now = func() time.Time { return clock }
	f.writeTranscript(t, "/home/dev/ws", id)
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "6f6f6f6f", Name: "demo-a1", Cwd: "/home/dev/ws",
		OriginHost: claude.HostDesktop, PromiseState: "inplace", HasSavedOptions: true})

	at := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	pass := func(i int) { s.reconcileForTest(observe.Snapshot{At: at.Add(time.Duration(i) * time.Second)}) }
	pass(1)
	pass(2)
	w, _ := s.watchForTest(id)
	if w.PromiseState != "fallback" || !w.BackgroundSince.Equal(clock) {
		t.Fatalf("the fallback records when it happened: %+v", w)
	}

	// The copy dies an hour later and is started again.
	started := clock
	clock = clock.Add(time.Hour)
	f.p.SetAlive(11, false)
	f.setFiles()
	// An hour with no pass reads as the computer waking from a sleep, so
	// the passes go on, closer than sleptAfter, until wakeHold is over.
	pass(3)
	clock = clock.Add(wakeHold / 2)
	pass(4)
	clock = clock.Add(wakeHold/2 + time.Second)
	pass(5)
	if n := countCalls(f, "--bg --resume "); n != 2 {
		t.Fatalf("the copy must be started again: %v", f.r.CallList())
	}
	w, _ = s.watchForTest(id)
	if !w.BackgroundSince.Equal(started) {
		t.Fatalf("a restart of the copy keeps the time the watch went to the background: %+v", w)
	}
	if st, _ := f.d.Store.Load(); len(st.Watches) != 1 || !st.Watches[0].BackgroundSince.Equal(started) {
		t.Fatalf("the time is saved: %+v", st.Watches)
	}
}

// A copy found running already sends the watch to the background just as
// a copy it started would, and the time is written down.
func TestBackgroundSinceIsSetWhenARunningCopyIsAdopted(t *testing.T) {
	id := "6f6f6f6f-0000-4000-8000-000000000002"
	f := newFixture(t, func(args []string) (string, error) {
		if strings.Join(args, " ") == "agents --json" {
			return `[{"pid":9,"id":"4d4d4d4d","kind":"background","sessionId":"` + id + `"}]`, nil
		}
		return "", nil
	})
	clock := time.Date(2026, 9, 25, 8, 46, 0, 0, time.UTC)
	f.d.Now = func() time.Time { return clock }
	f.writeTranscript(t, "/home/dev/ws", id)
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "6f6f6f6f", Cwd: "/home/dev/ws",
		OriginHost: claude.HostVSCode, PromiseState: "inplace", HasSavedOptions: true})
	at := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	for i := 1; i <= 2; i++ {
		s.reconcileForTest(observe.Snapshot{At: at.Add(time.Duration(i) * time.Second)})
	}
	if w, _ := s.watchForTest(id); w.PromiseState != "fallback" || !w.BackgroundSince.Equal(clock) {
		t.Fatalf("%+v", w)
	}
}

// A watch that leaves the background forgets when it went there: it is
// back in an app, and the next time it goes is a new time.
func TestBackgroundSinceIsClearedWhenTheWatchLeavesTheBackground(t *testing.T) {
	id := "6f6f6f6f-0000-4000-8000-000000000003"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "6f6f6f6f", Cwd: "/home/dev/ws",
		OriginHost: claude.HostDesktop, PromiseState: "fallback", HasSavedOptions: true,
		BackgroundSince: time.Date(2026, 9, 25, 8, 46, 0, 0, time.UTC)})
	snap := observe.Snapshot{At: time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC),
		Sessions: []claude.Session{{ID: id, ShortID: "6f6f6f6f", PID: 7, Host: claude.HostDesktop, Cwd: "/home/dev/ws"}}}
	s.reconcileForTest(snap)
	if w, _ := s.watchForTest(id); w.PromiseState != "inplace" || !w.BackgroundSince.IsZero() {
		t.Fatalf("%+v", w)
	}
}

// An In background card is told when the watch went there, where it came
// from, the Remote Control address of the copy and the command that
// resumes it in a terminal.
func TestTheViewCarriesWhatTheBackgroundCardNeeds(t *testing.T) {
	id := "6f6f6f6f-0000-4000-8000-000000000004"
	watching := "6f6f6f6f-0000-4000-8000-000000000005"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	f.p.SetAlive(9, true)
	f.p.SetAlive(10, true)
	f.setFiles(bgSession(9, id, "cse_01TESTBRIDGE02"), desktopSession(10, watching))
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()
	since := time.Date(2026, 9, 25, 8, 46, 0, 0, time.FixedZone("EEST", 3*3600))
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "6f6f6f6f", Cwd: "/home/dev/my ws",
		OriginHost: claude.HostDesktop, PromiseState: "fallback", HasSavedOptions: true, BackgroundSince: since})
	s.addWatchForTest(state.Watch{SessionID: watching, ShortID: "6f6f6f6f", Cwd: "/home/dev/ws",
		OriginHost: claude.HostDesktop, PromiseState: "inplace", BackgroundSince: since})

	var bg, app WatchView
	if !waitFor(t, f, func() bool {
		for _, w := range s.View().Watches {
			switch w.SessionID {
			case id:
				bg = w
			case watching:
				app = w
			}
		}
		return bg.State == StateInBackground && app.State == StateWatching
	}) {
		t.Fatalf("%+v %+v", bg, app)
	}
	if bg.BackgroundSince != "2026-09-25T05:46:00Z" || bg.OriginHost != claude.HostDesktop ||
		bg.RemoteURL != "https://claude.ai/code/session_01TESTBRIDGE02" ||
		bg.ResumeCmd != hosts.ResumeCommandIn("/home/dev/my ws", id) {
		t.Fatalf("%+v", bg)
	}
	if app.BackgroundSince != "" || app.RemoteURL != "" {
		t.Fatalf("only an In background card has a time and a Remote Control address: %+v", app)
	}
	raw, err := json.Marshal(bg)
	if err != nil {
		t.Fatal(err)
	}
	// The command's exact form depends on the OS it is rendered for and is
	// tested in hosts; here it only has to reach the page.
	resumeCmd, err := json.Marshal(hosts.ResumeCommandIn("/home/dev/my ws", id))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"backgroundSince":"2026-09-25T05:46:00Z"`, `"originHost":"desktop"`,
		`"remoteUrl":"https://claude.ai/code/session_01TESTBRIDGE02"`, `"resumeCmd":` + string(resumeCmd)} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the view does not carry %s:\n%s", want, raw)
		}
	}
	if strings.Count(string(raw), `"backgroundSince"`) != 1 {
		t.Errorf("backgroundSince is said once: %s", raw)
	}
}

// Open in a terminal attaches to the background copy through the
// launcher, and says what happened in one line either way.
func TestOpenTerminal(t *testing.T) {
	id := "6f6f6f6f-0000-4000-8000-000000000006"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	f.p.SetAlive(9, true)
	f.setFiles(sess(9, id, "background", "cli", "4d4d4d4d"))
	f.d.Obs.RefreshNow()
	var asked []string
	answer := func(string) (string, error) { return "Terminal", nil }
	f.d.OpenTerminal = func(short string) (string, error) {
		asked = append(asked, short)
		return answer(short)
	}
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "4d4d4d4d", Name: "demo-a1", Cwd: "/home/dev/ws",
		OriginHost: claude.HostDesktop, PromiseState: "fallback", HasSavedOptions: true})

	res := s.OpenTerminal(id)
	if !res.OK || res.Message != "Opened Terminal on demo-a1." || strings.Join(asked, " ") != "4d4d4d4d" {
		t.Fatalf("%+v %v", res, asked)
	}

	answer = func(string) (string, error) { return "a terminal", nil }
	if res := s.OpenTerminal(id); !res.OK || res.Message != "Opened a terminal on demo-a1." {
		t.Fatalf("%+v", res)
	}

	answer = func(string) (string, error) { return "", errors.New("no terminal program was found on PATH") }
	res = s.OpenTerminal(id)
	if res.OK || res.Message != "Could not open a terminal: no terminal program was found on PATH. Run claude attach 4d4d4d4d in any terminal." {
		t.Fatalf("%+v", res)
	}
	if len(f.r.CallList()) != 0 {
		t.Fatalf("opening a terminal runs nothing through the CLI: %v", f.r.CallList())
	}
}

// Open in a terminal is refused on a machine with no display, and for a
// session with no background copy running.
func TestOpenTerminalRefuses(t *testing.T) {
	id := "6f6f6f6f-0000-4000-8000-000000000007"
	appOnly := "6f6f6f6f-0000-4000-8000-000000000008"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	f.p.SetAlive(9, true)
	f.p.SetAlive(10, true)
	f.setFiles(sess(9, id, "background", "cli", "4d4d4d4d"), desktopSession(10, appOnly))
	f.d.Obs.RefreshNow()
	calls := 0
	f.d.OpenTerminal = func(string) (string, error) { calls++; return "Terminal", nil }
	headless := false
	f.d.Env = func() hosts.Env {
		return hosts.Env{Platform: "linux", CLIFound: true, CLIPresent: true, Headless: headless}
	}
	s := New(*f.d)

	if res := s.OpenTerminal(appOnly); res.OK || res.Message != "That session has no background copy running." {
		t.Fatalf("%+v", res)
	}
	if res := s.OpenTerminal("6f6f6f6f-0000-4000-8000-000000000009"); res.OK {
		t.Fatalf("%+v", res)
	}
	headless = true
	s = New(*f.d)
	if res := s.OpenTerminal(id); res.OK || res.Message != OpenTerminalRefused {
		t.Fatalf("%+v", res)
	}
	if calls != 0 {
		t.Fatalf("nothing is opened when it is refused, opened %d", calls)
	}
}

// The page's button names what really opens, as the launcher decides it,
// and a machine with no display offers none.
func TestTheViewNamesTheTerminalThatOpens(t *testing.T) {
	for _, headless := range []bool{false, true} {
		f := newFixture(t, func([]string) (string, error) { return "[]", nil })
		f.d.TerminalName = func() string { return "Windows Terminal" }
		f.d.Env = func() hosts.Env {
			return hosts.Env{Platform: "windows", CLIFound: true, CLIPresent: true, Headless: headless}
		}
		s, _, cancel := newSup(t, f)
		want := "Open in Windows Terminal"
		if headless {
			want = ""
		}
		if !waitFor(t, f, func() bool { return s.View().TerminalLabel == want }) {
			t.Errorf("headless %v: %q, want %q", headless, s.View().TerminalLabel, want)
		}
		cancel()
	}
}
