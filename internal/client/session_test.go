package client

import (
	"errors"
	"strings"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
)

const (
	idA = "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
	idB = "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb"
	idC = "cccccccc-3333-4333-8333-cccccccccccc"
	idD = "dddddddd-4444-4444-8444-dddddddddddd"
)

// view has two live unbabysat sessions both named api, a babysat session
// running in a terminal (only in Watches, as the supervisor builds it), and
// a stuck babysat session running nowhere.
func view() supervise.View {
	return supervise.View{
		Version: "0.4.0",
		Sessions: []supervise.SessionView{
			{ID: idA, ShortID: "aaaaaaaa", Name: "api", PID: 100, ProcStart: "1790000000000", Host: claude.HostTerminal, Live: []claude.Host{claude.HostTerminal}},
			{ID: idB, ShortID: "bbbbbbbb", Name: "api", PID: 200, ProcStart: "1790000000000", Host: claude.HostVSCode, Live: []claude.Host{claude.HostVSCode}},
		},
		Watches: []supervise.WatchView{
			{
				Watch: state.Watch{SessionID: idD, ShortID: "dddddddd", Name: "worker", Cwd: "/srv/worker"},
				State: supervise.StateWatching, Host: claude.HostTerminal, Live: []claude.Host{claude.HostTerminal},
				RCOn: true, Status: "busy", PID: 300, ProcStart: "1790000000000",
				Tree:  supervise.TreeView{UptimeSeconds: 3700},
				Stats: claude.Stats{Model: "claude-opus-5-5", InputTokens: 1200, OutputTokens: 3400},
			},
			{Watch: state.Watch{SessionID: idC, ShortID: "cccccccc", Name: "nightly"}, State: supervise.StateStuck, Live: []claude.Host{}},
		},
	}
}

func noSelf() (int, bool) { return 0, false }

func TestSessionsFromAWatchOnly(t *testing.T) {
	var got *Session
	for _, s := range Sessions(view()) {
		if s.ID == idD {
			s := s
			got = &s
		}
	}
	if got == nil {
		t.Fatal("the babysat running session is missing")
	}
	if !got.Running || got.App != "terminal" || !got.RemoteControl || got.Status != "busy" || got.UptimeSeconds != 3700 ||
		got.Tokens.Input != 1200 || got.Model != "claude-opus-5-5" || !got.Babysat || got.State != "watching" || got.PID != 300 {
		t.Fatalf("watch-only session = %+v", *got)
	}
}

func TestSessionsAppsNeverNull(t *testing.T) {
	for _, s := range Sessions(view()) {
		if s.Apps == nil {
			t.Errorf("%s has nil Apps", s.ShortID)
		}
	}
}

func TestResolveByIDAndShortID(t *testing.T) {
	for _, w := range []string{idA, "aaaaaaaa"} {
		s, err := Resolve(view(), w, noSelf)
		if err != nil || s.ID != idA {
			t.Errorf("Resolve(%q) = %+v, %v", w, s, err)
		}
	}
}

func TestResolveAmbiguousName(t *testing.T) {
	_, err := Resolve(view(), "api", noSelf)
	var amb *AmbiguousError
	if !errors.As(err, &amb) || len(amb.IDs) != 2 {
		t.Fatalf("err = %v, want ambiguous with two ids", err)
	}
	if want := "2 sessions match api: "; !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("message = %q, want it to start with %q", err.Error(), want)
	}
}

func TestResolveAmbiguousShortID(t *testing.T) {
	v := view()
	v.Sessions[1].ShortID = "aaaaaaaa"
	_, err := Resolve(v, "aaaaaaaa", noSelf)
	var amb *AmbiguousError
	if !errors.As(err, &amb) || len(amb.IDs) != 2 || amb.IDs[0] != idA || amb.IDs[1] != idB {
		t.Fatalf("err = %v, want ambiguous with the two full ids, since the short ids are the same", err)
	}
}

// A word that is one session's short id and another's name or also-called
// name fits two sessions, and is refused with both full ids.
func TestResolveShortIDThatIsAnotherSessionsNameIsAmbiguous(t *testing.T) {
	for _, field := range []string{"Name", "AlsoCalled"} {
		v := view()
		if field == "Name" {
			v.Sessions[0].ShortID = "deadbeef"
			v.Sessions[1].Name = "deadbeef"
		} else {
			v.Sessions[0].ShortID = "deadbeef"
			v.Sessions[1].AlsoCalled = "deadbeef"
		}
		_, err := Resolve(v, "deadbeef", noSelf)
		var amb *AmbiguousError
		if !errors.As(err, &amb) || len(amb.IDs) != 2 || amb.IDs[0] != idA || amb.IDs[1] != idB {
			t.Fatalf("%s: err = %v, want ambiguous with both full ids", field, err)
		}
	}
}

// A session whose own name is its short id is not ambiguous with itself.
func TestResolveShortIDThatIsTheSameSessionsNameIsFine(t *testing.T) {
	v := view()
	v.Sessions[0].Name = "aaaaaaaa"
	s, err := Resolve(v, "aaaaaaaa", noSelf)
	if err != nil || s.ID != idA {
		t.Fatalf("got %+v, %v", s, err)
	}
}

// A full id wins outright, even when the same word is another session's
// short id or name.
func TestResolveFullIDWinsOverEverything(t *testing.T) {
	v := view()
	v.Sessions[1].ShortID = idA
	v.Sessions[1].Name = idA
	s, err := Resolve(v, idA, noSelf)
	if err != nil || s.ID != idA {
		t.Fatalf("got %+v, %v", s, err)
	}
}

// self means the enclosing session, even when another session is named self.
func TestResolveSelfIgnoresASessionNamedSelf(t *testing.T) {
	v := view()
	v.Sessions[0].Name = "self"
	s, err := Resolve(v, "self", func() (int, bool) { return 200, true })
	if err != nil || s.ID != idB {
		t.Fatalf("got %+v, %v; want the enclosing session", s, err)
	}
}

func TestResolveFindsNotRunningWatch(t *testing.T) {
	for _, w := range []string{"nightly", "cccccccc", idC} {
		s, err := Resolve(view(), w, noSelf)
		if err != nil || s.ID != idC || s.Running || !s.Babysat || s.State != "stuck" {
			t.Errorf("Resolve(%q) = %+v, %v", w, s, err)
		}
	}
}

func TestResolveUnknown(t *testing.T) {
	if _, err := Resolve(view(), "nope", noSelf); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveSelfOutsideASession(t *testing.T) {
	if _, err := Resolve(view(), "self", noSelf); !errors.Is(err, ErrNotInSession) {
		t.Fatalf("err = %v", err)
	}
}

func TestSelfFoundThroughParents(t *testing.T) {
	p := procs.NewFake()
	p.SetParent(900, 800) // this command
	p.SetParent(800, 200) // its shell, child of the VS Code session
	p.SetCreateTime(900, 1790000002000)
	p.SetCreateTime(800, 1790000001000)
	p.SetCreateTime(200, 1790000000000)
	pid, ok := FindSelf(view(), 900, p)
	if !ok || pid != 200 {
		t.Fatalf("FindSelf = %d, %v; want 200", pid, ok)
	}
	s, err := Resolve(view(), "self", func() (int, bool) { return pid, ok })
	if err != nil || s.ID != idB {
		t.Fatalf("Resolve(self) = %+v, %v", s, err)
	}
}

func TestSelfFoundInAWatch(t *testing.T) {
	p := procs.NewFake()
	p.SetParent(900, 300) // the babysat session's own process
	p.SetCreateTime(900, 1790000002000)
	p.SetCreateTime(300, 1790000000000)
	pid, ok := FindSelf(view(), 900, p)
	if !ok || pid != 300 {
		t.Fatalf("FindSelf = %d, %v; want the babysat session 300", pid, ok)
	}
	s, err := Resolve(view(), "self", func() (int, bool) { return pid, ok })
	if err != nil || s.ID != idD || !s.Babysat {
		t.Fatalf("Resolve(self) = %+v, %v", s, err)
	}
}

func TestSelfIgnoresReusedPid(t *testing.T) {
	p := procs.NewFake()
	p.SetParent(900, 100)
	p.SetCreateTime(900, 1790000002000)
	p.SetCreateTime(100, 1700000000000) // pid 100 is now a different process
	if pid, ok := FindSelf(view(), 900, p); ok {
		t.Fatalf("FindSelf matched %d through a reused pid", pid)
	}
}

// On Windows an orphan keeps its dead parent's pid, and pids are reused, so
// a parent that started after its child is not its parent.
func TestSelfDoesNotFollowAParentThatStartedLater(t *testing.T) {
	p := procs.NewFake()
	p.SetParent(900, 800)
	p.SetParent(800, 200) // 200 is a live session, with the start its file says
	p.SetCreateTime(900, 1790000002000)
	p.SetCreateTime(800, 1790000001000)
	p.SetCreateTime(200, 1790000000000)
	if pid, ok := FindSelf(view(), 900, p); !ok || pid != 200 {
		t.Fatalf("FindSelf = %d, %v; want 200 when each parent is older", pid, ok)
	}
	p.SetCreateTime(800, 1789999999000) // the shell started before the pid that is now its parent
	if pid, ok := FindSelf(view(), 900, p); ok {
		t.Fatalf("FindSelf followed a parent that started later and matched %d", pid)
	}
}

func TestSelfStopsWhereACreationTimeIsUnknown(t *testing.T) {
	p := procs.NewFake()
	p.SetParent(900, 200)
	p.SetCreateTime(200, 1790000000000) // 900's own time is unknown
	if pid, ok := FindSelf(view(), 900, p); ok {
		t.Fatalf("FindSelf matched %d without knowing the child's start", pid)
	}
}

// A start time that cannot be read is not a confirmed match.
func TestSelfNeedsAConfirmedStart(t *testing.T) {
	v := view()
	v.Sessions[1].ProcStart = "" // session B, pid 200
	p := procs.NewFake()
	p.SetParent(900, 800)
	p.SetParent(800, 200)
	p.SetCreateTime(900, 1790000002000)
	p.SetCreateTime(800, 1790000001000)
	p.SetCreateTime(200, 1790000000000)
	if pid, ok := FindSelf(v, 900, p); ok {
		t.Fatalf("FindSelf matched %d with no recorded start", pid)
	}
}

func TestSelfStopsOnCycles(t *testing.T) {
	p := procs.NewFake()
	p.SetParent(900, 901)
	p.SetParent(901, 900)
	p.SetCreateTime(900, 1790000000000)
	p.SetCreateTime(901, 1790000000000)
	if _, ok := FindSelf(view(), 900, p); ok {
		t.Fatal("matched on a cycle")
	}
}

func TestLabels(t *testing.T) {
	s := Session{Name: "api", AlsoCalled: "old-api", ShortID: "aaaaaaaa"}
	got := s.Labels()
	if len(got) != 3 || got[0] != "api" || got[1] != "old-api" || got[2] != "aaaaaaaa" {
		t.Fatalf("Labels = %q", got)
	}
}

// The engine names an unnamed session by the first 8 characters of its id,
// which can differ from the short id of a background job.
func TestLabelsIncludeTheIDPrefixWithoutDuplicates(t *testing.T) {
	s := Session{ID: idA, ShortID: "job12345"}
	got := s.Labels()
	if len(got) != 2 || got[0] != "job12345" || got[1] != "aaaaaaaa" {
		t.Fatalf("Labels = %q", got)
	}
	s = Session{ID: idA, Name: "aaaaaaaa", ShortID: "aaaaaaaa"}
	if got := s.Labels(); len(got) != 1 || got[0] != "aaaaaaaa" {
		t.Fatalf("Labels = %q, want one", got)
	}
	if got := (Session{ID: "abc"}).Labels(); len(got) != 1 || got[0] != "abc" {
		t.Fatalf("Labels of a short id = %q", got)
	}
}

func TestResolveEmptyWordNeverMatches(t *testing.T) {
	v := view()
	v.Sessions = append(v.Sessions, supervise.SessionView{ID: "eeeeeeee-5555-4555-8555-eeeeeeeeeeee", PID: 400})
	v.Watches = append(v.Watches, supervise.WatchView{Watch: state.Watch{SessionID: "ffffffff-6666-4666-8666-ffffffffffff"}})
	if s, err := Resolve(v, "", noSelf); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve(\"\") = %+v, %v; want ErrNotFound", s, err)
	}
}

func TestResolveSkipsEmptyFields(t *testing.T) {
	v := supervise.View{Sessions: []supervise.SessionView{{ID: idA}}}
	for _, w := range []string{"", "x"} {
		if s, err := Resolve(v, w, noSelf); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q) = %+v, %v; want ErrNotFound for an unnamed session", w, s, err)
		}
	}
}

// The conversations on the page's Not running list are sessions too, last,
// marked as not running, with the commands to start them again. A session
// that is running or babysat is never listed twice.
func TestSessionsIncludeNotRunning(t *testing.T) {
	v := view()
	idE := "eeeeeeee-5555-4555-8555-eeeeeeeeeeee"
	at := time.Date(2026, 10, 9, 13, 0, 0, 0, time.UTC)
	v.NotRunning = []supervise.PastView{
		{ID: idE, ShortID: "eeeeeeee", Name: "old", Cwd: "/srv/old", LastActivity: at,
			ResumeCmd: "cd /srv/old && claude --resume " + idE, AttachCmd: "claude attach eeeeeeee"},
		{ID: idC, ShortID: "cccccccc", Name: "nightly"},
	}
	all := Sessions(v)
	last := all[len(all)-1]
	if last.ID != idE || !last.NotRunning || last.Running || last.Babysat || last.App != "" ||
		last.Folder != "/srv/old" || last.LastActivity != "2026-10-09T13:00:00Z" ||
		last.ResumeCmd != "cd /srv/old && claude --resume "+idE || last.AttachCmd != "claude attach eeeeeeee" || last.Apps == nil {
		t.Fatalf("last %+v", last)
	}
	n := 0
	for _, s := range all {
		if s.ID == idC {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the babysat session is listed %d times", n)
	}
}

// A word is matched against the running and babysat sessions first, so a
// conversation that is not running never makes a word that names one of
// them ambiguous. When nothing else fits, it finds the one not running.
func TestResolvePrefersSessionsThatRun(t *testing.T) {
	v := view()
	idE := "eeeeeeee-5555-4555-8555-eeeeeeeeeeee"
	v.NotRunning = []supervise.PastView{{ID: idE, ShortID: "eeeeeeee", Name: "worker"}, {ID: "ffffffff-6666-4666-8666-ffffffffffff", ShortID: "ffffffff", Name: "old"}}
	s, err := Resolve(v, "worker", func() (int, bool) { return 0, false })
	if err != nil || s.ID != idD {
		t.Fatalf("worker: %+v %v", s, err)
	}
	for _, word := range []string{"old", "ffffffff"} {
		s, err := Resolve(v, word, func() (int, bool) { return 0, false })
		if err != nil || !s.NotRunning {
			t.Fatalf("%s: %+v %v", word, s, err)
		}
	}
}
