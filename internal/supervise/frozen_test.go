package supervise

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// frozenCase is a supervisor that is not running, with one babysat session
// in host that is busy, waiting on the model, and whose transcript was last
// written an hour before the start, and a clock the test moves.
type frozenCase struct {
	t        *testing.T
	f        *fixture
	s        *Supervisor
	log      *state.Log
	now      time.Time
	sn       claude.Session
	others   []claude.Session
	awaiting bool
	cpu      float64
	pids     []int
	path     string
	ctx      context.Context
}

func newFrozenCase(t *testing.T, host claude.Host) *frozenCase {
	t.Helper()
	c := &frozenCase{t: t, now: time.Date(2026, 10, 9, 13, 0, 0, 0, time.UTC), awaiting: true, cpu: 100, pids: []int{9}, ctx: context.Background()}
	c.f = newFixture(t, func(args []string) (string, error) {
		if len(args) > 0 && args[0] == "stop" {
			c.f.p.SetAlive(9, false)
			return "stopped " + args[len(args)-1], nil
		}
		return "[]", nil
	})
	c.f.d.Now = func() time.Time { return c.now }
	c.f.p.SetAlive(9, true)
	c.log = c.f.d.Log
	c.s = New(*c.f.d)
	id := "7a7a7a7a-0000-4000-8000-0000000000f1"
	cwd := "/home/dev/ws"
	c.f.writeTranscript(t, cwd, id)
	c.path = filepath.Join(c.f.d.ProjectsDir, claude.Slug(cwd), id+".jsonl")
	c.touch(c.now.Add(-time.Hour))
	c.sn = claude.Session{ID: id, ShortID: "1b2c3d4e", PID: 9, ProcStart: "7", Host: host, Cwd: cwd,
		Status: "busy", StatusUpdatedAt: c.now.Add(-time.Hour), RemoteControl: true}
	origin, promise := host, "inplace"
	if host == claude.HostBackground {
		origin, promise = claude.HostTerminal, "fallback"
	}
	c.s.st.Watches = []state.Watch{{SessionID: id, ShortID: "1b2c3d4e", Name: "api", Cwd: cwd, OriginHost: origin, PromiseState: promise}}
	return c
}

// touch sets when the transcript was last written.
func (c *frozenCase) touch(at time.Time) {
	c.t.Helper()
	if err := os.Chtimes(c.path, at, at); err != nil {
		c.t.Fatal(err)
	}
}

// look runs one check at the case's clock.
func (c *frozenCase) look() {
	c.t.Helper()
	c.s.ask(func(context.Context) Result {
		c.s.snap = observe.Snapshot{At: c.now, Sessions: append([]claude.Session{c.sn}, c.others...)}
		c.s.trees[c.sn.PID] = procs.TreeStats{CPUSeconds: c.cpu, PIDs: c.pids, Processes: len(c.pids)}
		c.s.stats[c.sn.ID] = claude.Stats{AwaitingModel: c.awaiting}
		c.s.checkFrozen(c.ctx, c.s.snap)
		return Result{OK: true}
	})
}

// wait looks every 30 seconds for d.
func (c *frozenCase) wait(d time.Duration) {
	c.t.Helper()
	for end := c.now.Add(d); c.now.Before(end); {
		c.now = c.now.Add(30 * time.Second)
		c.look()
	}
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

// entries are the Activity entries whose message contains sub.
func (c *frozenCase) entries(sub string) []state.Entry {
	var out []state.Entry
	for _, e := range c.log.Recent(50, "") {
		if strings.Contains(e.Message, sub) {
			out = append(out, e)
		}
	}
	return out
}

// A babysat background session busy waiting on the model, with nothing
// changing for FrozenAfter, is stopped, so the usual rescue starts it
// again, and Activity says why as an automatic action.
func TestAFrozenBackgroundSessionIsStopped(t *testing.T) {
	c := newFrozenCase(t, claude.HostBackground)
	c.look()
	c.wait(FrozenAfter - time.Minute)
	if c.stops() != 0 {
		t.Fatal("stopped before FrozenAfter")
	}
	c.wait(time.Minute)
	if c.stops() != 1 || !slices.Contains(c.f.r.CallList(), "stop 1b2c3d4e") {
		t.Fatalf("calls %v", c.f.r.CallList())
	}
	got := c.entries("stopped it to start it again in the background")
	if len(got) != 1 || !got[0].Automatic || got[0].Reason != notRespondingWhy() {
		t.Fatalf("activity %+v", c.log.Recent(10, ""))
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

// Anything that changes starts the wait again: CPU used, its status set
// again, a write to its transcripts, a process starting in its tree, or a
// look after the computer slept. A session running a tool, waiting to
// retry or waiting on a question is never frozen.
func TestAWorkingSessionIsNotFrozen(t *testing.T) {
	for name, change := range map[string]func(c *frozenCase){
		"it used CPU":              func(c *frozenCase) { c.cpu += FrozenCPUSlack + 1 },
		"its status was set again": func(c *frozenCase) { c.sn.StatusUpdatedAt = c.now },
		"a transcript was written": func(c *frozenCase) { c.touch(c.now) },
		"a process started":        func(c *frozenCase) { c.pids = []int{9, 12} },
		"the computer slept":       func(c *frozenCase) { c.now = c.now.Add(30 * time.Minute) },
	} {
		c := newFrozenCase(t, claude.HostBackground)
		c.look()
		c.wait(10 * time.Minute)
		change(c)
		c.look()
		c.wait(FrozenAfter - time.Minute)
		if c.stops() != 0 {
			t.Errorf("%s, yet it was stopped", name)
		}
		c.wait(2 * time.Minute)
		if c.stops() != 1 {
			t.Errorf("%s: not stopped once quiet for FrozenAfter again", name)
		}
	}
	for name, set := range map[string]func(c *frozenCase){
		"a tool is running, or a retry is waited out": func(c *frozenCase) { c.awaiting = false },
		"it waits on a question":                      func(c *frozenCase) { c.sn.Status = "waiting" },
	} {
		c := newFrozenCase(t, claude.HostBackground)
		set(c)
		c.look()
		c.wait(2 * FrozenAfter)
		if c.stops() != 0 {
			t.Errorf("%s, yet it was stopped", name)
		}
	}
}

// A tree whose CPU time could not be read is never taken for quiet.
func TestUnknownCPUStartsTheWaitAgain(t *testing.T) {
	c := newFrozenCase(t, claude.HostBackground)
	c.look()
	for end := c.now.Add(2 * FrozenAfter); c.now.Before(end); {
		c.now = c.now.Add(30 * time.Second)
		c.s.ask(func(context.Context) Result {
			c.s.snap = observe.Snapshot{At: c.now, Sessions: []claude.Session{c.sn}}
			c.s.trees[c.sn.PID] = procs.TreeStats{CPUSeconds: c.cpu, CPUUnknown: true, PIDs: c.pids, Processes: 1}
			c.s.stats[c.sn.ID] = claude.Stats{AwaitingModel: true}
			c.s.checkFrozen(c.ctx, c.s.snap)
			return Result{OK: true}
		})
	}
	if c.stops() != 0 {
		t.Fatal("stopped without knowing its CPU time")
	}
}

// A frozen session in a terminal or an app is not ended: the page and show
// say it is not responding, and Activity says so once.
func TestAFrozenSessionInAnAppIsOnlyFlagged(t *testing.T) {
	c := newFrozenCase(t, claude.HostTerminal)
	c.look()
	c.wait(3 * FrozenAfter)
	if c.stops() != 0 || len(c.f.p.CallList()) != 0 {
		t.Fatalf("an app's session was acted on: %v %v", c.f.r.CallList(), c.f.p.CallList())
	}
	if n := len(c.entries("It runs in a terminal window, so it is left as it is.")); n != 1 {
		t.Fatalf("activity lines %d: %q", n, logMessages(c.log))
	}
	v := viewOf(t, c.s, []claude.Session{c.sn})
	if len(v.Watches) != 1 || !v.Watches[0].NotResponding {
		t.Fatalf("watches %+v", v.Watches)
	}
	c.sn.Status = "idle"
	c.look()
	if v := viewOf(t, c.s, []claude.Session{c.sn}); v.Watches[0].NotResponding {
		t.Fatal("still flagged once it is idle")
	}
}

// Sessions are controlled only through the claude CLI: when `claude stop`
// fails, or leaves the process running, nothing else ends it. Activity
// says how to stop it by hand, and no freeze is counted.
func TestAFrozenSessionIsOnlyStoppedThroughTheCLI(t *testing.T) {
	defer func(d time.Duration) { frozenStopWait = d }(frozenStopWait)
	frozenStopWait = 50 * time.Millisecond
	for name, respond := range map[string]func([]string) (string, error){
		"stop failed":          func([]string) (string, error) { return "daemon not answering", errors.New("exit status 1") },
		"stop left it running": func([]string) (string, error) { return "stopped", nil },
	} {
		c := newFrozenCase(t, claude.HostBackground)
		c.f.r.SetRespond(respond)
		c.look()
		c.wait(FrozenAfter)
		if len(c.f.p.CallList()) != 0 {
			t.Errorf("%s: a process was ended directly: %v", name, c.f.p.CallList())
		}
		if len(c.watch().Freezes) != 0 || len(c.entries("stopped it to start it again")) != 0 {
			t.Errorf("%s: counted as stopped", name)
		}
		if len(c.entries("Stop it by hand with `claude stop 1b2c3d4e`")) != 1 {
			t.Errorf("%s: activity %q", name, logMessages(c.log))
		}
	}
}

// A copy whose short id an app session of the same conversation also
// answers to is never named in `claude stop`, and nothing else ends it:
// Activity says how to find and stop it by hand.
func TestASharedShortIDIsNeverNamed(t *testing.T) {
	c := newFrozenCase(t, claude.HostBackground)
	c.others = []claude.Session{{ID: c.sn.ID, ShortID: c.sn.ShortID, PID: 30, ProcStart: "8", Host: claude.HostTerminal, Cwd: c.sn.Cwd, Status: "idle"}}
	c.look()
	c.wait(FrozenAfter)
	if c.stops() != 0 || len(c.f.p.CallList()) != 0 {
		t.Fatalf("acted on a shared short id: %v %v", c.f.r.CallList(), c.f.p.CallList())
	}
	if len(c.entries("`claude agents`")) != 1 {
		t.Fatalf("activity %q", logMessages(c.log))
	}
}

// A copy that went by itself in the meantime is not stopped, not counted
// as a freeze, and its rescue is not put down to one.
func TestACopyAlreadyGoneIsNotCountedAsAFreeze(t *testing.T) {
	c := newFrozenCase(t, claude.HostBackground)
	c.look()
	c.wait(FrozenAfter - time.Minute)
	c.f.p.SetAlive(9, false)
	c.wait(2 * time.Minute)
	if c.stops() != 0 || len(c.watch().Freezes) != 0 || len(c.entries("stopped it to start it again")) != 0 {
		t.Fatalf("a copy already gone was handled as a freeze: %v %+v", c.f.r.CallList(), c.watch())
	}
	if _, ok := c.s.frozeStopped[c.sn.ID]; ok {
		t.Fatal("its rescue would blame a freeze")
	}
}

// Nothing is stopped or ended while the program shuts down: quitting
// leaves babysat sessions running.
func TestNothingIsStoppedDuringShutdown(t *testing.T) {
	c := newFrozenCase(t, claude.HostBackground)
	c.look()
	c.wait(FrozenAfter - time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.ctx = ctx
	c.wait(2 * time.Minute)
	if c.stops() != 0 || len(c.f.p.CallList()) != 0 || len(c.watch().Freezes) != 0 {
		t.Fatalf("acted during shutdown: %v %v", c.f.r.CallList(), c.f.p.CallList())
	}
}

// A session that freezes MaxFreezes times within FreezeWindow is not
// started again once more: the watch is paused, and the frozen copy is
// left for the person to look at. Try again stops it so the rescue starts
// it, with the count cleared.
func TestRepeatedFreezesPauseUntilTryAgain(t *testing.T) {
	c := newFrozenCase(t, claude.HostBackground)
	c.s.st.Watches[0].Freezes = []time.Time{c.now.Add(-90 * time.Minute), c.now.Add(-40 * time.Minute)}
	c.look()
	c.wait(FrozenAfter)
	if c.stops() != 0 {
		t.Fatal("stopped on the freeze that pauses")
	}
	w := c.watch()
	if !w.Paused || w.PauseReason != frozePause {
		t.Fatalf("watch %+v", w)
	}
	if got := c.entries("stuck until you press Try again"); len(got) != 1 || !got[0].Automatic || got[0].Reason != frozePause {
		t.Fatalf("activity %+v", c.log.Recent(10, ""))
	}
	c.s.ask(func(context.Context) Result {
		c.s.snap = observe.Snapshot{At: c.now, Sessions: []claude.Session{c.sn}}
		return Result{OK: true}
	})
	if r := c.s.ResumeWatch(c.sn.ID, ViaPage); !r.OK {
		t.Fatalf("try again: %+v", r)
	}
	if c.stops() != 1 {
		t.Fatalf("Try again did not stop the frozen copy: %v", c.f.r.CallList())
	}
	if w := c.watch(); w.Paused || len(w.Freezes) != 0 {
		t.Fatalf("watch after Try again %+v", w)
	}
}

// Try again stops only the copy that froze, and only while it still looks
// frozen: one that came back to life, or a new copy, is just watched.
func TestTryAgainLeavesACopyThatRecovered(t *testing.T) {
	for name, change := range map[string]func(c *frozenCase){
		"it used CPU":        func(c *frozenCase) { c.cpu += FrozenCPUSlack + 1 },
		"it wrote":           func(c *frozenCase) { c.touch(c.now) },
		"it is another copy": func(c *frozenCase) { c.sn.PID, c.pids = 10, []int{10}; c.f.p.SetAlive(10, true) },
		"it answered a tool": func(c *frozenCase) { c.awaiting = false },
	} {
		c := newFrozenCase(t, claude.HostBackground)
		c.s.st.Watches[0].Freezes = []time.Time{c.now.Add(-90 * time.Minute), c.now.Add(-40 * time.Minute)}
		c.look()
		c.wait(FrozenAfter)
		if !c.watch().Paused {
			t.Fatalf("%s: not paused", name)
		}
		change(c)
		c.now = c.now.Add(time.Minute)
		c.s.ask(func(context.Context) Result {
			c.s.snap = observe.Snapshot{At: c.now, Sessions: []claude.Session{c.sn}}
			c.s.trees[c.sn.PID] = procs.TreeStats{CPUSeconds: c.cpu, PIDs: c.pids, Processes: len(c.pids)}
			c.s.stats[c.sn.ID] = claude.Stats{AwaitingModel: c.awaiting}
			return Result{OK: true}
		})
		if r := c.s.ResumeWatch(c.sn.ID, ViaPage); !r.OK {
			t.Fatalf("%s: try again: %+v", name, r)
		}
		if c.stops() != 0 {
			t.Errorf("%s: Try again stopped it: %v", name, c.f.r.CallList())
		}
		if c.watch().Paused {
			t.Errorf("%s: still paused", name)
		}
	}
}

// From end to end, with the loop running: a babysat background session
// that froze waiting on the model is stopped, then the usual rescue starts
// it again in the background, and its Activity line says why.
func TestAFrozenSessionIsStartedAgainWithItsReason(t *testing.T) {
	id := "7a7a7a7a-0000-4000-8000-0000000000f2"
	cwd := "/home/dev/ws"
	busy := fmt.Sprintf(`{"pid":9,"sessionId":"%s","cwd":"%s","procStart":"7","kind":"bg","entrypoint":"cli","jobId":"1b2c3d4e","status":"busy","statusUpdatedAt":1791550000000,"bridgeSessionId":"b"}`, id, cwd)
	again := fmt.Sprintf(`{"pid":11,"sessionId":"%s","cwd":"%s","procStart":"7","kind":"bg","entrypoint":"cli","jobId":"1b2c3d4e","status":"idle","bridgeSessionId":"b"}`, id, cwd)
	f := newFixture(t, nil)
	f.r.SetRespond(func(args []string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case a == "agents --json":
			return "[]", nil
		case a == "stop 1b2c3d4e":
			f.p.SetAlive(9, false)
			f.setFiles()
			return "stopped 1b2c3d4e", nil
		case strings.HasPrefix(a, "--bg --resume"):
			f.p.SetAlive(11, true)
			f.setFiles([]byte(again))
			return "backgrounded \u00b7 1b2c3d4e \u00b7 api", nil
		}
		return "", nil
	})
	// The transcript ends on a tool's result: the model is due to answer.
	dir := filepath.Join(f.d.ProjectsDir, claude.Slug(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	lines := `{"type":"user","timestamp":"2026-10-09T13:00:00Z","message":{"role":"user","content":"go"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-10-09T13:00:05Z","message":{"id":"m1","stop_reason":"tool_use","content":[{"type":"tool_use"}]}}` + "\n" +
		`{"type":"user","timestamp":"2026-10-09T13:00:09Z","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	var offset atomic.Int64
	base := time.Now()
	f.d.Now = func() time.Time { return base.Add(time.Duration(offset.Load())) }
	if err := f.d.Store.Save(state.State{Watches: []state.Watch{{SessionID: id, ShortID: "1b2c3d4e", Name: "api", Cwd: cwd,
		OriginHost: claude.HostTerminal, PromiseState: "fallback", HasSavedOptions: true}}}); err != nil {
		t.Fatal(err)
	}
	f.p.SetAlive(9, true)
	f.p.SetStats(9, procs.TreeStats{CPUSeconds: 100, PIDs: []int{9}, Processes: 1})
	f.setFiles([]byte(busy))
	f.d.Obs.RefreshNow()
	s, _, cancel := newSup(t, f)
	defer cancel()

	stopped := false
	for i := 0; i < 2*int(FrozenAfter/(20*time.Second)) && !stopped; i++ {
		offset.Add(int64(20 * time.Second))
		f.d.Obs.RefreshNow()
		time.Sleep(20 * time.Millisecond)
		stopped = slices.Contains(f.r.CallList(), "stop 1b2c3d4e")
	}
	if !stopped {
		t.Fatalf("never stopped: %v", f.r.CallList())
	}
	if !waitFor(t, f, func() bool {
		return slices.ContainsFunc(f.r.CallList(), func(c string) bool { return strings.HasPrefix(c, "--bg --resume "+id) })
	}) {
		t.Fatalf("not started again: %v", f.r.CallList())
	}
	if !waitFor(t, f, func() bool {
		return slices.ContainsFunc(f.d.Log.Recent(50, ""), func(e state.Entry) bool {
			return e.Automatic && strings.Contains(e.Reason, notRespondingCause) && strings.Contains(e.Message, "resumed")
		})
	}) {
		t.Fatalf("activity %+v", f.d.Log.Recent(20, ""))
	}
	if !waitFor(t, f, func() bool {
		w, ok := firstWatch(s)
		return ok && w.State == StateInBackground && len(w.Freezes) == 1
	}) {
		w, _ := firstWatch(s)
		t.Fatalf("watch %+v", w)
	}
}
