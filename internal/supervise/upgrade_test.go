package supervise

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// upgradeFixture is a babysat background session seen running at base,
// with a clock the test moves forward, and a daemon log that says Claude
// Code updated itself from 2.1.295 to 2.1.296 five seconds before base.
// The clock keeps running from wherever the test set it, since a resume
// waits on it for the copy it started.
type upgradeFixture struct {
	f      *fixture
	s      *Supervisor
	id     string
	base   time.Time
	mu     sync.Mutex
	offset time.Duration
}

func newUpgradeFixture(t *testing.T) *upgradeFixture {
	t.Helper()
	u := &upgradeFixture{id: "1a2b3c4d-0000-4000-8000-000000000001", base: time.Now().UTC()}
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
	u.f.d.Now = func() time.Time {
		u.mu.Lock()
		defer u.mu.Unlock()
		return time.Now().UTC().Add(u.offset)
	}
	u.s, _, _ = newSup(t, u.f)
	u.s.addWatchForTest(state.Watch{SessionID: u.id, ShortID: "1a2b3c4d", Name: "demo-a1", Cwd: "/home/dev/ws",
		PromiseState: "inplace", OriginHost: claude.HostBackground, HasSavedOptions: true})
	u.s.reconcileForTest(u.snap(0, true))
	line := "[" + u.base.Add(-5*time.Second).Format(time.RFC3339Nano) + "] [supervisor] binary at /home/dev/.local/bin/claude changed " +
		"(/home/dev/.local/share/claude/versions/2.1.295 \u2192 /home/dev/.local/share/claude/versions/2.1.296) \u2014 self-restarting\n"
	if err := os.WriteFile(u.f.d.DaemonLog, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return u
}

// snap is a snapshot taken after base, with the session running in the
// background or not, and the clock moved to it.
func (u *upgradeFixture) snap(after time.Duration, running bool) observe.Snapshot {
	at := u.base.Add(after)
	u.mu.Lock()
	u.offset = at.Sub(time.Now().UTC())
	u.mu.Unlock()
	sn := observe.Snapshot{At: at}
	if running {
		sn.Sessions = []claude.Session{{ID: u.id, ShortID: "1a2b3c4d", PID: 7, Host: claude.HostBackground, Cwd: "/home/dev/ws"}}
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

// Claude Code updated itself and is starting its background sessions'
// processes again on the new program. A babysat background session that is
// gone for that moment is left to it, and Activity says why, once. When it
// is still not back after the wait, it is resumed, with the update as the
// reason.
func TestABackgroundSessionIsLeftToClaudeCodeWhileItUpdates(t *testing.T) {
	u := newUpgradeFixture(t)
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

	u.s.reconcileForTest(u.snap(upgradeWait+time.Second, false))
	if n := countCalls(u.f, "--bg --resume "); n != 1 {
		t.Fatalf("not resumed once the wait was over: %v", u.f.r.CallList())
	}
	if u.lines(func(e state.Entry) bool {
		return e.Automatic && e.Reason == "Claude Code updated from 2.1.295 to 2.1.296" && strings.HasPrefix(e.Message, "resumed as background")
	}) != 1 {
		t.Fatalf("the rescue does not give the update as its reason: %v", u.f.d.Log.Recent(20, ""))
	}
}

// A session Claude Code brings back on the new program within the wait is
// never resumed.
func TestABackgroundSessionClaudeCodeBringsBackIsNotResumed(t *testing.T) {
	u := newUpgradeFixture(t)
	for i := 1; i <= 3; i++ {
		u.s.reconcileForTest(u.snap(time.Duration(i)*time.Second, false))
	}
	u.s.reconcileForTest(u.snap(4*time.Second, true))
	u.s.reconcileForTest(u.snap(upgradeWait+time.Second, true))
	if n := countCalls(u.f, "--bg --resume "); n != 0 {
		t.Fatalf("resumed a session Claude Code brought back: %v", u.f.r.CallList())
	}
}
