package supervise

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// scheduledFixture runs one Claude Desktop session whose transcript says it
// is a scheduled task's run.
func scheduledFixture(t *testing.T, id string) *fixture {
	t.Helper()
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	f.d.readStats = func(string, string) (claude.Stats, error) {
		return claude.Stats{ScheduledTask: true, Turns: 1}, nil
	}
	f.writeTranscript(t, "/home/dev/ws", id)
	f.p.SetAlive(9, true)
	f.setFiles(sess(9, id, "interactive", "claude-desktop", ""))
	f.d.Obs.RefreshNow()
	return f
}

func scheduledInView(s *Supervisor, id string) bool {
	for _, sn := range s.View().Sessions {
		if sn.ID == id {
			return sn.ScheduledTask
		}
	}
	return false
}

// A scheduled task's run is shown as one, and is not babysat: Claude
// Desktop starts it again on its schedule, and a rescued copy could repeat
// the task's work.
func TestAScheduledTaskRunIsNotBabysat(t *testing.T) {
	id := "45454545-0000-4000-8000-000000000001"
	f := scheduledFixture(t, id)
	s, _, cancel := newSup(t, f)
	defer cancel()
	if !waitFor(t, f, func() bool { return scheduledInView(s, id) }) {
		t.Fatalf("the view does not say it is a scheduled task run: %+v", s.View().Sessions)
	}
	res := s.Babysit(id, false, ViaPage)
	if res.OK || !strings.Contains(res.Message, "scheduled task") {
		t.Fatalf("babysitting a scheduled task run: %+v", res)
	}
	if len(s.View().Watches) != 0 {
		t.Fatalf("a watch was added: %+v", s.View().Watches)
	}
}

// A scheduled task's run that is babysat already, from before CC Babysitter
// knew better or before its transcript was read, is let go, and Activity
// says why.
func TestABabysatScheduledTaskRunIsLetGo(t *testing.T) {
	id := "45454545-0000-4000-8000-000000000002"
	f := scheduledFixture(t, id)
	if err := f.d.Store.Save(state.State{Settings: state.DefaultSettings(), Watches: []state.Watch{
		{SessionID: id, ShortID: "45454545", Name: "Daily report", OriginHost: claude.HostDesktop, PromiseState: "inplace"},
	}}); err != nil {
		t.Fatal(err)
	}
	s, _, cancel := newSup(t, f)
	defer cancel()
	if !waitFor(t, f, func() bool { return len(s.View().Watches) == 0 }) {
		t.Fatalf("the scheduled task run is still babysat: %+v", s.View().Watches)
	}
	found := false
	for _, e := range f.d.Log.Recent(50, "") {
		if strings.Contains(e.Message, "scheduled task") && strings.Contains(e.Message, "no longer babysitting") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Activity does not say why: %+v", f.d.Log.Recent(10, ""))
	}
}

// writeRunTranscript writes a transcript that starts the way a Claude
// Desktop scheduled task's run does, for the real transcript reader.
func writeRunTranscript(t *testing.T, f *fixture, cwd, id string) {
	t.Helper()
	dir := filepath.Join(f.d.ProjectsDir, claude.Slug(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"queue-operation","operation":"enqueue","timestamp":"2026-10-05T05:28:47Z","content":"<scheduled-task name=\"daily-report\">x</scheduled-task>"}` + "\n" +
		`{"type":"user","timestamp":"2026-10-05T05:28:48Z","message":{"role":"user","content":"<scheduled-task name=\"daily-report\">x</scheduled-task>"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A run babysat before this was known, whose app has since closed, is not
// brought back in the background: its transcript is read first, and the
// watch is let go instead.
func TestAGoneScheduledTaskRunIsNotResumed(t *testing.T) {
	id := "45454545-0000-4000-8000-000000000003"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	writeRunTranscript(t, f, "/home/dev/ws", id)
	if err := f.d.Store.Save(state.State{Settings: state.DefaultSettings(), Watches: []state.Watch{
		{SessionID: id, ShortID: "45454545", Name: "Daily report", Cwd: "/home/dev/ws", OriginHost: claude.HostDesktop, PromiseState: "inplace"},
	}}); err != nil {
		t.Fatal(err)
	}
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()
	if !waitFor(t, f, func() bool { return len(s.View().Watches) == 0 }) {
		t.Fatalf("the run is still babysat: %+v", s.View().Watches)
	}
	if n := countCalls(f, "--bg --resume"); n != 0 {
		t.Fatalf("a scheduled task run was resumed %d times: %v", n, f.r.CallList())
	}
}

// A run that our own background copy already carries is let go and that
// copy is stopped, since it would repeat the task's work; Activity says so.
func TestAScheduledTaskRunInTheBackgroundIsStopped(t *testing.T) {
	id := "45454545-0000-4000-8000-000000000004"
	f := newFixture(t, func(args []string) (string, error) { return "[]", nil })
	f.d.readStats = func(string, string) (claude.Stats, error) {
		return claude.Stats{ScheduledTask: true}, nil
	}
	f.writeTranscript(t, "/home/dev/ws", id)
	f.p.SetAlive(9, true)
	f.setFiles(bgSession(9, id, "b"))
	if err := f.d.Store.Save(state.State{Settings: state.DefaultSettings(), Watches: []state.Watch{
		{SessionID: id, ShortID: "45454545", Name: "Daily report", Cwd: "/home/dev/ws", OriginHost: claude.HostDesktop, PromiseState: "fallback"},
	}}); err != nil {
		t.Fatal(err)
	}
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()
	if !waitFor(t, f, func() bool { return len(s.View().Watches) == 0 }) {
		t.Fatalf("the run is still babysat: %+v", s.View().Watches)
	}
	if n := countCalls(f, "stop 45454545"); n != 1 {
		t.Fatalf("its background copy was stopped %d times: %v", n, f.r.CallList())
	}
	if countMessages(f, "stopped its background copy 45454545") != 1 {
		t.Fatalf("Activity does not say the copy was stopped: %+v", f.d.Log.Recent(10, ""))
	}
}

// On a server, a new background session is adopted only once its
// transcript has been read, so a scheduled task's run is never adopted,
// while an ordinary one still is.
func TestAutoBabysitNeverAdoptsAScheduledTaskRun(t *testing.T) {
	early := "45454545-0000-4000-8000-000000000005"
	run := "45454545-0000-4000-8000-000000000006"
	plain := "45454545-0000-4000-8000-000000000007"
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	f.d.Env = func() hosts.Env {
		return hosts.Env{Platform: "linux", CLIFound: true, CLIPresent: true, Headless: true}
	}
	f.p.SetAlive(9, true)
	f.setFiles(bgSession(9, early, "b"))
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()
	s.reconcileForTest(f.d.Obs.Current())

	writeRunTranscript(t, f, "/home/dev/ws", run)
	f.p.SetAlive(10, true)
	f.p.SetAlive(11, true)
	f.addFile(bgSession(10, run, "b"))
	f.addFile(bgSession(11, plain, "b"))
	if !waitFor(t, f, func() bool { return len(s.View().Watches) == 1 }) {
		t.Fatalf("the ordinary session must be adopted: %+v", s.View().Watches)
	}
	if w, _ := firstWatch(s); w.SessionID != plain {
		t.Fatalf("the scheduled task run was adopted: %+v", w)
	}
	if countMessages(f, "adopted background session 45454545") != 1 {
		t.Fatalf("want exactly one adoption: %+v", f.d.Log.Recent(10, ""))
	}
}
