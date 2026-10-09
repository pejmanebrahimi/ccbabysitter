package supervise

import (
	"context"
	"reflect"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// viewOf builds the view of one snapshot on a supervisor that is not
// running, so a test sees exactly what that snapshot turns into.
func viewOf(t *testing.T, s *Supervisor, sessions []claude.Session) View {
	t.Helper()
	var v View
	s.ask(func(context.Context) Result {
		s.snap = observe.Snapshot{At: time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC), Sessions: sessions}
		v = s.buildView()
		return Result{OK: true}
	})
	return v
}

func onlySession(t *testing.T, v View) SessionView {
	t.Helper()
	if len(v.Sessions) != 1 {
		t.Fatalf("want one entry, got %d: %+v", len(v.Sessions), v.Sessions)
	}
	return v.Sessions[0]
}

// One session id live in two processes is one entry, taken from the
// process in an app, and says every host it is live in.
func TestASessionLiveInTwoProcessesIsOneEntry(t *testing.T) {
	id := "7a7a7a7a-0000-4000-8000-000000000001"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	s := New(*f.d)
	s.trees[41] = procs.TreeStats{CPUPercent: 3, RSS: 1000, Processes: 2, PIDs: []int{41, 42}}
	s.trees[30] = procs.TreeStats{CPUPercent: 5, RSS: 2000, Processes: 1, PIDs: []int{30}}
	sessions := []claude.Session{
		{ID: id, ShortID: "7a7a7a7a", PID: 41, Host: claude.HostVSCode, Entrypoint: "claude-vscode", Name: "panel", Cwd: "/home/dev/a", Status: "idle"},
		{ID: id, ShortID: "7a7a7a7a", PID: 30, Host: claude.HostTerminal, Entrypoint: "cli", Name: "term", Cwd: "/home/dev/a", Status: "busy", RemoteControl: true},
	}
	v := viewOf(t, s, sessions)
	got := onlySession(t, v)
	if got.PID != 30 || got.Host != claude.HostTerminal || got.Entrypoint != "cli" || got.Name != "term" || got.Status != "busy" {
		t.Fatalf("the lowest pid in an app is the primary: %+v", got)
	}
	if got.Tree.Processes != 1 || got.Tree.RSSBytes != 2000 {
		t.Fatalf("the tree is the primary's: %+v", got.Tree)
	}
	if !reflect.DeepEqual(got.Live, []claude.Host{claude.HostTerminal, claude.HostVSCode}) {
		t.Fatalf("live: %v", got.Live)
	}
	if !got.RemoteControl {
		t.Fatalf("Remote Control is on when any of them has it")
	}
	sessions[0].RemoteControl, sessions[1].RemoteControl = true, false
	if got := onlySession(t, viewOf(t, s, sessions)); !got.RemoteControl || got.PID != 30 {
		t.Fatalf("Remote Control on in the other process still counts: %+v", got)
	}
	if v.Totals.Sessions != 2 || v.Totals.ByHost[claude.HostVSCode] != 1 || v.Totals.ByHost[claude.HostTerminal] != 1 ||
		v.Totals.Processes != 3 || v.Totals.RSSBytes != 3000 {
		t.Fatalf("totals count every process: %+v", v.Totals)
	}
}

// A copy in the background gives way to the app, whatever its pid.
func TestTheAppIsPrimaryOverTheBackground(t *testing.T) {
	id := "7a7a7a7a-0000-4000-8000-000000000002"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	s := New(*f.d)
	got := onlySession(t, viewOf(t, s, []claude.Session{
		{ID: id, ShortID: "7a7a7a7a", PID: 5, Host: claude.HostBackground, Entrypoint: "cli"},
		{ID: id, ShortID: "7a7a7a7a", PID: 90, Host: claude.HostDesktop, Entrypoint: "claude-desktop"},
	}))
	if got.PID != 90 || got.Host != claude.HostDesktop || got.AttachCmd != "" || got.CanStop {
		t.Fatalf("%+v", got)
	}
	if !reflect.DeepEqual(got.Live, []claude.Host{claude.HostDesktop, claude.HostBackground}) {
		t.Fatalf("live: %v", got.Live)
	}
}

// Three processes are still one entry, the same one on every build, and
// the other sessions and the babysat one are left as they are.
func TestThreeProcessesAreOneStableEntry(t *testing.T) {
	id := "7a7a7a7a-0000-4000-8000-000000000003"
	other := "7a7a7a7a-0000-4000-8000-000000000004"
	babysat := "7a7a7a7a-0000-4000-8000-000000000005"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: babysat, ShortID: "7a7a7a7a", Cwd: "/home/dev/b",
		OriginHost: claude.HostDesktop, PromiseState: "inplace"})
	sessions := []claude.Session{
		{ID: id, ShortID: "7a7a7a7a", PID: 70, Host: claude.HostBackground},
		{ID: other, ShortID: "7a7a7a7a", PID: 60, Host: claude.HostTerminal},
		{ID: id, ShortID: "7a7a7a7a", PID: 52, Host: claude.HostVSCode},
		{ID: babysat, ShortID: "7a7a7a7a", PID: 80, Host: claude.HostDesktop},
		{ID: babysat, ShortID: "7a7a7a7a", PID: 81, Host: claude.HostTerminal},
		{ID: id, ShortID: "7a7a7a7a", PID: 51, Host: claude.HostVSCode},
	}
	first := viewOf(t, s, sessions)
	if len(first.Sessions) != 2 || first.Sessions[0].ID != id || first.Sessions[1].ID != other {
		t.Fatalf("%+v", first.Sessions)
	}
	got := first.Sessions[0]
	if got.PID != 51 || !reflect.DeepEqual(got.Live, []claude.Host{claude.HostVSCode, claude.HostVSCode, claude.HostBackground}) {
		t.Fatalf("%+v", got)
	}
	if !reflect.DeepEqual(first.Sessions[1].Live, []claude.Host{claude.HostTerminal}) {
		t.Fatalf("a session in one process is live in that one host: %v", first.Sessions[1].Live)
	}
	if first.Totals.Sessions != 6 {
		t.Fatalf("totals count every process: %+v", first.Totals)
	}
	reversed := append([]claude.Session{}, sessions...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	for i := 0; i < 5; i++ {
		again := viewOf(t, s, sessions)
		if !reflect.DeepEqual(again.Sessions, first.Sessions) {
			t.Fatalf("build %d differs:\n%+v\n%+v", i, again.Sessions, first.Sessions)
		}
	}
	if got := viewOf(t, s, reversed); len(got.Sessions) != 2 || !reflect.DeepEqual(got.Sessions[0], first.Sessions[0]) {
		t.Fatalf("the merged entry does not depend on the order processes are found in: %+v", got.Sessions)
	}
}

// A session shows the name its own app shows, from the transcript, and the
// name from its session record goes to the tooltip when it differs.
func TestASessionShowsTheNameItsAppShows(t *testing.T) {
	id := "7a7a7a7a-0000-4000-8000-000000000010"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	s := New(*f.d)
	s.stats[id] = claude.Stats{Title: "Hooora test"}
	got := onlySession(t, viewOf(t, s, []claude.Session{
		{ID: id, ShortID: "7a7a7a7a", PID: 5, Host: claude.HostVSCode, Entrypoint: "claude-vscode", Name: "my-app-2-94"},
	}))
	if got.Name != "Hooora test" || got.AlsoCalled != "my-app-2-94" {
		t.Fatalf("%q %q", got.Name, got.AlsoCalled)
	}

	s.stats[id] = claude.Stats{Title: "my-app-2-94"}
	got = onlySession(t, viewOf(t, s, []claude.Session{
		{ID: id, ShortID: "7a7a7a7a", PID: 5, Host: claude.HostTerminal, Entrypoint: "cli", Name: "my-app-2-94"},
	}))
	if got.Name != "my-app-2-94" || got.AlsoCalled != "" {
		t.Fatalf("the same name is not repeated: %q %q", got.Name, got.AlsoCalled)
	}

	s.stats[id] = claude.Stats{}
	got = onlySession(t, viewOf(t, s, []claude.Session{
		{ID: id, ShortID: "7a7a7a7a", PID: 5, Host: claude.HostTerminal, Entrypoint: "cli", Name: "my-app-2-94"},
	}))
	if got.Name != "my-app-2-94" || got.AlsoCalled != "" {
		t.Fatalf("with no title the record's name stays: %q %q", got.Name, got.AlsoCalled)
	}
}

// Desktop writes its own sidebar title into the session record and keeps
// it current on rename, so that name comes first there.
func TestADesktopSessionKeepsItsRecordName(t *testing.T) {
	id := "7a7a7a7a-0000-4000-8000-000000000011"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	s := New(*f.d)
	s.stats[id] = claude.Stats{Title: "Hooora test"}
	desk := claude.Session{ID: id, ShortID: "7a7a7a7a", PID: 5, Host: claude.HostDesktop, Entrypoint: "claude-desktop", Name: "Sidebar title"}
	got := onlySession(t, viewOf(t, s, []claude.Session{desk}))
	if got.Name != "Sidebar title" || got.AlsoCalled != "" {
		t.Fatalf("%q %q", got.Name, got.AlsoCalled)
	}
	desk.Name = ""
	got = onlySession(t, viewOf(t, s, []claude.Session{desk}))
	if got.Name != "Hooora test" || got.AlsoCalled != "" {
		t.Fatalf("an empty record name falls back to the title: %q %q", got.Name, got.AlsoCalled)
	}
}

// A watch shows the same name its session would.
func TestAWatchShowsTheNameItsAppShows(t *testing.T) {
	id := "7a7a7a7a-0000-4000-8000-000000000012"
	desk := "7a7a7a7a-0000-4000-8000-000000000013"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	s := New(*f.d)
	s.st.Watches = []state.Watch{
		{SessionID: id, ShortID: "7a7a7a7a", Name: "my-app-2-94", OriginHost: claude.HostVSCode, PromiseState: "inplace"},
		{SessionID: desk, ShortID: "7a7a7a7b", Name: "Sidebar title", OriginHost: claude.HostDesktop, PromiseState: "inplace"},
	}
	s.stats[id] = claude.Stats{Title: "Hooora test"}
	s.stats[desk] = claude.Stats{Title: "Hooora desk"}
	v := viewOf(t, s, []claude.Session{
		{ID: id, ShortID: "7a7a7a7a", PID: 5, Host: claude.HostVSCode, Entrypoint: "claude-vscode", Name: "my-app-2-94"},
		{ID: desk, ShortID: "7a7a7a7b", PID: 6, Host: claude.HostDesktop, Entrypoint: "claude-desktop", Name: "Sidebar title"},
	})
	if len(v.Watches) != 2 {
		t.Fatalf("%+v", v.Watches)
	}
	if w := v.Watches[0]; w.Name != "Hooora test" || w.AlsoCalled != "my-app-2-94" {
		t.Fatalf("%q %q", w.Name, w.AlsoCalled)
	}
	if w := v.Watches[1]; w.Name != "Sidebar title" || w.AlsoCalled != "" {
		t.Fatalf("%q %q", w.Name, w.AlsoCalled)
	}
	if s.st.Watches[0].Name != "my-app-2-94" {
		t.Fatalf("the saved watch is not changed: %+v", s.st.Watches[0])
	}
}

// The record's name follows the same rules as a title, so a difference in
// whitespace alone is not another name.
func TestARecordNameIsCleanedLikeATitle(t *testing.T) {
	id := "7a7a7a7a-0000-4000-8000-000000000014"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	s := New(*f.d)
	s.stats[id] = claude.Stats{Title: "Hooora test"}
	got := onlySession(t, viewOf(t, s, []claude.Session{
		{ID: id, ShortID: "7a7a7a7a", PID: 5, Host: claude.HostVSCode, Entrypoint: "claude-vscode", Name: "  Hooora \t test "},
	}))
	if got.Name != "Hooora test" || got.AlsoCalled != "" {
		t.Fatalf("%q %q", got.Name, got.AlsoCalled)
	}
	got = onlySession(t, viewOf(t, s, []claude.Session{
		{ID: id, ShortID: "7a7a7a7a", PID: 5, Host: claude.HostDesktop, Entrypoint: "claude-desktop", Name: " Sidebar   title "},
	}))
	if got.Name != "Sidebar title" || got.AlsoCalled != "" {
		t.Fatalf("%q %q", got.Name, got.AlsoCalled)
	}
}

// Uptime counts from when the session started in its process when that is
// later than the process's own start, as for a background session given a
// process that was started ahead of time.
func TestUptimeCountsFromTheSessionStart(t *testing.T) {
	id := "7a7a7a7a-0000-4000-8000-000000000009"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	now := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	f.d.Now = func() time.Time { return now }
	s := New(*f.d)
	s.trees[41] = procs.TreeStats{Processes: 1, PIDs: []int{41}, Uptime: 2 * time.Hour}
	bg := claude.Session{ID: id, ShortID: "7a7a7a7a", PID: 41, Host: claude.HostBackground, Entrypoint: "cli", Cwd: "/home/dev/a",
		StartedAt: now.Add(-5 * time.Minute)}
	if got := onlySession(t, viewOf(t, s, []claude.Session{bg})).Tree.UptimeSeconds; got != 300 {
		t.Fatalf("uptime %d s, want 300", got)
	}
	// Without a start time, or with one before the process's, the
	// process's age is the uptime.
	for _, at := range []time.Time{{}, now.Add(-3 * time.Hour), now.Add(time.Hour)} {
		bg.StartedAt = at
		if got := onlySession(t, viewOf(t, s, []claude.Session{bg})).Tree.UptimeSeconds; got != 7200 {
			t.Fatalf("StartedAt %v: uptime %d s, want 7200", at, got)
		}
	}
}

// The uptime of a babysat session counts from its start too.
func TestWatchUptimeCountsFromTheSessionStart(t *testing.T) {
	id := "7a7a7a7a-0000-4000-8000-00000000000a"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	now := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	f.d.Now = func() time.Time { return now }
	s := New(*f.d)
	s.trees[41] = procs.TreeStats{Processes: 1, PIDs: []int{41}, Uptime: 2 * time.Hour}
	s.st.Watches = []state.Watch{{SessionID: id, ShortID: "7a7a7a7a", Cwd: "/home/dev/a", PromiseState: "fallback", OriginHost: claude.HostTerminal}}
	bg := claude.Session{ID: id, ShortID: "7a7a7a7a", PID: 41, Host: claude.HostBackground, Entrypoint: "cli", Cwd: "/home/dev/a",
		StartedAt: now.Add(-5 * time.Minute)}
	v := viewOf(t, s, []claude.Session{bg})
	if len(v.Watches) != 1 || v.Watches[0].Tree.UptimeSeconds != 300 {
		t.Fatalf("watches %+v", v.Watches)
	}
}
