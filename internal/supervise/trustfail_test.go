package supervise

import (
	"errors"
	"strings"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// A folder the claude CLI does not trust does not become trusted a few
// seconds later, so the first "Workspace not trusted" answer makes the
// session stuck at once, without two more tries, and Activity says the
// session is not running anywhere and what it needs.
func TestAnUntrustedFolderStopsTheRescueAtOnce(t *testing.T) {
	id := "5b5b5b5b-0000-4000-8000-000000000001"
	f := newFixture(t, func(args []string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case a == "agents --json":
			return "[]", nil
		case strings.HasPrefix(a, "--bg --resume"):
			return "Workspace not trusted. Run `claude` in /home/dev/ws once and accept the trust prompt, then retry.", errors.New("exit status 1")
		}
		return "", nil
	})
	f.writeTranscript(t, "/home/dev/ws", id)
	s := New(*f.d)
	s.addWatchForTest(state.Watch{SessionID: id, ShortID: "5b5b5b5b", Name: "demo-a3", Cwd: "/home/dev/ws",
		OriginHost: claude.HostDesktop, PromiseState: "inplace", HasSavedOptions: true})
	at := time.Now()
	for i := 1; i <= 40; i++ {
		s.reconcileForTest(observe.Snapshot{At: at.Add(time.Duration(i) * time.Second)})
	}
	if n := countCalls(f, "--bg --resume "); n != 1 {
		t.Fatalf("want one try, got %d: %v", n, f.r.CallList())
	}
	w, ok := s.watchForTest(id)
	if !ok || !w.Paused || !strings.Contains(w.PauseReason, "does not trust") {
		t.Fatalf("not stuck with the trust reason: %+v %v", w, ok)
	}
	said := false
	for _, e := range f.d.Log.Recent(20, "") {
		if e.Automatic && strings.Contains(e.Message, "not running anywhere") {
			said = true
		}
	}
	if !said {
		t.Fatalf("Activity does not say the session is not running anywhere: %v", f.d.Log.Recent(20, ""))
	}
}
