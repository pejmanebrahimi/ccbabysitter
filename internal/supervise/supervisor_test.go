package supervise

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// fakePower counts acquire and release calls. The supervisor loop and the
// test goroutine both touch it, so every field is read under the mutex.
type fakePower struct {
	mu                 sync.Mutex
	acquired, released int
	supported          bool
}

func (p *fakePower) Supported() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.supported
}

func (p *fakePower) Acquire() (func(), bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.supported {
		return nil, false
	}
	p.acquired++
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.released++
	}, true
}

func (p *fakePower) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.acquired, p.released
}

func newSup(t *testing.T, f *fixture) (*Supervisor, *fakePower, context.CancelFunc) {
	return newSupWith(t, f, &fakePower{supported: true})
}

func newSupWith(t *testing.T, f *fixture, pw *fakePower) (*Supervisor, *fakePower, context.CancelFunc) {
	t.Helper()
	f.d.Power = pw
	s := New(*f.d)
	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)
	// The loop writes to the log and to the state folder, and both live in
	// temporary directories the fixture had created for it. Waiting for the
	// loop to finish before those directories are removed is what keeps a
	// write that lands after the test body from tripping the cleanup up.
	t.Cleanup(func() {
		cancel()
		select {
		case <-s.done:
		case <-time.After(10 * time.Second):
			t.Error("the reconcile loop did not stop")
		}
	})
	return s, pw, cancel
}

// waitFor drives the observer and waits until cond holds or a generous
// deadline passes. The snapshot channels hold a single value, so two quick
// refreshes can collapse into one delivery; refreshing on every turn is
// what makes the wait reliable instead of lucky.
func waitFor(t *testing.T, f *fixture, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if cond() {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		f.d.Obs.RefreshNow()
		time.Sleep(10 * time.Millisecond)
	}
}

// firstWatch returns the supervisor's only watch, or false while there is
// none yet.
func firstWatch(s *Supervisor) (WatchView, bool) {
	v := s.View()
	if len(v.Watches) == 0 {
		return WatchView{}, false
	}
	return v.Watches[0], true
}

func TestBabysitInPlaceRecordsOriginAndKeepsHost(t *testing.T) {
	id := "aaaaaaaa-0000-4000-8000-000000000001"
	f := newFixture(t, nil)
	f.p.SetAlive(7, true)
	f.setFiles(sess(7, id, "interactive", "claude-vscode", ""))
	f.d.Obs.RefreshNow()
	s, pw, cancel := newSup(t, f)
	defer cancel()

	res := s.Babysit(id, false, ViaPage)
	if !res.OK {
		t.Fatal(res.Message)
	}
	v := s.View()
	if len(v.Watches) != 1 || v.Watches[0].OriginHost != claude.HostVSCode || v.Watches[0].PromiseState != "inplace" {
		t.Fatalf("%+v", v.Watches)
	}
	if len(f.r.CallList()) != 0 || len(f.p.CallList()) != 0 {
		t.Fatalf("babysit in place must not touch the session: %v %v", f.r.CallList(), f.p.CallList())
	}
	if acquired, _ := pw.counts(); acquired != 1 {
		t.Fatalf("a babysat session keeps the computer awake, acquired %d", acquired)
	}
	st, _ := f.d.Store.Load()
	if len(st.Watches) != 1 {
		t.Fatalf("the watch must be persisted: %+v", st.Watches)
	}
	if len(v.Sessions) != 0 {
		t.Fatalf("a watched session appears only under watches: %+v", v.Sessions)
	}
}

func TestHostDiesThenFallback(t *testing.T) {
	id := "bbbbbbbb-0000-4000-8000-000000000001"
	f := newFixture(t, nil)
	f.r.SetRespond(func(args []string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case a == "agents --json":
			return "[]", nil
		case strings.HasPrefix(a, "--bg --resume"):
			f.p.SetAlive(9, true)
			f.setFiles(sess(9, id, "bg", "cli", "bbbbbbbb"))
			return "backgrounded \u00b7 bbbbbbbb \u00b7 demo-a1", nil
		}
		return "", nil
	})
	f.writeTranscript(t, "/home/dev/ws", id)
	f.p.SetAlive(7, true)
	f.setFiles(sess(7, id, "interactive", "claude-desktop", ""))
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()

	if res := s.Babysit(id, false, ViaPage); !res.OK {
		t.Fatal(res.Message)
	}
	f.p.SetAlive(7, false)

	if !waitFor(t, f, func() bool {
		w, ok := firstWatch(s)
		return ok && w.PromiseState == "fallback" && w.ShortID == "bbbbbbbb"
	}) {
		w, _ := firstWatch(s)
		t.Fatalf("no fallback resume: %+v %v", w, f.r.CallList())
	}
	w, _ := firstWatch(s)
	if !w.HasSavedOptions {
		t.Fatalf("a resumed background copy has saved its own options: %+v", w)
	}
	if !strings.Contains(strings.Join(f.r.CallList(), "\n"), "--bg --resume "+id+" --remote-control") {
		t.Fatalf("the first resume of a never-background session passes flags: %v", f.r.CallList())
	}
	found := false
	for _, e := range f.d.Log.Recent(50, "") {
		if e.Automatic && strings.Contains(e.Reason, "exited") {
			found = true
		}
	}
	if !found {
		t.Fatal("an automatic action must be explained in the activity log")
	}
}

func TestSameSnapshotTwiceCountsOnce(t *testing.T) {
	id := "dddddddd-0000-4000-8000-000000000001"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	s, _, cancel := newSup(t, f)
	defer cancel()

	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "dddddddd", Cwd: "/home/dev/ws", PromiseState: "inplace", HasSavedOptions: true})
	f.d.Obs.RefreshNow()
	snap := f.d.Obs.Current()
	s.reconcileForTest(snap)
	s.reconcileForTest(snap)
	s.reconcileForTest(snap)

	if n := s.absentForTest(id); n != 1 {
		t.Fatalf("three passes over one snapshot are one absence, got %d", n)
	}
	if strings.Contains(strings.Join(f.r.CallList(), "\n"), "--bg") {
		t.Fatalf("one absence is never enough to resume: %v", f.r.CallList())
	}
}

func TestBackoffAndPauseAfterThreeFailures(t *testing.T) {
	id := "eeeeeeee-0000-4000-8000-000000000001"
	f := newFixture(t, func(args []string) (string, error) {
		if strings.Join(args, " ") == "agents --json" {
			return "[]", nil
		}
		return "error: nope", nil
	})
	f.writeTranscript(t, "/home/dev/ws", id)
	f.d.Backoff = time.Millisecond
	s, _, cancel := newSup(t, f)
	defer cancel()

	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "eeeeeeee", Cwd: "/home/dev/ws", PromiseState: "inplace", HasSavedOptions: true})

	if !waitFor(t, f, func() bool {
		w, ok := firstWatch(s)
		return ok && w.Paused
	}) {
		w, _ := firstWatch(s)
		t.Fatalf("three failures in the window must pause the watch: %+v %v", w, f.r.CallList())
	}
	w, _ := firstWatch(s)
	if len(w.Failures) != 3 {
		t.Fatalf("%+v", w)
	}
	if !strings.Contains(w.PauseReason, "three failed resumes") {
		t.Fatalf("the pause must say why: %q", w.PauseReason)
	}
	if n := strings.Count(strings.Join(f.r.CallList(), "\n"), "--bg --resume"); n != 3 {
		t.Fatalf("exactly three attempts, got %d: %v", n, f.r.CallList())
	}

	// Resuming the watch clears the failures and lets it try again.
	if res := s.ResumeWatch(id, ViaPage); !res.OK {
		t.Fatal(res.Message)
	}
	w, _ = firstWatch(s)
	if w.Paused || len(w.Failures) != 0 {
		t.Fatalf("%+v", w)
	}
}

// The keep-awake request follows the watches and nothing else: a machine
// with a babysat session stays awake, and one with none follows its own
// settings again.
func TestKeepAwakeFollowsTheWatches(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555540"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	f.p.SetAlive(7, true)
	f.setFiles(sess(7, id, "interactive", "cli", ""))
	f.d.Obs.RefreshNow()
	s, pw, cancel := newSup(t, f)
	defer cancel()

	if v := s.View(); v.KeepAwake.Held || v.KeepAwake.ActiveWatches != 0 {
		t.Fatalf("nothing is babysat yet: %+v", v.KeepAwake)
	}
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "11111111", Cwd: "/home/dev/ws",
		OriginHost: claude.HostTerminal, PromiseState: "inplace", HasSavedOptions: true})
	v := s.View()
	if !v.KeepAwake.Held || v.KeepAwake.ActiveWatches != 1 || v.KeepAwake.Mode != "babysitting" {
		t.Fatalf("%+v", v.KeepAwake)
	}
	if acquired, _ := pw.counts(); acquired != 1 {
		t.Fatalf("the request is taken once, acquired %d", acquired)
	}

	if res := s.Unbabysit(id, ViaPage); !res.OK {
		t.Fatal(res.Message)
	}
	v = s.View()
	if v.KeepAwake.Held || v.KeepAwake.ActiveWatches != 0 {
		t.Fatalf("%+v", v.KeepAwake)
	}
	if _, released := pw.counts(); released != 1 {
		t.Fatalf("the request is released once, released %d", released)
	}
}

// A paused watch is not keeping anything alive, and the indicator must not
// claim a count it does not have.
func TestAPausedWatchKeepsNothingAwake(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555541"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	s, pw, cancel := newSup(t, f)
	defer cancel()

	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "11111111", Cwd: "/home/dev/ws",
		PromiseState: "paused", Paused: true, PauseReason: "three failed resumes in five minutes: exit 1"})
	v := s.View()
	if v.KeepAwake.Held || v.KeepAwake.ActiveWatches != 0 {
		t.Fatalf("%+v", v.KeepAwake)
	}
	if acquired, _ := pw.counts(); acquired != 0 {
		t.Fatalf("nothing to keep awake, acquired %d", acquired)
	}
}

// A keep-awake answer that arrives in the settings is not a choice: the
// rule is fixed, and the settings that do mean something are still saved.
func TestSettingsCannotChangeTheKeepAwakeRule(t *testing.T) {
	f := newFixture(t, nil)
	s, _, cancel := newSup(t, f)
	defer cancel()

	next := s.View().Settings
	next.KeepAwakeMode = "off"
	next.Theme = "auto"
	if res := s.SetSettings(next, ViaPage); !res.OK {
		t.Fatal(res.Message)
	}
	v := s.View()
	if v.Settings.KeepAwakeMode != "babysitting" || v.KeepAwake.Mode != "babysitting" {
		t.Fatalf("%+v", v.Settings)
	}
	if v.Settings.Theme != "auto" {
		t.Fatalf("the settings that are a choice must still be saved: %+v", v.Settings)
	}
	st, _ := f.d.Store.Load()
	if st.Settings.KeepAwakeMode != "babysitting" {
		t.Fatalf("the saved file must carry the rule: %+v", st.Settings)
	}
	for _, theme := range []string{"light", "dark"} {
		next.Theme = theme
		if res := s.SetSettings(next, ViaPage); !res.OK || s.View().Settings.Theme != theme {
			t.Fatalf("%s: %+v", theme, res)
		}
	}
	next.Theme = "system"
	if res := s.SetSettings(next, ViaPage); res.OK || res.Message != "Theme must be dark, light or auto." {
		t.Fatalf("the old Follow system value is refused: %+v", res)
	}
}

// A platform that cannot hold the request at all is said once, not on every
// pass, and the indicator still reports what is being watched.
func TestKeepAwakeUnsupportedIsReportedOnce(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555542"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	// The session stays live in its own host for the whole test, so the
	// watch is active on every pass and every pass asks for the request
	// again.
	f.p.SetAlive(7, true)
	f.setFiles(sess(7, id, "interactive", "cli", ""))
	f.d.Obs.RefreshNow()
	s, _, cancel := newSupWith(t, f, &fakePower{supported: false})
	defer cancel()

	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "11111111", Cwd: "/home/dev/ws",
		OriginHost: claude.HostTerminal, PromiseState: "inplace", HasSavedOptions: true})
	for i := 0; i < 3; i++ {
		f.d.Obs.RefreshNow()
		s.reconcileForTest(f.d.Obs.Current())
	}
	v := s.View()
	if v.KeepAwake.Supported || v.KeepAwake.Held {
		t.Fatalf("%+v", v.KeepAwake)
	}
	if v.KeepAwake.Mode != "babysitting" || v.KeepAwake.ActiveWatches != 1 {
		t.Fatalf("the indicator still says what is watched: %+v", v.KeepAwake)
	}
	n := 0
	for _, e := range f.d.Log.Recent(100, "") {
		if strings.Contains(e.Message, "keeping the computer awake is not supported") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("an unsupported platform is reported once, not every pass: %d", n)
	}
}

func TestBabysitRefusesOtherHost(t *testing.T) {
	id := "52525252-0000-4000-8000-000000000001"
	f := newFixture(t, func([]string) (string, error) { return "ok", nil })
	f.p.SetAlive(9, true)
	f.setFiles(sess(9, id, "interactive", "some-other-program", ""))
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()

	if res := s.Babysit(id, false, ViaPage); res.OK || !strings.Contains(res.Message, "another program") {
		t.Fatalf("%+v", res)
	}
	if len(f.r.CallList()) != 0 || len(f.p.CallList()) != 0 {
		t.Fatalf("another program's session is never touched: %v %v", f.r.CallList(), f.p.CallList())
	}
	v := s.View()
	if len(v.Sessions) != 1 || v.Sessions[0].Actionable {
		t.Fatalf("a session of another program is shown but not actionable: %+v", v.Sessions)
	}
}

func TestAutoBabysitSkipsSessionsPresentAtStart(t *testing.T) {
	early := "54545454-0000-4000-8000-000000000001"
	late := "55555555-0000-4000-8000-000000000001"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	f.d.Env = func() hosts.Env {
		return hosts.Env{Platform: "linux", CLIFound: true, CLIPresent: true, Headless: true}
	}
	f.p.SetAlive(9, true)
	f.setFiles(bgSession(9, early, "b"))
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()

	// One reconcile over the starting snapshot, so every session already
	// running when the program started is known before the setting is on.
	s.reconcileForTest(f.d.Obs.Current())

	st := s.View().Settings
	st.AutoBabysit = true
	if res := s.SetSettings(st, ViaPage); !res.OK {
		t.Fatal(res.Message)
	}

	f.p.SetAlive(11, true)
	f.addFile(bgSession(11, late, "b"))
	if !waitFor(t, f, func() bool { return len(s.View().Watches) == 1 }) {
		t.Fatalf("a session that appears later must be adopted: %+v", s.View().Watches)
	}
	w, _ := firstWatch(s)
	if w.SessionID != late {
		t.Fatalf("only the session that appeared later is adopted: %+v", w)
	}
	found := false
	for _, e := range f.d.Log.Recent(50, "") {
		if e.Automatic && e.Reason == "server auto-babysit" {
			found = true
		}
	}
	if !found {
		t.Fatal("adopting a session is an automatic action and must be explained")
	}
}

// On a server, babysitting every new background session is on from the
// first start, with nothing to switch on; once it is switched off, a new
// session is left alone.
func TestAutoBabysitIsOnByDefaultAndCanBeSwitchedOff(t *testing.T) {
	for _, on := range []bool{true, false} {
		early := "58585858-0000-4000-8000-000000000001"
		late := "59595959-0000-4000-8000-000000000001"
		f := newFixture(t, func([]string) (string, error) { return "[]", nil })
		f.d.Env = func() hosts.Env {
			return hosts.Env{Platform: "linux", CLIFound: true, CLIPresent: true, Headless: true}
		}
		f.p.SetAlive(9, true)
		f.setFiles(bgSession(9, early, "b"))
		f.d.Obs.RefreshNow()
		s, _, cancel := newSup(t, f)

		s.reconcileForTest(f.d.Obs.Current())
		st := s.View().Settings
		if !st.AutoBabysit {
			cancel()
			t.Fatalf("a fresh start must have it on: %+v", st)
		}
		if !on {
			st.AutoBabysit = false
			if res := s.SetSettings(st, ViaPage); !res.OK {
				cancel()
				t.Fatal(res.Message)
			}
		}

		f.p.SetAlive(11, true)
		f.addFile(bgSession(11, late, "b"))
		if !waitFor(t, f, func() bool { return len(f.d.Obs.Current().Sessions) == 2 }) {
			cancel()
			t.Fatal("the new session was never seen")
		}
		s.reconcileForTest(f.d.Obs.Current())
		adopted := len(s.View().Watches) == 1
		cancel()
		if adopted != on {
			t.Fatalf("on=%v: adopted=%v %+v", on, adopted, s.View().Watches)
		}
	}
}

func TestTotalsUseUnionOfTrees(t *testing.T) {
	a := "56565656-0000-4000-8000-000000000001"
	b := "57575757-0000-4000-8000-000000000001"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	f.p.SetAlive(7, true)
	f.p.SetAlive(9, true)
	f.p.SetStats(7, procs.TreeStats{CPUPercent: 10, RSS: 100, Processes: 2, PIDs: []int{7, 8}})
	f.p.SetStats(9, procs.TreeStats{CPUPercent: 5, RSS: 50, Processes: 2, PIDs: []int{8, 9}})
	f.setFiles(sess(7, a, "interactive", "cli", ""), bgSession(9, b, ""))
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()

	s.reconcileForTest(f.d.Obs.Current())
	tot := s.View().Totals
	if tot.Sessions != 2 || tot.Processes != 3 {
		t.Fatalf("a process in two trees counts once: %+v", tot)
	}
	if tot.CPUPercent != 15 || tot.RSSBytes != 150 {
		t.Fatalf("%+v", tot)
	}
	if tot.ByHost[claude.HostTerminal] != 1 || tot.ByHost[claude.HostBackground] != 1 {
		t.Fatalf("%+v", tot.ByHost)
	}
	if tot.RemoteControlled != 1 {
		t.Fatalf("only the terminal session has remote control on: %+v", tot)
	}
}

func TestViewNeverEntersTheLoop(t *testing.T) {
	id := "58585858-0000-4000-8000-000000000001"
	f := newFixture(t, nil)
	f.d.Env = func() hosts.Env {
		return hosts.Env{Platform: "linux", CLIFound: true, CLIPresent: true, Headless: true}
	}
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	defer release()
	f.r.SetRespond(func(args []string) (string, error) {
		if strings.HasPrefix(strings.Join(args, " "), "stop ") {
			<-gate
		}
		return "", nil
	})
	f.p.SetAlive(9, true)
	f.setFiles(bgSession(9, id, "b"))
	f.d.Obs.RefreshNow()

	// The notifier has to be in place before the supervisor is built, so it
	// reaches it through a holder. New does not notify, but the loop can
	// publish its first reconcile before the holder is filled in, which is
	// why the callback tolerates an empty one.
	var supRef atomic.Pointer[Supervisor]
	var notified sync.WaitGroup
	notified.Add(1)
	var seen sync.Once
	f.d.Notify = func() {
		// The notifier reads the published view. Doing so must never wait
		// on the loop that is publishing it.
		sp := supRef.Load()
		if sp == nil {
			return
		}
		_ = sp.View().Version
		seen.Do(notified.Done)
	}
	s, _, cancel := newSup(t, f)
	defer cancel()
	supRef.Store(s)

	s.ResumeWatch(id, ViaPage)
	waitDone := make(chan struct{})
	go func() { notified.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(3 * time.Second):
		t.Fatal("a notifier that reads the view must not deadlock")
	}

	done := make(chan Result, 1)
	go func() { done <- s.Stop(id, ViaPage) }()

	// Wait until the action is inside the blocked CLI call.
	if !waitFor(t, f, func() bool {
		for _, c := range f.r.CallList() {
			if strings.HasPrefix(c, "stop ") {
				return true
			}
		}
		return false
	}) {
		t.Fatal("the action never reached the runner")
	}

	start := time.Now()
	_ = s.View()
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("the view must not wait for a slow action: %v", d)
	}
	release()
	select {
	case res := <-done:
		if !res.OK {
			t.Fatal(res.Message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the action never finished")
	}
}

func TestViewJSONIsCamelCaseAndNeverNull(t *testing.T) {
	f := newFixture(t, nil)
	s, _, cancel := newSup(t, f)
	defer cancel()

	data, err := json.Marshal(s.View())
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`"sessions":[]`, `"watches":[]`, `"notRunning":[]`, `"keepAwake"`, `"autostartSupported"`, `"url"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
	if strings.Contains(text, "null") {
		t.Fatalf("a published view never carries a null: %s", text)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	for k := range top {
		if unicode.IsUpper(rune(k[0])) {
			t.Fatalf("top level key %q is not camelCase", k)
		}
	}
}

func TestViewCarriesTheAddressAndUnknownSessionsAreRefused(t *testing.T) {
	f := newFixture(t, nil)
	f.d.URL = "http://127.0.0.1:47391"
	s, _, cancel := newSup(t, f)
	defer cancel()

	v := s.View()
	if v.URL != "http://127.0.0.1:47391" {
		t.Fatalf("%+v", v)
	}
	if res := s.Babysit("59595959-0000-4000-8000-000000000001", false, ViaPage); res.OK || !strings.Contains(res.Message, "not running") {
		t.Fatalf("%+v", res)
	}
	if res := s.Unbabysit("59595959-0000-4000-8000-000000000001", ViaPage); res.OK {
		t.Fatal("unbabysitting a session that is not watched must refuse")
	}
}

func TestAutoBabysitIgnoresTheEmptySnapshotBeforeTheFirstRead(t *testing.T) {
	early := "64646464-0000-4000-8000-000000000001"
	late := "65656565-0000-4000-8000-000000000001"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	f.d.Env = func() hosts.Env {
		return hosts.Env{Platform: "linux", CLIFound: true, CLIPresent: true, Headless: true}
	}
	settings := state.DefaultSettings()
	settings.AutoBabysit = true
	if err := f.d.Store.Save(state.State{Settings: settings}); err != nil {
		t.Fatal(err)
	}

	// The supervisor starts before the observer has ever read the session
	// files, so its first reconcile sees an empty snapshot.
	s, _, cancel := newSup(t, f)
	defer cancel()

	f.p.SetAlive(9, true)
	f.setFiles(bgSession(9, early, "b"))
	if !waitFor(t, f, func() bool { return len(s.View().Sessions) == 1 }) {
		t.Fatal("the first real snapshot was never reconciled")
	}
	if n := len(s.View().Watches); n != 0 {
		t.Fatalf("a session present in the first real snapshot is not adopted: %d", n)
	}

	f.p.SetAlive(11, true)
	f.addFile(bgSession(11, late, "b"))
	if !waitFor(t, f, func() bool { return len(s.View().Watches) == 1 }) {
		t.Fatalf("a session that appears later must be adopted: %+v", s.View().Watches)
	}
	if w, _ := firstWatch(s); w.SessionID != late {
		t.Fatalf("%+v", w)
	}
}

// A babysat session that closed before anything was said in it has no
// saved conversation, so there is nothing to start again. Its babysitting
// ends, with the reason in the activity log, rather than sitting Stuck
// behind a Try again that could never work, and the keep-awake count
// follows at once.
func TestASessionEndedBeforeAnythingWasSaidIsNoLongerBabysat(t *testing.T) {
	empty := "66666666-0000-4000-8000-000000000002"
	busy := "66666666-0000-4000-8000-000000000003"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	f.p.SetAlive(7, true)
	f.setFiles(sess(7, busy, "interactive", "cli", ""))
	f.d.Obs.RefreshNow()
	s, pw, cancel := newSup(t, f)
	defer cancel()

	s.addWatchForTest(state.Watch{SessionID: busy, ShortID: "66666666", Cwd: "/home/dev/ws",
		OriginHost: claude.HostTerminal, PromiseState: "inplace"})
	// No conversation is written for this one: nothing was said in it.
	s.addWatchForTest(state.Watch{SessionID: empty, ShortID: "66666666", Name: "quiet-one", Cwd: "/home/dev/ws",
		OriginHost: claude.HostTerminal, PromiseState: "inplace"})

	if !waitFor(t, f, func() bool { return len(s.View().Watches) == 1 }) {
		t.Fatalf("the empty session is no longer babysat: %+v", s.View().Watches)
	}
	v := s.View()
	if v.Watches[0].SessionID != busy || v.Totals.Babysat != 1 {
		t.Fatalf("only the session with something in it is left: %+v", v.Watches)
	}
	if !v.KeepAwake.Held || v.KeepAwake.ActiveWatches != 1 {
		t.Fatalf("the keep-awake count follows: %+v", v.KeepAwake)
	}
	if acquired, released := pw.counts(); acquired != 1 || released != 0 {
		t.Fatalf("the request is held throughout: acquired %d released %d", acquired, released)
	}
	if strings.Contains(strings.Join(f.r.CallList(), "\n"), "--bg --resume") {
		t.Fatalf("nothing may be resumed: %v", f.r.CallList())
	}
	found := 0
	for _, e := range f.d.Log.Recent(50, "") {
		if e.Automatic && e.Reason == EmptyReason && e.Session == "quiet-one" &&
			e.Message == "quiet-one ended before anything was said in it, so there is nothing to bring back." {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("ending the babysitting is an automatic action and is explained once: %+v", f.d.Log.Recent(50, ""))
	}
	for _, w := range s.View().Watches {
		if w.Paused {
			t.Fatalf("nothing is left Stuck: %+v", w)
		}
	}
}

// A look for the saved conversation that could not be finished, such as
// one stopped by a folder that cannot be read, never ends babysitting and
// says nothing: the watch is looked at again on the next pass, and once
// the folder can be read the usual rule applies.
func TestAnUnfinishedLookForTheConversationLeavesTheWatchAlone(t *testing.T) {
	id := "66666666-0000-4000-8000-000000000007"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	lockProjects(t, f)
	// The loop is not started: every pass here is one this test drives.
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "66666666", Name: "locked-one", Cwd: "/home/dev/ws",
		OriginHost: claude.HostTerminal, PromiseState: "inplace"})

	at := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	pass := 0
	next := func() {
		pass++
		s.reconcileForTest(observe.Snapshot{At: at.Add(time.Duration(pass) * time.Second)})
	}
	for i := 0; i < AbsentSweepsRequired+3; i++ {
		next()
	}
	if v := s.View(); len(v.Watches) != 1 || v.Watches[0].Paused {
		t.Fatalf("the watch is left as it is: %+v", v.Watches)
	}
	for _, e := range f.d.Log.Recent(50, "") {
		if e.Session == "locked-one" {
			t.Fatalf("nothing is said about it: %+v", e)
		}
	}
	if strings.Contains(strings.Join(f.r.CallList(), "\n"), "--resume") {
		t.Fatalf("nothing is resumed: %v", f.r.CallList())
	}

	// Once the folder can be read, the look is finished and the usual
	// rule applies: nothing saved ends it.
	if err := os.Chmod(f.d.ProjectsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	next()
	if v := s.View(); len(v.Watches) != 0 {
		t.Fatalf("with the folder readable, nothing saved ends it: %+v", v.Watches)
	}
}

// An older pause is not settled on a look that could not be finished
// either: it stays as it is until the look can be finished.
func TestAnOlderPauseWaitsForAFinishedLook(t *testing.T) {
	id := "66666666-0000-4000-8000-000000000008"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	lockProjects(t, f)
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "66666666", Name: "old-pause", Cwd: "/home/dev/ws",
		OriginHost: claude.HostTerminal, PromiseState: "inplace", Paused: true, PauseReason: "transcript missing"})

	at := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	s.reconcileForTest(observe.Snapshot{At: at})
	if v := s.View(); len(v.Watches) != 1 || !v.Watches[0].Paused {
		t.Fatalf("the pause is left as it is: %+v", v.Watches)
	}
	for _, e := range f.d.Log.Recent(50, "") {
		if e.Session == "old-pause" {
			t.Fatalf("nothing is said about it: %+v", e)
		}
	}

	if err := os.Chmod(f.d.ProjectsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s.reconcileForTest(observe.Snapshot{At: at.Add(time.Second)})
	if v := s.View(); len(v.Watches) != 0 {
		t.Fatalf("with the folder readable, nothing saved ends it: %+v", v.Watches)
	}
}

// When the last babysat session ends that way, the keep-awake request is
// let go.
func TestTheLastEmptySessionEndingLetsTheComputerSleep(t *testing.T) {
	id := "66666666-0000-4000-8000-000000000004"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	s, pw, cancel := newSup(t, f)
	defer cancel()

	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "66666666", Cwd: "/home/dev/ws",
		OriginHost: claude.HostTerminal, PromiseState: "inplace"})
	if !waitFor(t, f, func() bool { return len(s.View().Watches) == 0 }) {
		t.Fatalf("%+v", s.View().Watches)
	}
	if !waitFor(t, f, func() bool {
		v := s.View()
		return !v.KeepAwake.Held && v.KeepAwake.ActiveWatches == 0
	}) {
		t.Fatalf("%+v", s.View().KeepAwake)
	}
	if acquired, released := pw.counts(); acquired != released {
		t.Fatalf("whatever was taken is let go: acquired %d released %d", acquired, released)
	}
}

// A watch an earlier version paused for having no saved conversation is
// settled the way today's rule would: ended when its session is still
// running nowhere with nothing saved, watched again when it runs.
func TestAnOlderPauseForNoConversationIsSettled(t *testing.T) {
	gone := "66666666-0000-4000-8000-000000000005"
	back := "66666666-0000-4000-8000-000000000006"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	f.p.SetAlive(7, true)
	f.setFiles(sess(7, back, "interactive", "cli", ""))
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()

	for _, id := range []string{gone, back} {
		s.addWatchForTest(state.Watch{SessionID: id, ShortID: id[:8], Cwd: "/home/dev/ws",
			OriginHost: claude.HostTerminal, PromiseState: "inplace", Paused: true, PauseReason: "transcript missing"})
	}
	if !waitFor(t, f, func() bool {
		v := s.View()
		return len(v.Watches) == 1 && !v.Watches[0].Paused
	}) {
		t.Fatalf("%+v", s.View().Watches)
	}
	w, _ := firstWatch(s)
	if w.SessionID != back || w.State != StateWatching || w.PauseReason != "" {
		t.Fatalf("the running one is watched again: %+v", w)
	}
	reasons := map[string]bool{}
	again := false
	for _, e := range f.d.Log.Recent(50, "") {
		if e.Automatic {
			reasons[e.Reason] = true
		}
		if e.Automatic && e.Reason == "the session is running again" && e.Message == "babysitting it again" {
			again = true
		}
	}
	if !reasons[EmptyReason] || !reasons["the session is running again"] {
		t.Fatalf("both are explained: %v", reasons)
	}
	if !again {
		t.Fatalf("the running one says, in plain words, that it is babysat again: %+v", f.d.Log.Recent(50, ""))
	}
}

// dedupeFixture builds a watch whose session has two live background
// copies, and a fake CLI whose stop really stops the copy it names.
func dedupeFixture(t *testing.T, id, watchShort string) (*fixture, *Supervisor, context.CancelFunc) {
	t.Helper()
	f := newFixture(t, nil)
	f.r.SetRespond(func(args []string) (string, error) {
		a := strings.Join(args, " ")
		switch a {
		case "stop 0a0b0c0d", "rm 0a0b0c0d":
			f.p.SetAlive(11, false)
		case "stop 61616161", "rm 61616161":
			f.p.SetAlive(9, false)
		}
		return "ok", nil
	})
	f.p.SetAlive(9, true)
	f.p.SetAlive(11, true)
	f.setFiles(
		sess(9, id, "bg", "cli", "61616161"),
		sess(11, id, "bg", "cli", "0a0b0c0d"),
	)
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: watchShort, Cwd: "/home/dev/ws", PromiseState: "fallback", HasSavedOptions: true})
	return f, s, cancel
}

func TestDedupeKeepsTheCopyTheWatchKnows(t *testing.T) {
	id := "61616161-0000-4000-8000-000000000001"
	f, s, cancel := dedupeFixture(t, id, "61616161")
	defer cancel()

	// The copy goes the moment it is asked to stop, while the removal and
	// the sentence about it come just after, so the wait is for the last of
	// them rather than the first.
	if !waitFor(t, f, func() bool { return countMessages(f, "stopped and removed 0a0b0c0d") >= 1 }) {
		t.Fatalf("the extra copy must be stopped, removed and explained: %v", f.r.CallList())
	}
	// More passes over what the observer last saw change nothing: a copy
	// is dealt with once.
	for i := 0; i < 3; i++ {
		s.reconcileForTest(f.d.Obs.Current())
	}
	if n := countMessages(f, "stopped and removed 0a0b0c0d"); n != 1 {
		t.Fatalf("the clean up is said once, got %d lines", n)
	}
	if countCalls(f, "stop 0a0b0c0d") != 1 || countCalls(f, "rm 0a0b0c0d") != 1 {
		t.Fatalf("an extra copy is stopped and removed once: %v", f.r.CallList())
	}
	calls := strings.Join(f.r.CallList(), "\n")
	if !strings.Contains(calls, "stop 0a0b0c0d") || !strings.Contains(calls, "rm 0a0b0c0d") {
		t.Fatalf("an extra copy is stopped and removed: %v", f.r.CallList())
	}
	if strings.Contains(calls, "stop 61616161") || strings.Contains(calls, "rm 61616161") {
		t.Fatalf("the copy the watch knows must be left alone: %v", f.r.CallList())
	}
	w, _ := firstWatch(s)
	if w.ShortID != "61616161" || w.Paused {
		t.Fatalf("%+v", w)
	}
	found := false
	for _, e := range f.d.Log.Recent(50, "") {
		if e.Automatic && strings.Contains(e.Reason, "more than one background copy") {
			found = true
		}
	}
	if !found {
		t.Fatal("removing a copy is an automatic action and must be explained")
	}
}

// An app session with no job id of its own is known by the first eight
// characters of the session id, which is also the short id our resumed copy
// was given. Cleaning up an extra copy beside it must only ever touch
// background copies, and never a short id an app session answers to.
func TestDedupeNeverTouchesTheShortIDAnAppAnswersTo(t *testing.T) {
	id := "61616161-0000-4000-8000-000000000003"
	f := newFixture(t, func([]string) (string, error) { return "ok", nil })
	f.p.SetAlive(5, true)
	f.p.SetAlive(9, true)
	f.p.SetAlive(11, true)
	f.setFiles(
		sess(5, id, "interactive", "claude-desktop", ""),
		sess(9, id, "bg", "cli", "61616161"),
		sess(11, id, "bg", "cli", "0a0b0c0d"),
	)
	f.d.Obs.RefreshNow()
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "61616161", Cwd: "/home/dev/ws",
		OriginHost: claude.HostDesktop, PromiseState: "fallback", HasSavedOptions: true})

	for i := 0; i < 3; i++ {
		f.d.Obs.RefreshNow()
		s.reconcileForTest(f.d.Obs.Current())
	}
	if n := countCalls(f, "stop 61616161") + countCalls(f, "rm 61616161"); n != 0 {
		t.Fatalf("the short id the app and our copy share must never be stopped or removed: %v", f.r.CallList())
	}
	calls := f.r.CallList()
	if len(calls) != 2 || calls[0] != "stop 0a0b0c0d" || calls[1] != "rm 0a0b0c0d" {
		t.Fatalf("only the extra copy is stopped and removed, once: %v", calls)
	}
	if w, _ := s.watchForTest(id); w.ShortID != "61616161" {
		t.Fatalf("the watch keeps its own copy: %+v", w)
	}
}

// A background copy whose short id an app session also answers to is never
// named in a command, even when it is the extra one: the command could
// reach the app's session instead.
func TestDedupeLeavesACopyThatSharesAnAppsShortID(t *testing.T) {
	id := "61616161-0000-4000-8000-000000000004"
	f := newFixture(t, func([]string) (string, error) { return "ok", nil })
	f.p.SetAlive(5, true)
	f.p.SetAlive(9, true)
	f.p.SetAlive(11, true)
	f.setFiles(
		sess(5, id, "interactive", "claude-desktop", ""),
		sess(9, id, "bg", "cli", "61616161"),
		sess(11, id, "bg", "cli", "0a0b0c0d"),
	)
	f.d.Obs.RefreshNow()
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "0a0b0c0d", Cwd: "/home/dev/ws",
		OriginHost: claude.HostDesktop, PromiseState: "fallback", HasSavedOptions: true})

	for i := 0; i < 3; i++ {
		f.d.Obs.RefreshNow()
		s.reconcileForTest(f.d.Obs.Current())
	}
	if calls := f.r.CallList(); len(calls) != 0 {
		t.Fatalf("nothing may run: %v", calls)
	}
	if n := countMessages(f, "cannot use that id safely"); n != 1 {
		t.Fatalf("the copy left running is named once, got %d", n)
	}
}

func TestDedupeAdoptsAKeptCopyWhenNoneMatches(t *testing.T) {
	id := "61616161-0000-4000-8000-000000000002"
	f, s, cancel := dedupeFixture(t, id, "99999999")
	defer cancel()

	if !waitFor(t, f, func() bool {
		w, ok := firstWatch(s)
		return ok && w.ShortID != "99999999"
	}) {
		w, _ := firstWatch(s)
		t.Fatalf("the kept copy's short id must be recorded: %+v %v", w, f.r.CallList())
	}
	w, _ := firstWatch(s)
	if w.ShortID != "0a0b0c0d" {
		t.Fatalf("the first copy is the one kept: %+v", w)
	}
	calls := strings.Join(f.r.CallList(), "\n")
	if !strings.Contains(calls, "stop 61616161") || !strings.Contains(calls, "rm 61616161") {
		t.Fatalf("the other copy is stopped and removed: %v", f.r.CallList())
	}
	if strings.Contains(calls, "stop 0a0b0c0d") {
		t.Fatalf("the kept copy must be left alone: %v", f.r.CallList())
	}
	if w.Paused {
		t.Fatalf("%+v", w)
	}
}

func TestPublishedViewOwnsItsSlices(t *testing.T) {
	id := "67676767-0000-4000-8000-000000000001"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	s, _, cancel := newSup(t, f)
	defer cancel()

	failed := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "67676767", Cwd: "/home/dev/ws", PromiseState: "inplace", Paused: true, Failures: []time.Time{failed}})

	v := s.View()
	if len(v.Watches[0].Failures) != 1 {
		t.Fatalf("%+v", v.Watches[0])
	}
	v.Watches[0].Failures[0] = failed.Add(time.Hour)

	// Any action publishes a fresh view; one about a session nobody is
	// watching changes nothing else.
	s.ResumeWatch("67676767-0000-4000-8000-00000000000f", ViaPage)
	next := s.View()
	if !next.Watches[0].Failures[0].Equal(failed) {
		t.Fatalf("changing published failure times must not reach the supervisor: %v", next.Watches[0].Failures)
	}
}

func TestTotalsSkipNestedSessions(t *testing.T) {
	parent := "68686868-0000-4000-8000-000000000001"
	child := "69696969-0000-4000-8000-000000000001"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	f.p.SetAlive(7, true)
	f.p.SetAlive(8, true)
	f.p.SetStats(7, procs.TreeStats{CPUPercent: 10, RSS: 100, Processes: 2, PIDs: []int{7, 8}})
	f.p.SetStats(8, procs.TreeStats{CPUPercent: 4, RSS: 40, Processes: 1, PIDs: []int{8}})
	f.setFiles(sess(7, parent, "interactive", "cli", ""), bgSession(8, child, ""))
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()

	s.reconcileForTest(f.d.Obs.Current())
	tot := s.View().Totals
	if tot.Processes != 2 {
		t.Fatalf("the union of both trees is two processes: %+v", tot)
	}
	if tot.CPUPercent != 10 || tot.RSSBytes != 100 {
		t.Fatalf("a session inside another session's tree is already counted: %+v", tot)
	}
}

// A pass resumes one absent session and no more: a machine that has just
// woken up can find every watch absent at once, and starting them all in
// one pass would hold the loop for as long as every resume takes together.
func TestOnePassResumesOneSessionAndQueuesTheRest(t *testing.T) {
	ids := []string{
		"71717171-0000-4000-8000-000000000001",
		"72727272-0000-4000-8000-000000000001",
		"73737373-0000-4000-8000-000000000001",
	}
	f := newFixture(t, nil)
	var mu sync.Mutex
	resumed := map[string]bool{}
	f.r.SetRespond(func(args []string) (string, error) {
		a := strings.Join(args, " ")
		if a == "agents --json" {
			return "[]", nil
		}
		if strings.HasPrefix(a, "--bg --resume ") {
			id := strings.Fields(a)[2]
			mu.Lock()
			resumed[id] = true
			pid := 90 + len(resumed)
			mu.Unlock()
			// A resume that worked leaves a live background session behind,
			// so the watch stops being absent and is not tried again.
			f.p.SetAlive(pid, true)
			f.addFile(bgSession(pid, id, "b"))
			return "backgrounded \u00b7 " + id[:8] + " \u00b7 demo-a1", nil
		}
		return "", nil
	})
	for _, id := range ids {
		f.writeTranscript(t, "/home/dev/ws", id)
	}
	// The loop is deliberately not started: every pass here is one this
	// test drove, so counting them means something.
	s := New(*f.d)

	for i, id := range ids {
		s.addWatchForTest(state.Watch{SessionID: id, ShortID: []string{"71717171", "72727272", "73737373"}[i],
			Cwd: "/home/dev/ws", PromiseState: "inplace", HasSavedOptions: true})
	}

	resumeCalls := func() int {
		n := 0
		for _, c := range f.r.CallList() {
			if strings.HasPrefix(c, "--bg --resume ") {
				n++
			}
		}
		return n
	}
	pass := func() {
		f.d.Obs.RefreshNow()
		s.reconcile(context.Background(), f.d.Obs.Current())
	}

	// The first pass is the first absent sweep, and one absence is never
	// enough: a fallback waits for two in a row.
	pass()
	if n := resumeCalls(); n != 0 {
		t.Fatalf("one absence is never enough to resume, got %d calls: %v", n, f.r.CallList())
	}

	pass()
	if n := resumeCalls(); n != 1 {
		t.Fatalf("one pass resumes exactly one session, got %d: %v", n, f.r.CallList())
	}
	waitingSaid := 0
	for _, e := range f.d.Log.Recent(50, "") {
		if strings.Contains(e.Message, "waiting to be resumed") {
			waitingSaid++
		}
	}
	if waitingSaid != 1 {
		t.Fatalf("the queue is mentioned once per pass, got %d lines", waitingSaid)
	}

	pass()
	pass()
	if n := resumeCalls(); n != 3 {
		t.Fatalf("three passes resume all three, got %d: %v", n, f.r.CallList())
	}
	mu.Lock()
	defer mu.Unlock()
	for _, id := range ids {
		if !resumed[id] {
			t.Fatalf("%s was never resumed: %+v", id, resumed)
		}
	}
}

// A shutdown between the absence check and the resume is a failure of the
// timing, not of the session. Recording it would let a service that is
// restarted often count its own restarts as failures and pause every watch
// for good.
func TestACancelledPassRecordsNoFailure(t *testing.T) {
	id := "74747474-0000-4000-8000-000000000001"
	f := newFixture(t, func(args []string) (string, error) {
		if strings.Join(args, " ") == "agents --json" {
			return "[]", nil
		}
		return "error: nope", errors.New("exit 1")
	})
	f.writeTranscript(t, "/home/dev/ws", id)

	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "74747474", Cwd: "/home/dev/ws", PromiseState: "inplace", HasSavedOptions: true})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Two passes over two snapshots: enough absence for a fallback, were
	// the pass allowed to act at all.
	f.d.Obs.RefreshNow()
	s.reconcile(ctx, f.d.Obs.Current())
	f.d.Obs.RefreshNow()
	s.reconcile(ctx, f.d.Obs.Current())

	for _, c := range f.r.CallList() {
		if strings.HasPrefix(c, "--bg --resume") {
			t.Fatalf("a cancelled pass must not resume anything: %v", f.r.CallList())
		}
	}
	w, ok := firstWatch(s)
	if !ok {
		t.Fatal("the watch disappeared")
	}
	if len(w.Failures) != 0 || w.Paused {
		t.Fatalf("a cancelled pass records nothing: %+v", w)
	}
}

// A preference that says autostart is on while nothing was written would
// tell the user, every time they look, that something is set up which is
// not.
func TestAutostartFailureRevertsTheStoredPreference(t *testing.T) {
	f := newFixture(t, nil)
	var order []string
	f.d.Autostart = func(enable bool) (string, error) {
		st, ok := f.d.Store.Peek()
		if ok && st.Settings.Autostart {
			order = append(order, "saved before applying")
		}
		return "", errors.New("the login folder is read-only")
	}
	s, _, cancel := newSup(t, f)
	defer cancel()

	next := s.View().Settings
	next.Autostart = true
	res := s.SetSettings(next, ViaPage)
	if res.OK || !strings.Contains(res.Message, "read-only") {
		t.Fatalf("%+v", res)
	}
	if len(order) != 1 {
		t.Fatal("the preference must be saved before the login entry is written")
	}
	if s.View().Settings.Autostart {
		t.Fatal("a failure must put the stored preference back")
	}
	st, _ := f.d.Store.Load()
	if st.Settings.Autostart {
		t.Fatalf("the saved file must be put back too: %+v", st.Settings)
	}
}

// Reading a very long transcript for the first time is the one thing a
// pass does that can take real time, so it happens away from the loop: an
// action asked for while a read is under way answers immediately, and the
// numbers turn up in a later view.
func TestStatsAreReadAwayFromTheLoop(t *testing.T) {
	id := "83838383-0000-4000-8000-000000000001"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	f.d.readStats = func(string, string) (claude.Stats, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return claude.Stats{Model: "claude-sonnet-5", Turns: 41, InputTokens: 4242}, nil
	}
	f.writeTranscript(t, "/home/dev/ws", id)
	f.p.SetAlive(9, true)
	f.setFiles(sess(9, id, "bg", "cli", "83838383"))
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the transcript was never read")
	}

	// The read is stuck. An action must not wait behind it.
	done := make(chan Result, 1)
	go func() { done <- s.ResumeWatch("83838383-0000-4000-8000-00000000000f", ViaPage) }()
	select {
	case res := <-done:
		if res.Message == "" {
			t.Fatal("the action must answer")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("an action waited for a slow transcript read")
	}

	close(release)
	if !waitFor(t, f, func() bool {
		for _, sn := range s.View().Sessions {
			if sn.ID == id && sn.Stats.Turns == 41 {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("the numbers must turn up in a later view: %+v", s.View().Sessions)
	}
}

func countCalls(f *fixture, prefix string) int {
	n := 0
	for _, c := range f.r.CallList() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func countMessages(f *fixture, substring string) int {
	n := 0
	for _, e := range f.d.Log.Recent(500, "") {
		if strings.Contains(e.Message, substring) {
			n++
		}
	}
	return n
}

// An extra copy is removed whether or not stopping it worked, and when
// neither worked the entry names both commands to run by hand.
func TestDedupeRemovesEvenWhenTheStopFailedAndNamesBothCommands(t *testing.T) {
	id := "88888888-0000-4000-8000-000000000001"
	f := newFixture(t, func(args []string) (string, error) {
		a := strings.Join(args, " ")
		if strings.HasPrefix(a, "stop ") {
			return "error: that session is wedged", errors.New("exit 1")
		}
		return "ok", nil
	})
	f.p.SetAlive(9, true)
	f.p.SetAlive(11, true)
	f.setFiles(
		sess(9, id, "bg", "cli", "88888888"),
		sess(11, id, "bg", "cli", "99990000"),
	)
	f.d.Obs.RefreshNow()
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "88888888", Cwd: "/home/dev/ws",
		PromiseState: "fallback", HasSavedOptions: true})

	for i := 0; i < 4; i++ {
		s.reconcile(context.Background(), f.d.Obs.Peek())
	}

	calls := strings.Join(f.r.CallList(), "\n")
	if !strings.Contains(calls, "stop 99990000") {
		t.Fatalf("the extra copy must be asked to stop: %v", f.r.CallList())
	}
	if !strings.Contains(calls, "rm 99990000") {
		t.Fatalf("the extra copy must be removed even though the stop failed: %v", f.r.CallList())
	}
	said := ""
	for _, e := range f.d.Log.Recent(500, "") {
		if strings.Contains(e.Message, "could not clean up the extra background copy") {
			said = e.Message
		}
	}
	if said == "" {
		t.Fatalf("the failure must be explained: %+v", f.d.Log.Recent(50, ""))
	}
	if !strings.Contains(said, "run `claude stop 99990000`, then `claude rm 99990000`") {
		t.Fatalf("the entry must name both commands, one after the other: %q", said)
	}
	if strings.Contains(said, "&&") {
		t.Fatalf("Windows PowerShell 5.1 cannot run commands joined with &&: %q", said)
	}
	if n := countMessages(f, "could not clean up the extra background copy"); n != 1 {
		t.Fatalf("the same trouble is said once, got %d lines", n)
	}
}

// A shutdown must not lose a resume that really started a background copy:
// the copy is out there, and nothing else would ever record it.
func TestAResumeThatSucceededIsSavedEvenWhenShutdownLandsMidPass(t *testing.T) {
	first := "89898989-0000-4000-8000-000000000001"
	second := "90909090-0000-4000-8000-000000000001"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := newFixture(t, nil)
	f.r.SetRespond(func(args []string) (string, error) {
		a := strings.Join(args, " ")
		if a == "agents --json" {
			return "[]", nil
		}
		if strings.HasPrefix(a, "--bg --resume ") {
			// The shutdown lands while the resume is running, after the
			// background copy has already been started.
			cancel()
			return "backgrounded \u00b7 12341234 \u00b7 demo-a1", nil
		}
		return "", nil
	})
	f.writeTranscript(t, "/home/dev/ws", first)
	f.writeTranscript(t, "/home/dev/ws", second)

	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: first, ShortID: "89898989", Cwd: "/home/dev/ws", PromiseState: "inplace", HasSavedOptions: true})
	s.addWatchForTest(state.Watch{SessionID: second, ShortID: "90909090", Cwd: "/home/dev/ws", PromiseState: "inplace", HasSavedOptions: true})

	f.d.Obs.RefreshNow()
	s.reconcile(ctx, f.d.Obs.Peek())
	f.d.Obs.RefreshNow()
	s.reconcile(ctx, f.d.Obs.Peek())

	if n := countCalls(f, "--bg --resume "); n != 1 {
		t.Fatalf("the pass must stop after the shutdown, got %d resumes: %v", n, f.r.CallList())
	}
	st, err := f.d.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	saved := st.Find(first)
	if saved == nil {
		t.Fatalf("the watch disappeared: %+v", st.Watches)
	}
	if saved.ShortID != "12341234" || saved.PromiseState != "fallback" {
		t.Fatalf("a copy that really started must reach the disk: %+v", saved)
	}
}

// A state file that had to be set aside is explained in one sentence, the
// one the store itself wrote. Building a second sentence around it either
// says the same thing twice or, for a version mismatch, leads with a
// reason that is not the real one.
func TestTheHealedStateEntryIsExactlyTheReasonAndSaidOnce(t *testing.T) {
	cases := []struct {
		what    string
		saved   string
		leading string
	}{
		{"a file that is not readable", "{ not json", "saved state could not be read"},
		{"a file from another major version", `{"version":"9.4.1","watches":[]}`, "saved state was written by version 9.4.1"},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			f := newFixture(t, nil)
			if err := os.WriteFile(filepath.Join(f.d.Store.Dir, "state.json"), []byte(c.saved), 0o600); err != nil {
				t.Fatal(err)
			}

			New(*f.d)

			var said []string
			for _, e := range f.d.Log.Recent(50, "") {
				if strings.Contains(e.Message, "state.json.bad") {
					said = append(said, e.Message)
				}
			}
			if len(said) != 1 {
				t.Fatalf("the file being set aside is explained once, got %d: %v", len(said), said)
			}
			if !strings.HasPrefix(said[0], c.leading) {
				t.Fatalf("the entry must lead with the real reason: %q", said[0])
			}
			if n := strings.Count(said[0], "no sessions are being babysat"); n != 1 {
				t.Fatalf("the entry says what it means once, got %d times: %q", n, said[0])
			}
			if !strings.Contains(said[0], "moved to state.json.bad") {
				t.Fatalf("the entry must say where the file went: %q", said[0])
			}
		})
	}
}

// Two unusable ids are one piece of news, not one per pass and not a memo
// that flips between them.
func TestTwoUnusableCopyIDsAreSaidOnceAcrossPasses(t *testing.T) {
	id := "92929292-0000-4000-8000-000000000001"
	f := newFixture(t, func([]string) (string, error) { return "ok", nil })
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "92929292", Cwd: "/home/dev/ws",
		PromiseState: "fallback", HasSavedOptions: true})

	ours := claude.Session{ID: id, ShortID: "92929292", PID: 9, Host: claude.HostBackground, Cwd: "/home/dev/ws"}
	first := claude.Session{ID: id, ShortID: "NOT-HEX!", PID: 10, Host: claude.HostBackground, Cwd: "/home/dev/ws"}
	second := claude.Session{ID: id, ShortID: "ALSO-BAD", PID: 11, Host: claude.HostBackground, Cwd: "/home/dev/ws"}
	for i := 0; i < 3; i++ {
		s.reconcile(context.Background(), observe.Snapshot{
			At:       time.Now().Add(time.Duration(i) * time.Second),
			Sessions: []claude.Session{ours, first, second},
		})
	}

	// Each id is named, since each is a copy somebody has to go and deal
	// with, but the pair is one piece of news: three passes say it once.
	if n := countMessages(f, "cannot use that id safely"); n != 2 {
		t.Fatalf("three passes must say it once, one line per id, got %d lines: %+v", n, f.d.Log.Recent(50, ""))
	}
	for _, short := range []string{"NOT-HEX!", "ALSO-BAD"} {
		if n := countMessages(f, short); n != 1 {
			t.Fatalf("%s must be named exactly once, got %d", short, n)
		}
	}
	for _, c := range f.r.CallList() {
		if strings.Contains(c, "NOT-HEX!") || strings.Contains(c, "ALSO-BAD") {
			t.Fatalf("an id of that shape must never reach the runner: %v", f.r.CallList())
		}
	}
}

// A resume that finds the session running somewhere else did nothing wrong
// and must not count against the watch: three of those would pause a watch
// over a session that was never in trouble.
func TestAResumeThatFindsTheSessionRunningRecordsNoFailure(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555534"
	f := newFixture(t, nil)
	f.r.SetRespond(func(args []string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case a == "agents --json":
			return "[]", nil
		case strings.HasPrefix(a, "--bg --resume"):
			f.p.SetAlive(10, true)
			f.addFile(desktopSession(10, id))
			return "error: session 11111111 is already running in the background; run claude attach 11111111", nil
		}
		return "", nil
	})
	f.writeTranscript(t, "/home/dev/ws", id)
	s, _, cancel := newSup(t, f)
	defer cancel()

	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "11111111", Cwd: "/home/dev/ws",
		OriginHost: claude.HostDesktop, PromiseState: "inplace", HasSavedOptions: true})

	if !waitFor(t, f, func() bool {
		return countMessages(f, "already running in the desktop app, nothing to resume") == 1
	}) {
		t.Fatalf("the resume must say what it found: %+v", f.d.Log.Recent(50, ""))
	}
	w, _ := firstWatch(s)
	if len(w.Failures) != 0 || w.Paused {
		t.Fatalf("a session that is running is not a failure: %+v", w)
	}
	if w.PromiseState != "inplace" || w.OriginHost != claude.HostDesktop {
		t.Fatalf("the watch follows the session: %+v", w)
	}
	if n := countCalls(f, "--bg --resume "); n != 1 {
		t.Fatalf("one attempt is enough, got %d: %v", n, f.r.CallList())
	}
}

// A resume that finds our own background copy already running is the
// promise being kept, not a session sitting in a host of its own: calling it
// in place would tell the loop to leave that copy alone for good, and a host
// that later takes the session back would find it still running beside it.
func TestAResumeThatFindsOurOwnCopyKeepsTheFallback(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555536"
	f := newFixture(t, nil)
	f.r.SetRespond(func(args []string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case a == "agents --json":
			return "[]", nil
		case strings.HasPrefix(a, "--bg --resume"):
			f.p.SetAlive(9, true)
			f.addFile(bgSession(9, id, "b"))
			return "error: session 11111111 is running as a background session", nil
		}
		return "", nil
	})
	f.writeTranscript(t, "/home/dev/ws", id)

	// The loop is deliberately not started: the two absent snapshots and the
	// pass that resumes are the ones this test drove.
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, Cwd: "/home/dev/ws",
		OriginHost: claude.HostDesktop, PromiseState: "inplace"})

	at := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 2; i++ {
		s.reconcileForTest(observe.Snapshot{At: at.Add(time.Duration(i) * time.Second)})
	}

	w, _ := s.watchForTest(id)
	if w.PromiseState != "fallback" {
		t.Fatalf("a copy of our own is the fallback state: %+v", w)
	}
	if !w.HasSavedOptions {
		t.Fatalf("a copy that is running has saved its own options: %+v", w)
	}
	if w.ShortID != "11111111" {
		t.Fatalf("the copy that was found is the one to watch: %+v", w)
	}
	if len(w.Failures) != 0 || w.Paused {
		t.Fatalf("a session that is running is not a failure: %+v", w)
	}
	said := ""
	for _, e := range f.d.Log.Recent(50, "") {
		if e.Automatic && strings.Contains(e.Reason, "session found running elsewhere") {
			said = e.Message
		}
	}
	if !strings.Contains(said, "already running in the background, nothing to resume") {
		t.Fatalf("the pass must say what it found, under its own reason: %q %+v", said, f.d.Log.Recent(50, ""))
	}
}

// A release that happens while a paused watch is still on the list must not
// claim that nothing is babysat: the list is what the header counts, and it
// still has a row on it.
func TestKeepAwakeReleaseSaysNothingIsLeftToKeepAwake(t *testing.T) {
	active := "11111111-2222-4333-8444-555555555548"
	paused := "11111111-2222-4333-8444-555555555549"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	f.p.SetAlive(7, true)
	f.setFiles(sess(7, active, "interactive", "cli", ""))
	f.d.Obs.RefreshNow()
	s, pw, cancel := newSup(t, f)
	defer cancel()

	s.addWatchForTest(state.Watch{SessionID: active, ShortID: "11111111", Cwd: "/home/dev/ws",
		OriginHost: claude.HostTerminal, PromiseState: "inplace", HasSavedOptions: true})
	s.addWatchForTest(state.Watch{SessionID: paused, ShortID: "22222222", Cwd: "/home/dev/ws",
		PromiseState: "paused", Paused: true, PauseReason: "three failed resumes in five minutes: exit 1"})
	if v := s.View(); !v.KeepAwake.Held || v.KeepAwake.ActiveWatches != 1 {
		t.Fatalf("%+v", v.KeepAwake)
	}

	if res := s.Unbabysit(active, ViaPage); !res.OK {
		t.Fatal(res.Message)
	}
	v := s.View()
	if v.KeepAwake.Held || v.KeepAwake.ActiveWatches != 0 {
		t.Fatalf("%+v", v.KeepAwake)
	}
	if len(v.Watches) != 1 || v.Totals.Babysat != 1 {
		t.Fatalf("the paused watch is still babysat: %d %+v", v.Totals.Babysat, v.Watches)
	}
	if _, released := pw.counts(); released != 1 {
		t.Fatalf("the request is released once, released %d", released)
	}
	said := 0
	for _, e := range f.d.Log.Recent(100, "") {
		if !strings.Contains(e.Message, "no longer keeping the computer awake") {
			continue
		}
		said++
		if e.Message != "no longer keeping the computer awake, no babysat session is active" {
			t.Fatalf("a watch is still on the list: %q", e.Message)
		}
	}
	if said != 1 {
		t.Fatalf("the release is said once, said %d", said)
	}
}

// A watch whose background copy stopped, while an app shows the session
// again, goes back to watching it there. Nothing is started and nothing is
// stopped: the person reopened it, or the app restored it.
func TestAReopenedAppTakesTheWatchBack(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555560"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	f.p.SetAlive(10, true)
	f.setFiles(desktopSession(10, id))
	f.d.Obs.RefreshNow()
	s := New(*f.d)
	old := time.Now().Add(-time.Minute)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "11111111", Name: "demo-a1", Cwd: "/home/dev/ws",
		OriginHost: claude.HostTerminal, PromiseState: "fallback", HasSavedOptions: true, Failures: []time.Time{old}})

	s.reconcileForTest(f.d.Obs.Current())

	w, _ := s.watchForTest(id)
	if w.PromiseState != "inplace" || w.OriginHost != claude.HostDesktop {
		t.Fatalf("the watch must follow the app that has the session: %+v", w)
	}
	if !w.OriginRemoteControl {
		t.Fatalf("the desktop session has Remote Control on: %+v", w)
	}
	if len(w.Failures) != 0 {
		t.Fatalf("the struggles before are behind it: %+v", w.Failures)
	}
	if len(f.r.CallList()) != 0 || len(f.p.CallList()) != 0 {
		t.Fatalf("nothing is started or stopped: %v %v", f.r.CallList(), f.p.CallList())
	}
	if StateOf(w, f.d.Obs.Current().All(id)) != StateWatching {
		t.Fatalf("the watch is Watching again: %+v", w)
	}
	found := false
	for _, e := range f.d.Log.Recent(20, "") {
		if e.Automatic && e.Reason == "session open in the desktop app again" && e.Message == "babysitting it in the desktop app" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the change must be explained: %+v", f.d.Log.Recent(20, ""))
	}
}

// Our background copy is never stopped to make room for an app, even when
// the app shows the session beside it.
func TestOurCopyIsNeverStoppedToMakeRoom(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555561"
	f := newFixture(t, func([]string) (string, error) { return "ok", nil })
	f.p.SetAlive(9, true)
	f.p.SetAlive(10, true)
	f.setFiles(bgSession(9, id, "b"), desktopSession(10, id))
	f.d.Obs.RefreshNow()
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "11111111", Cwd: "/home/dev/ws",
		OriginHost: claude.HostDesktop, PromiseState: "fallback", HasSavedOptions: true})

	for i := 0; i < 3; i++ {
		f.d.Obs.RefreshNow()
		s.reconcileForTest(f.d.Obs.Current())
	}
	if n := countCalls(f, "stop "); n != 0 {
		t.Fatalf("our copy must never be stopped to make room: %v", f.r.CallList())
	}
	w, _ := s.watchForTest(id)
	if w.PromiseState != "fallback" || StateOf(w, f.d.Obs.Current().All(id)) != StateInBackground {
		t.Fatalf("the watch stays In background: %+v", w)
	}
}

// In the background, a copy that dies is started again, the watch stays
// and so does keep-awake.
func TestADeadBackgroundCopyIsStartedAgain(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555562"
	f := newFixture(t, nil)
	var mu sync.Mutex
	resumes := 0
	f.r.SetRespond(func(args []string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case a == "agents --json":
			return "[]", nil
		case strings.HasPrefix(a, "--bg --resume"):
			mu.Lock()
			resumes++
			mu.Unlock()
			f.p.SetAlive(11, true)
			f.setFiles(bgSession(11, id, "b"))
			return "backgrounded \u00b7 11111111 \u00b7 demo-a1", nil
		}
		return "", nil
	})
	f.writeTranscript(t, "/home/dev/ws", id)
	f.p.SetAlive(9, true)
	f.setFiles(bgSession(9, id, "b"))
	f.d.Obs.RefreshNow()
	s, pw, cancel := newSup(t, f)
	defer cancel()
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "11111111", Cwd: "/home/dev/ws",
		OriginHost: claude.HostVSCode, PromiseState: "fallback", HasSavedOptions: true})
	if !waitFor(t, f, func() bool { w, ok := firstWatch(s); return ok && w.State == StateInBackground }) {
		t.Fatal("the watch starts In background")
	}

	f.p.SetAlive(9, false)
	f.setFiles()
	if !waitFor(t, f, func() bool {
		mu.Lock()
		n := resumes
		mu.Unlock()
		w, ok := firstWatch(s)
		return ok && n == 1 && w.State == StateInBackground
	}) {
		w, _ := firstWatch(s)
		t.Fatalf("the copy must be started again: %+v %v", w, f.r.CallList())
	}
	if !s.View().KeepAwake.Held {
		t.Fatal("keep-awake stays held throughout")
	}
	if _, released := pw.counts(); released != 0 {
		t.Fatalf("keep-awake was never let go, released %d", released)
	}
}

// A session whose app is the background to begin with is back where it
// belongs after its copy is started again, so it is still Watching.
func TestABackgroundSessionStaysWatchingAfterARestart(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555563"
	f := newFixture(t, func(args []string) (string, error) {
		a := strings.Join(args, " ")
		if a == "agents --json" {
			return "[]", nil
		}
		if strings.HasPrefix(a, "--bg --resume") {
			return "backgrounded \u00b7 11111111 \u00b7 demo-a1", nil
		}
		return "", nil
	})
	f.writeTranscript(t, "/home/dev/ws", id)
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "11111111", Cwd: "/home/dev/ws",
		OriginHost: claude.HostBackground, PromiseState: "inplace", HasSavedOptions: true})

	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 2; i++ {
		s.reconcileForTest(observe.Snapshot{At: at.Add(time.Duration(i) * time.Second)})
	}
	if n := countCalls(f, "--bg --resume "); n != 1 {
		t.Fatalf("the copy must be started again: %v", f.r.CallList())
	}
	w, _ := s.watchForTest(id)
	if w.PromiseState != "inplace" {
		t.Fatalf("a background session is still watched in place: %+v", w)
	}
	ours := claude.Session{ID: id, ShortID: "11111111", PID: 9, Host: claude.HostBackground, Cwd: "/home/dev/ws"}
	if got := StateOf(w, []claude.Session{ours}); got != StateWatching {
		t.Fatalf("state %s, want watching", got)
	}
}

// The request is held while any watch is Watching, In background or
// Starting, and a Stuck one does not hold it.
func TestKeepAwakeHoldsForEveryStateButStuck(t *testing.T) {
	cases := []struct {
		name string
		w    state.Watch
		live bool
		want bool
	}{
		{"watching", state.Watch{OriginHost: claude.HostTerminal, PromiseState: "inplace"}, true, true},
		{"in background", state.Watch{OriginHost: claude.HostDesktop, PromiseState: "fallback", HasSavedOptions: true}, true, true},
		{"starting", state.Watch{OriginHost: claude.HostDesktop, PromiseState: "fallback", HasSavedOptions: true}, false, true},
		{"stuck", state.Watch{OriginHost: claude.HostDesktop, PromiseState: "paused", Paused: true, PauseReason: "three failed resumes in five minutes: exit 1"}, false, false},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := "11111111-2222-4333-8444-5555555555" + strconv.Itoa(70+i)
			f := newFixture(t, func([]string) (string, error) { return "[]", nil })
			if c.live {
				f.p.SetAlive(9, true)
				kind := "interactive"
				if c.w.PromiseState == "fallback" {
					kind = "bg"
				}
				f.setFiles(sess(9, id, kind, "cli", id[:8]))
			}
			f.d.Obs.RefreshNow()
			f.d.Power = &fakePower{supported: true}
			s := New(*f.d)
			c.w.SessionID, c.w.ShortID, c.w.Cwd = id, id[:8], "/home/dev/ws"
			s.addWatchForTest(c.w)
			s.reconcileForTest(f.d.Obs.Current())
			if got := s.View().KeepAwake.Held; got != c.want {
				t.Fatalf("held %v, want %v", got, c.want)
			}
		})
	}
}

// Babysitting three sessions one after another says three, not one, and
// letting one go says two.
func TestKeepAwakeCountsEveryBabysatSession(t *testing.T) {
	ids := []string{
		"11111111-2222-4333-8444-555555555580",
		"22222222-2222-4333-8444-555555555581",
		"33333333-2222-4333-8444-555555555582",
	}
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	var files [][]byte
	for i, id := range ids {
		f.p.SetAlive(20+i, true)
		files = append(files, sess(20+i, id, "interactive", "cli", ""))
	}
	f.setFiles(files...)
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()

	for _, id := range ids {
		if res := s.Babysit(id, false, ViaPage); !res.OK {
			t.Fatal(res.Message)
		}
	}
	// Recent lists the oldest entry first, so the newest is looked for from
	// the end.
	last := func() string {
		entries := f.d.Log.Recent(100, "")
		for i := len(entries) - 1; i >= 0; i-- {
			if strings.HasPrefix(entries[i].Message, "keeping the computer awake for") {
				return entries[i].Message
			}
		}
		return ""
	}
	if got := last(); got != "keeping the computer awake for 3 babysat sessions" {
		t.Fatalf("%q", got)
	}
	if res := s.Unbabysit(ids[0], ViaPage); !res.OK {
		t.Fatal(res.Message)
	}
	if got := last(); got != "keeping the computer awake for 2 babysat sessions" {
		t.Fatalf("%q", got)
	}
}
