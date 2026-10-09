package supervise

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// frozenCase is a supervisor that is not running, with one babysat session
// in host that is busy and whose transcript was last written an hour
// before t0, and a clock the test moves.
type frozenCase struct {
	t   *testing.T
	f   *fixture
	s   *Supervisor
	log *state.Log
	now time.Time
	sn  claude.Session
}

func newFrozenCase(t *testing.T, host claude.Host) *frozenCase {
	t.Helper()
	c := &frozenCase{t: t, now: time.Date(2026, 10, 9, 13, 0, 0, 0, time.UTC)}
	c.f = newFixture(t, func(args []string) (string, error) { return "stopped " + args[len(args)-1], nil })
	c.f.d.Now = func() time.Time { return c.now }
	c.log = c.f.d.Log
	c.s = New(*c.f.d)
	id := "7a7a7a7a-0000-4000-8000-0000000000f1"
	cwd := "/home/dev/ws"
	c.f.writeTranscript(t, cwd, id)
	path := filepath.Join(c.f.d.ProjectsDir, claude.Slug(cwd), id+".jsonl")
	if err := os.Chtimes(path, c.now.Add(-time.Hour), c.now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	c.sn = claude.Session{ID: id, ShortID: "7a7a7a7a", PID: 9, ProcStart: "7", Host: host, Cwd: cwd,
		Status: "busy", StatusUpdatedAt: c.now.Add(-time.Hour), RemoteControl: true}
	origin := host
	promise := "inplace"
	if host == claude.HostBackground {
		origin, promise = claude.HostTerminal, "fallback"
	}
	c.s.st.Watches = []state.Watch{{SessionID: id, ShortID: "7a7a7a7a", Name: "api", Cwd: cwd, OriginHost: origin, PromiseState: promise}}
	return c
}

// look runs one check at the case's clock, with the session's tree having
// used cpu seconds so far.
func (c *frozenCase) look(cpu float64) {
	c.t.Helper()
	c.s.ask(func(ctx context.Context) Result {
		c.s.snap = observe.Snapshot{At: c.now, Sessions: []claude.Session{c.sn}}
		c.s.trees[c.sn.PID] = procs.TreeStats{CPUSeconds: cpu, PIDs: []int{c.sn.PID}, Processes: 1}
		c.s.checkFrozen(ctx, c.s.snap)
		return Result{OK: true}
	})
}

func (c *frozenCase) stops() int {
	n := 0
	for _, call := range c.f.r.CallList() {
		if strings.HasPrefix(call, "stop ") {
			n++
		}
	}
	return n
}

func (c *frozenCase) watch() state.Watch {
	c.t.Helper()
	w := c.s.find(c.sn.ID)
	if w == nil {
		c.t.Fatal("the watch is gone")
	}
	return *w
}

func (c *frozenCase) activity(sub string) int {
	n := 0
	for _, m := range logMessages(c.log) {
		if strings.Contains(m, sub) {
			n++
		}
	}
	return n
}

// A babysat background session that stays busy with nothing changing for
// FrozenAfter is stopped, so the usual rescue starts it again, and
// Activity says why.
func TestAFrozenBackgroundSessionIsStopped(t *testing.T) {
	c := newFrozenCase(t, claude.HostBackground)
	c.look(100)
	c.now = c.now.Add(10 * time.Minute)
	c.look(101)
	if c.stops() != 0 {
		t.Fatal("stopped before FrozenAfter")
	}
	c.now = c.now.Add(10 * time.Minute)
	c.look(102)
	if c.stops() != 1 {
		t.Fatalf("stops %d, calls %v", c.stops(), c.f.r.CallList())
	}
	if c.activity("not responding: busy for 20 minutes with no output and no CPU use. Stopped it to start it again in the background.") != 1 {
		t.Fatalf("activity %q", logMessages(c.log))
	}
	if w := c.watch(); len(w.Freezes) != 1 || w.Paused {
		t.Fatalf("watch %+v", w)
	}
	// The rescue that follows says why it was needed.
	c.s.ask(func(context.Context) Result {
		c.s.countAbsence(observe.Snapshot{At: c.now.Add(time.Second)})
		return Result{OK: true}
	})
	if got := c.s.knownCause(c.sn.ID); got != notRespondingCause {
		t.Fatalf("cause %q", got)
	}
}

// CPU used, a write to the transcript or a new status starts the wait
// again.
func TestAWorkingSessionIsNotFrozen(t *testing.T) {
	c := newFrozenCase(t, claude.HostBackground)
	c.look(100)
	c.now = c.now.Add(10 * time.Minute)
	c.look(100 + FrozenCPUSlack + 1)
	c.now = c.now.Add(10 * time.Minute)
	c.look(100 + FrozenCPUSlack + 1)
	if c.stops() != 0 {
		t.Fatal("a session that used CPU was stopped")
	}
	c.now = c.now.Add(5 * time.Minute)
	c.sn.StatusUpdatedAt = c.now
	c.look(100 + FrozenCPUSlack + 1)
	c.now = c.now.Add(15 * time.Minute)
	c.look(100 + FrozenCPUSlack + 1)
	if c.stops() != 0 {
		t.Fatal("a session whose status was set again was stopped")
	}
	c.sn.Status = "waiting"
	c.now = c.now.Add(30 * time.Minute)
	c.look(100 + FrozenCPUSlack + 1)
	if c.stops() != 0 {
		t.Fatal("a session waiting on a question was stopped")
	}
}

// A frozen session in a terminal or an app is not ended: the page and show
// say it is not responding, and Activity says so once.
func TestAFrozenSessionInAnAppIsOnlyFlagged(t *testing.T) {
	c := newFrozenCase(t, claude.HostTerminal)
	c.look(100)
	for range 3 {
		c.now = c.now.Add(FrozenAfter)
		c.look(100)
	}
	if c.stops() != 0 || len(c.f.p.CallList()) != 0 {
		t.Fatalf("an app's session was acted on: %v %v", c.f.r.CallList(), c.f.p.CallList())
	}
	if n := c.activity("not responding: busy for 20 minutes with no output and no CPU use. It runs in a terminal window, so it is left as it is."); n != 1 {
		t.Fatalf("activity lines %d: %q", n, logMessages(c.log))
	}
	v := viewOf(t, c.s, []claude.Session{c.sn})
	if len(v.Watches) != 1 || !v.Watches[0].NotResponding {
		t.Fatalf("watches %+v", v.Watches)
	}
	c.sn.Status = "idle"
	c.look(100)
	if v := viewOf(t, c.s, []claude.Session{c.sn}); v.Watches[0].NotResponding {
		t.Fatal("still flagged once it is idle")
	}
}

// A session that freezes MaxFreezes times within FreezeWindow is not
// started again once more: the watch is paused, and the frozen process is
// left for the person to look at.
func TestRepeatedFreezesPauseTheWatch(t *testing.T) {
	c := newFrozenCase(t, claude.HostBackground)
	c.s.st.Watches[0].Freezes = []time.Time{c.now.Add(-90 * time.Minute), c.now.Add(-40 * time.Minute)}
	c.look(100)
	c.now = c.now.Add(FrozenAfter)
	c.look(100)
	if c.stops() != 0 {
		t.Fatal("stopped on the freeze that pauses")
	}
	w := c.watch()
	if !w.Paused || w.PauseReason != "it froze three times in two hours" {
		t.Fatalf("watch %+v", w)
	}
	if !slices.ContainsFunc(logMessages(c.log), func(m string) bool { return strings.Contains(m, "froze three times in two hours") }) {
		t.Fatalf("activity %q", logMessages(c.log))
	}
}

// When `claude stop` fails, the frozen process is ended by pid and
// creation time instead.
func TestAFrozenSessionIsEndedWhenStopFails(t *testing.T) {
	c := newFrozenCase(t, claude.HostBackground)
	c.f.r.SetRespond(func([]string) (string, error) { return "", errors.New("daemon not answering") })
	c.f.p.SetAlive(c.sn.PID, true)
	c.look(100)
	c.now = c.now.Add(FrozenAfter)
	c.look(100)
	if !slices.Contains(c.f.p.CallList(), "terminate 9") {
		t.Fatalf("procs calls %v", c.f.p.CallList())
	}
	if !c.s.frozeStopped[c.sn.ID] {
		t.Fatal("its rescue would not say why")
	}
}
