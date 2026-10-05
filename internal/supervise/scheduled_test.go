package supervise

import (
	"strings"
	"testing"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
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
