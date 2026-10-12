package supervise

import (
	"strings"
	"sync"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// upgradeFixture is a babysat session seen running at base in host, its
// process on Claude Code 2.1.295, while the Claude Code installed is now
// 2.1.296. Its clock is moved by the test and keeps running from there,
// since a resume waits on it for the copy it started.
type upgradeFixture struct {
	f      *fixture
	s      *Supervisor
	id     string
	host   claude.Host
	base   time.Time
	mu     sync.Mutex
	offset time.Duration
}

func newUpgradeFixture(t *testing.T, host claude.Host, installed string) *upgradeFixture {
	t.Helper()
	u := &upgradeFixture{id: "1a2b3c4d-0000-4000-8000-000000000001", host: host, base: time.Now().UTC()}
	u.f = newFixture(t, nil)
	u.f.r.SetRespond(func(args []string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case a == "agents --json":
			return "[]", nil
		case strings.HasPrefix(a, "--bg --resume"):
			return "backgrounded \u00b7 1a2b3c4d \u00b7 demo-a1", nil
		}
		return "", nil
	})
	u.f.writeTranscript(t, "/home/dev/ws", u.id)
	u.f.d.Env = func() hosts.Env {
		return hosts.Env{Platform: "linux", CLIFound: true, CLIPresent: true, CLIVersion: installed}
	}
	u.f.d.Now = func() time.Time {
		u.mu.Lock()
		defer u.mu.Unlock()
		return time.Now().UTC().Add(u.offset)
	}
	u.s, _, _ = newSup(t, u.f)
	u.s.addWatchForTest(state.Watch{SessionID: u.id, ShortID: "1a2b3c4d", Name: "demo-a1", Cwd: "/home/dev/ws",
		PromiseState: "inplace", OriginHost: host, HasSavedOptions: true})
	u.s.reconcileForTest(u.snap(0, true))
	return u
}

// snap is a snapshot taken after base, with the session running on its
// old version or gone, and the clock moved to it.
func (u *upgradeFixture) snap(after time.Duration, running bool) observe.Snapshot {
	at := u.base.Add(after)
	u.mu.Lock()
	u.offset = at.Sub(time.Now().UTC())
	u.mu.Unlock()
	sn := observe.Snapshot{At: at}
	if running {
		sn.Sessions = []claude.Session{{ID: u.id, ShortID: "1a2b3c4d", PID: 7, Host: u.host, Cwd: "/home/dev/ws", Version: "2.1.295"}}
	}
	return sn
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

// Claude Code updated itself, and replaces a background session's process
// still on the old version with one on the new version, so the session is
// gone for a moment. A babysat background session that went away on an
// older version than the one installed is left to Claude Code for a while,
// and Activity says why, once. When it is still not back after that, it is
// resumed, with the update as the reason.
func TestABackgroundSessionIsLeftToClaudeCodeWhileItUpdates(t *testing.T) {
	u := newUpgradeFixture(t, claude.HostBackground, "2.1.296")
	for i := 1; i <= 4; i++ {
		u.s.reconcileForTest(u.snap(time.Duration(i)*time.Second, false))
	}
	if n := countCalls(u.f, "--bg --resume "); n != 0 {
		t.Fatalf("resumed while Claude Code was starting it again: %v", u.f.r.CallList())
	}
	waiting := u.lines(func(e state.Entry) bool {
		return strings.Contains(e.Message, "Claude Code updated from 2.1.295 to 2.1.296")
	})
	if waiting != 1 {
		t.Fatalf("want one line saying why it waits, got %d: %v", waiting, u.f.d.Log.Recent(20, ""))
	}

	u.s.reconcileForTest(u.snap(upgradeWait+2*time.Second, false))
	if n := countCalls(u.f, "--bg --resume "); n != 1 {
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
	u := newUpgradeFixture(t, claude.HostBackground, "2.1.296")
	for i := 1; i <= 3; i++ {
		u.s.reconcileForTest(u.snap(time.Duration(i)*time.Second, false))
	}
	u.s.reconcileForTest(u.snap(4*time.Second, true))
	u.s.reconcileForTest(u.snap(upgradeWait+2*time.Second, true))
	if n := countCalls(u.f, "--bg --resume "); n != 0 {
		t.Fatalf("resumed a session Claude Code brought back: %v", u.f.r.CallList())
	}
}

// A background session that went away on the version installed, or a
// session that was not in the background, is not waited for: it is
// resumed as soon as it counts as gone.
func TestOnlyAnOutdatedBackgroundSessionIsWaitedFor(t *testing.T) {
	for _, c := range []struct {
		name      string
		host      claude.Host
		installed string
	}{
		{"same version", claude.HostBackground, "2.1.295"},
		{"version unknown", claude.HostBackground, ""},
		{"in a terminal", claude.HostTerminal, "2.1.296"},
	} {
		t.Run(c.name, func(t *testing.T) {
			u := newUpgradeFixture(t, c.host, c.installed)
			for i := 1; i <= 4; i++ {
				u.s.reconcileForTest(u.snap(time.Duration(i)*time.Second, false))
			}
			if n := countCalls(u.f, "--bg --resume "); n == 0 {
				t.Fatalf("want it resumed at once: %v", u.f.r.CallList())
			}
		})
	}
}
