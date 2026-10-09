package supervise

import (
	"fmt"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

func sess(pid int, id, kind, entry string, jobID string) []byte {
	j := ""
	if jobID != "" {
		j = fmt.Sprintf(`,"jobId":"%s"`, jobID)
	}
	return []byte(fmt.Sprintf(`{"pid":%d,"sessionId":"%s","cwd":"/home/dev/ws","procStart":"7","kind":"%s","entrypoint":"%s","bridgeSessionId":"b"%s}`, pid, id, kind, entry, j))
}

func snapOf(files ...[]byte) observe.Snapshot {
	return observe.Build(files, func(int, string) bool { return true }, time.Now())
}

func watch(id, short, promise string) state.Watch {
	return state.Watch{SessionID: id, ShortID: short, PromiseState: promise}
}

func TestDecideInPlace(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555501"
	w := watch(id, "11111111", "inplace")
	if Decide(w, snapOf(sess(1, id, "interactive", "claude-vscode", "")), 0) != None {
		t.Fatal("alive in its host")
	}
	if Decide(w, snapOf(sess(1, id, "interactive", "claude-vscode", ""), sess(2, id, "interactive", "cli", "")), 0) != None {
		t.Fatal("two user hosts: never act")
	}
	if Decide(w, snapOf(), 1) != None {
		t.Fatal("first absent sweep")
	}
	if Decide(w, snapOf(), 2) != Fallback {
		t.Fatal("second absent sweep falls back")
	}
	w.Paused = true
	if Decide(w, snapOf(), 5) != None {
		t.Fatal("paused never acts")
	}
}

func TestDecideFallback(t *testing.T) {
	id := "22222222-2222-4333-8444-555555555502"
	w := watch(id, "22222222", "fallback")
	ours := sess(1, id, "bg", "cli", "22222222")
	if Decide(w, snapOf(ours), 0) != None {
		t.Fatal("our copy alone")
	}
	if Decide(w, snapOf(ours, sess(2, id, "interactive", "claude-desktop", "")), 0) != None {
		t.Fatal("an app showing the session beside our copy: the copy is never stopped to make room")
	}
	if Decide(w, snapOf(ours, sess(3, id, "bg", "cli", "aabbccdd")), 0) != Dedupe {
		t.Fatal("two of ours")
	}
	if Decide(w, snapOf(), 2) != Fallback {
		t.Fatal("our copy died: resume again")
	}
}

func TestShouldPauseForFailures(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 10, 0, 0, time.UTC)
	if ShouldPauseForFailures([]time.Time{now.Add(-4 * time.Minute), now.Add(-2 * time.Minute)}, now) {
		t.Fatal("two")
	}
	if !ShouldPauseForFailures([]time.Time{now.Add(-4 * time.Minute), now.Add(-2 * time.Minute), now}, now) {
		t.Fatal("three")
	}
	if ShouldPauseForFailures([]time.Time{now.Add(-9 * time.Minute), now.Add(-2 * time.Minute), now}, now) {
		t.Fatal("one outside window")
	}
}

func TestDecideRejoin(t *testing.T) {
	id := "44444444-2222-4333-8444-555555555504"
	w := watch(id, "44444444", "fallback")
	app := sess(2, id, "interactive", "claude-desktop", "")
	if Decide(w, snapOf(app), 0) != Rejoin {
		t.Fatal("our copy is gone and an app has the session: rejoin")
	}
	if Decide(watch(id, "44444444", "inplace"), snapOf(app), 0) != None {
		t.Fatal("a watch in place is already where it should be")
	}
}

func TestStateOf(t *testing.T) {
	id := "55555555-2222-4333-8444-555555555505"
	ours := claude.Session{ID: id, ShortID: "55555555", Host: claude.HostBackground}
	app := claude.Session{ID: id, ShortID: "55555555", Host: claude.HostVSCode}
	stuck := watch(id, "55555555", "paused")
	stuck.Paused = true
	cases := []struct {
		name string
		w    state.Watch
		live []claude.Session
		want WatchState
	}{
		{"live in its app", watch(id, "55555555", "inplace"), []claude.Session{app}, StateWatching},
		{"in place but running nowhere", watch(id, "55555555", "inplace"), nil, StateStarting},
		{"our copy carries it", watch(id, "55555555", "fallback"), []claude.Session{ours}, StateInBackground},
		{"our copy beside an app", watch(id, "55555555", "fallback"), []claude.Session{ours, app}, StateInBackground},
		{"our copy died", watch(id, "55555555", "fallback"), nil, StateStarting},
		{"an app has it again", watch(id, "55555555", "fallback"), []claude.Session{app}, StateWatching},
		{"paused", stuck, []claude.Session{app}, StateStuck},
	}
	for _, c := range cases {
		if got := StateOf(c.w, c.live); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// A busy session whose signals all stay the same for FrozenAfter is
// frozen. Any change starts the quiet time again: a new status, a write to
// a transcript, a process starting or ending in its tree, or CPU used.
func TestFrozen(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 13, 0, 0, 0, time.UTC)
	var l Liveness
	l = l.Next("busy|a", 100, t0)
	if Frozen(l, "busy", t0.Add(FrozenAfter-time.Second)) {
		t.Fatal("frozen before FrozenAfter")
	}
	quiet := l.Next("busy|a", 100+FrozenCPUSlack/2, t0.Add(10*time.Minute))
	if !Frozen(quiet, "busy", t0.Add(FrozenAfter)) {
		t.Fatal("busy and unchanged for FrozenAfter is frozen")
	}
	if Frozen(quiet, "idle", t0.Add(FrozenAfter)) || Frozen(quiet, "waiting", t0.Add(FrozenAfter)) {
		t.Fatal("only a busy session can be frozen")
	}
	for name, next := range map[string]Liveness{
		"its fingerprint changed": l.Next("busy|b", 100, t0.Add(10*time.Minute)),
		"it used CPU":             l.Next("busy|a", 100+FrozenCPUSlack, t0.Add(10*time.Minute)),
		"a process in it ended":   l.Next("busy|a", 50, t0.Add(10*time.Minute)),
	} {
		if Frozen(next, "busy", t0.Add(FrozenAfter)) {
			t.Errorf("%s, yet frozen", name)
		}
		if !Frozen(next, "busy", t0.Add(10*time.Minute+FrozenAfter)) {
			t.Errorf("%s: frozen once quiet for FrozenAfter again", name)
		}
	}
	if Frozen(Liveness{}, "busy", t0) {
		t.Fatal("a session never seen is not frozen")
	}
}

// Three freezes within FreezeWindow pause the watch rather than start it
// again and again.
func TestShouldPauseForFreezes(t *testing.T) {
	now := time.Date(2026, 10, 9, 13, 0, 0, 0, time.UTC)
	two := []time.Time{now.Add(-90 * time.Minute), now.Add(-30 * time.Minute)}
	if ShouldPauseForFreezes(two, now) {
		t.Fatal("two freezes do not pause")
	}
	if !ShouldPauseForFreezes(append(two, now), now) {
		t.Fatal("three within the window pause")
	}
	old := []time.Time{now.Add(-FreezeWindow - time.Minute), now.Add(-30 * time.Minute), now}
	if ShouldPauseForFreezes(old, now) {
		t.Fatal("a freeze outside the window does not count")
	}
}
