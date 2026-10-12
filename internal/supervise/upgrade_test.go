package supervise

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// upgradeCase is what an upgradeFixture is made of: the app the session
// runs in, the Claude Code version its process runs, the installed version
// CC Babysitter last read, and what claude --version answers now.
type upgradeCase struct {
	host            claude.Host
	session         string
	cached, current string
}

// outdated is the usual case: a background session on 2.1.295, while
// Claude Code 2.1.296 is installed and known to be.
var outdated = upgradeCase{host: claude.HostBackground, session: "2.1.295", cached: "2.1.296", current: "2.1.296"}

// upgradeFixture is a babysat session seen running at base. Its clock is
// moved by the test and keeps running from there, since a resume waits on
// it for the copy it started.
type upgradeFixture struct {
	f      *fixture
	s      *Supervisor
	id     string
	c      upgradeCase
	base   time.Time
	mu     sync.Mutex
	offset time.Duration
}

func newUpgradeFixture(t *testing.T, c upgradeCase) *upgradeFixture {
	t.Helper()
	u := &upgradeFixture{id: "1a2b3c4d-0000-4000-8000-000000000001", c: c, base: time.Now().UTC()}
	u.f = newFixture(t, nil)
	u.f.r.SetRespond(func(args []string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case a == "agents --json":
			return "[]", nil
		case a == "--version":
			return c.current + " (Claude Code)", nil
		case strings.HasPrefix(a, "--bg --resume"):
			return "backgrounded \u00b7 1a2b3c4d \u00b7 demo-a1", nil
		}
		return "", nil
	})
	u.f.writeTranscript(t, "/home/dev/ws", u.id)
	u.f.d.Env = func() hosts.Env {
		return hosts.Env{Platform: "linux", CLIFound: true, CLIPresent: true, CLIVersion: c.cached}
	}
	u.f.d.Now = func() time.Time {
		u.mu.Lock()
		defer u.mu.Unlock()
		return time.Now().UTC().Add(u.offset)
	}
	u.s, _, _ = newSup(t, u.f)
	u.s.addWatchForTest(state.Watch{SessionID: u.id, ShortID: "1a2b3c4d", Name: "demo-a1", Cwd: "/home/dev/ws",
		PromiseState: "inplace", OriginHost: c.host, HasSavedOptions: true})
	u.s.reconcileForTest(u.snap(0, true))
	return u
}

// snap is a snapshot taken after base, with the session running or gone,
// and the clock moved to it.
func (u *upgradeFixture) snap(after time.Duration, running bool) observe.Snapshot {
	at := u.base.Add(after)
	u.mu.Lock()
	u.offset = at.Sub(time.Now().UTC())
	u.mu.Unlock()
	sn := observe.Snapshot{At: at}
	if running {
		sn.Sessions = []claude.Session{{ID: u.id, ShortID: "1a2b3c4d", PID: 7, Host: u.c.host, Cwd: "/home/dev/ws", Version: u.c.session}}
	}
	return sn
}

// goneFor runs passes a second apart with the session gone, from one
// second after base to n seconds after.
func (u *upgradeFixture) goneFor(n int) {
	for i := 1; i <= n; i++ {
		u.s.reconcileForTest(u.snap(time.Duration(i)*time.Second, false))
	}
}

func (u *upgradeFixture) lines(match func(state.Entry) bool) int {
	n := 0
	for _, e := range u.f.d.Log.Recent(100, "") {
		if match(e) {
			n++
		}
	}
	return n
}

func (u *upgradeFixture) resumes() int { return countCalls(u.f, "--bg --resume ") }

// Claude Code updated itself, and replaces a background session's process
// still on the old version with one on the new version, so the session is
// gone for a moment. A babysat background session that went away on an
// older version than the one installed is left to Claude Code for a while,
// and Activity says why, once. When it is still not back after that, it is
// resumed, with the update as the reason.
func TestABackgroundSessionIsLeftToClaudeCodeWhileItUpdates(t *testing.T) {
	u := newUpgradeFixture(t, outdated)
	u.goneFor(4)
	if n := u.resumes(); n != 0 {
		t.Fatalf("resumed while Claude Code was starting it again: %v", u.f.r.CallList())
	}
	waiting := u.lines(func(e state.Entry) bool {
		return strings.Contains(e.Message, "Claude Code updated from 2.1.295 to 2.1.296")
	})
	if waiting != 1 {
		t.Fatalf("want one line saying why it waits, got %d: %v", waiting, u.f.d.Log.Recent(20, ""))
	}

	u.s.reconcileForTest(u.snap(upgradeWait+2*time.Second, false))
	if n := u.resumes(); n != 1 {
		t.Fatalf("not resumed once the wait was over: %v", u.f.r.CallList())
	}
	if u.lines(func(e state.Entry) bool {
		return e.Automatic && e.Reason == "Claude Code updated from 2.1.295 to 2.1.296" && strings.HasPrefix(e.Message, "resumed as background")
	}) != 1 {
		t.Fatalf("the rescue does not give the update as its reason: %v", u.f.d.Log.Recent(20, ""))
	}
}

// A session Claude Code brings back on the new version within the wait is
// never resumed.
func TestABackgroundSessionClaudeCodeBringsBackIsNotResumed(t *testing.T) {
	u := newUpgradeFixture(t, outdated)
	u.goneFor(3)
	u.s.reconcileForTest(u.snap(4*time.Second, true))
	u.s.reconcileForTest(u.snap(upgradeWait+2*time.Second, true))
	if n := u.resumes(); n != 0 {
		t.Fatalf("resumed a session Claude Code brought back: %v", u.f.r.CallList())
	}
}

// The installed version CC Babysitter keeps can be a minute old, and
// Claude Code can replace a process sooner than that after updating. When
// the version kept is the session's own, the CLI is asked, once.
func TestAnUpdateNotYetReadIsAskedFor(t *testing.T) {
	u := newUpgradeFixture(t, upgradeCase{host: claude.HostBackground, session: "2.1.295", cached: "2.1.295", current: "2.1.296"})
	u.goneFor(4)
	if n := u.resumes(); n != 0 {
		t.Fatalf("resumed while Claude Code was starting it again: %v", u.f.r.CallList())
	}
	if n := countCalls(u.f, "--version"); n != 1 {
		t.Fatalf("want the CLI asked once, got %d: %v", n, u.f.r.CallList())
	}
}

// A background session that went away on the version installed, or a newer
// one, or with no version known, and a session that was not in the
// background, is not waited for: it is resumed as soon as it counts as
// gone.
func TestOnlyAnOutdatedBackgroundSessionIsWaitedFor(t *testing.T) {
	for name, c := range map[string]upgradeCase{
		"same version":    {host: claude.HostBackground, session: "2.1.295", cached: "2.1.295", current: "2.1.295"},
		"newer session":   {host: claude.HostBackground, session: "2.1.297", cached: "2.1.296", current: "2.1.296"},
		"version unknown": {host: claude.HostBackground, session: "2.1.295", cached: "", current: ""},
		"in a terminal":   {host: claude.HostTerminal, session: "2.1.295", cached: "2.1.296", current: "2.1.296"},
	} {
		t.Run(name, func(t *testing.T) {
			u := newUpgradeFixture(t, c)
			u.goneFor(4)
			if n := u.resumes(); n == 0 {
				t.Fatalf("want it resumed at once: %v", u.f.r.CallList())
			}
		})
	}
}

// A session CC Babysitter stopped itself for freezing is still on the old
// version when Claude Code did not get to replace it, but nothing will
// start it again: it is resumed at once, and the reason stays why it was
// stopped.
func TestASessionStoppedForFreezingIsNotWaitedFor(t *testing.T) {
	u := newUpgradeFixture(t, outdated)
	// The stop and the looks after it run as one step on the loop, as they
	// do for real, so no other snapshot counts the session gone first.
	running, gone := u.snap(time.Second, true), []observe.Snapshot{u.snap(2*time.Second, false), u.snap(3*time.Second, false), u.snap(4*time.Second, false)}
	u.s.ask(func(ctx context.Context) Result {
		u.s.reconcile(ctx, running)
		u.s.frozeStopped[u.id] = u.s.deps.Now()
		for _, sn := range gone {
			u.s.reconcile(ctx, sn)
		}
		return Result{OK: true}
	})
	if n := u.resumes(); n == 0 {
		t.Fatalf("want it resumed at once: %v\n%v", u.f.r.CallList(), u.f.d.Log.Recent(20, ""))
	}
	if u.lines(func(e state.Entry) bool { return strings.Contains(e.Message+e.Reason, "Claude Code updated") }) != 0 {
		t.Fatalf("an update was given for a session stopped for freezing: %v", u.f.d.Log.Recent(20, ""))
	}
	if u.lines(func(e state.Entry) bool { return e.Automatic && e.Reason == notRespondingCause }) == 0 {
		t.Fatalf("the rescue does not say it stopped responding: %v", u.f.d.Log.Recent(20, ""))
	}
}

// The update explains a session going away only for a while after it
// went: a rescue long after, such as from Try again, gives the plain
// reason.
func TestTheUpdateReasonIsOnlyForARecentExit(t *testing.T) {
	u := newUpgradeFixture(t, outdated)
	u.goneFor(2)
	got := ""
	u.s.reconcileForTest(u.snap(retireWindow+time.Minute, false))
	u.s.ask(func(context.Context) Result {
		got = u.s.exitReason(u.id, "1a2b3c4d", time.Time{})
		return Result{OK: true}
	})
	if strings.Contains(got, "Claude Code updated") {
		t.Fatalf("an old update still given as the reason: %q", got)
	}
}

// The update reason comes from the sessions' own files, not the daemon's
// log, so it is given where there is no daemon log to read.
func TestTheUpdateReasonNeedsNoDaemonLog(t *testing.T) {
	u := newUpgradeFixture(t, outdated)
	u.goneFor(2)
	got := ""
	u.s.ask(func(context.Context) Result {
		u.s.deps.DaemonLog = ""
		got = u.s.exitReason(u.id, "1a2b3c4d", time.Time{})
		return Result{OK: true}
	})
	if got != "Claude Code updated from 2.1.295 to 2.1.296" {
		t.Fatalf("reason %q", got)
	}
}

func TestOlderVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"2.1.295", "2.1.296", true},
		{"2.1.296", "2.1.295", false},
		{"2.1.296", "2.1.296", false},
		{"2.1.99", "2.1.100", true},
		{"2.1", "2.1.1", true},
		{"2.1.x", "2.1.296", false},
		{"", "2.1.296", false},
	} {
		if got := olderVersion(c.a, c.b); got != c.want {
			t.Errorf("olderVersion(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
