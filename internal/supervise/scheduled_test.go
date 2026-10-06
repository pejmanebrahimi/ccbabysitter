package supervise

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
)

// scheduledFixture runs one Claude Desktop session whose transcript says it
// is a scheduled task's run.
func scheduledFixture(t *testing.T, id string) *fixture {
	t.Helper()
	f := newFixture(t, func([]string) (string, error) { return "[]", nil })
	writeRunTranscript(t, f, "/home/dev/ws", id)
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

// Babysit asks the transcript itself, so a run is refused even before its
// counts have been read.
func TestABabysitIsRefusedBeforeTheCountsAreRead(t *testing.T) {
	id := "45454545-0000-4000-8000-000000000008"
	f := scheduledFixture(t, id)
	release := make(chan struct{})
	defer close(release)
	f.d.readStats = func(string, string) (claude.Stats, error) {
		<-release
		return claude.Stats{}, nil
	}
	s, _, cancel := newSup(t, f)
	defer cancel()
	res := s.Babysit(id, false, ViaPage)
	if res.OK || !strings.Contains(res.Message, ScheduledRunReason) {
		t.Fatalf("babysitting a run whose counts are not read yet: %+v", res)
	}
}
