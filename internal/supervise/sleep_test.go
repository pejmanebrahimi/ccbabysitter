package supervise

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// sleepFixture is a machine whose clock and passes the test drives, with
// the given sessions babysat and running in Claude Desktop, keep-awake held
// for them, and a power log that names cause for any sleep, without a time
// of its own.
func sleepFixture(t *testing.T, cause string, ids ...string) (*fixture, *Supervisor, *time.Time) {
	t.Helper()
	return sleepFixtureWith(t, func(context.Context, time.Time, time.Time) (string, time.Time, bool) {
		return cause, time.Time{}, cause != ""
	}, nil, ids...)
}

// sleepFixtureWith is sleepFixture with its own power log and lid.
func sleepFixtureWith(t *testing.T, sleepCause func(context.Context, time.Time, time.Time) (string, time.Time, bool), lid func() bool, ids ...string) (*fixture, *Supervisor, *time.Time) {
	t.Helper()
	f := newFixture(t, nil)
	f.r.SetRespond(resumeWithRC(f))
	f.d.Power = &fakePower{supported: true}
	f.d.SleepCause = sleepCause
	f.d.LidClosed = lid
	clock := time.Date(2026, 10, 8, 3, 0, 0, 0, time.UTC)
	f.d.Now = func() time.Time { return clock }
	s := New(*f.d)
	for _, id := range ids {
		f.writeTranscript(t, "/home/dev/ws", id)
		s.addWatchForTest(state.Watch{SessionID: id, ShortID: id[:8], Cwd: "/home/dev/ws",
			OriginHost: claude.HostDesktop, PromiseState: "inplace", HasSavedOptions: true})
	}
	return f, s, &clock
}

// awake runs n passes ten seconds apart with the sessions running.
func awake(s *Supervisor, clock *time.Time, n int, ids ...string) {
	var running []claude.Session
	for _, id := range ids {
		running = append(running, inDesktop(id))
	}
	for i := 0; i < n; i++ {
		pass(s, clock, running...)
	}
}

// sleepLines are the Activity lines that say the computer slept, which may
// take a moment to be written.
func sleepLines(t *testing.T, f *fixture, want int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var lines []string
		for _, e := range activity(f) {
			if strings.HasPrefix(e.Message, "The computer ") {
				lines = append(lines, e.Message)
			}
		}
		if len(lines) >= want || time.Now().After(deadline) {
			return lines
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A computer that slept while it was kept awake for babysat sessions, as a
// closed lid makes it, is named in Activity once it has been awake a while,
// with the time the sessions could not be reached.
func TestASleepIsNamedOnceAwake(t *testing.T) {
	a := "aaaaaaaa-0000-4000-8000-000000000020"
	f, s, clock := sleepFixture(t, "lid", a)
	awake(s, clock, 1, a)
	*clock = clock.Add(12 * time.Minute)
	awake(s, clock, 1, a)
	if lines := sleepLines(t, f, 0); len(lines) != 0 {
		t.Fatalf("named before the computer had been awake a while: %v", lines)
	}
	awake(s, clock, 14, a)
	want := "The computer slept because its lid was closed. Babysat sessions could not be reached from 03:00 to 03:12."
	if lines := sleepLines(t, f, 1); len(lines) != 1 || lines[0] != want {
		t.Fatalf("got %v, want %q", lines, want)
	}
}

// The short wakes a Mac makes with its lid shut are part of the one sleep:
// one line covers it all.
func TestShortWakesArePartOfTheSleep(t *testing.T) {
	a := "aaaaaaaa-0000-4000-8000-000000000021"
	f, s, clock := sleepFixture(t, "lid", a)
	awake(s, clock, 1, a)
	*clock = clock.Add(20 * time.Minute)
	awake(s, clock, 3, a)
	*clock = clock.Add(30 * time.Minute)
	awake(s, clock, 15, a)
	want := "The computer slept because its lid was closed. Babysat sessions could not be reached from 03:00 to 03:50."
	if lines := sleepLines(t, f, 1); len(lines) != 1 || lines[0] != want {
		t.Fatalf("got %v, want %q", lines, want)
	}
}

// With nothing babysat, the computer is free to sleep, and nothing is said.
func TestASleepWithNothingBabysatIsNotNamed(t *testing.T) {
	f, s, clock := sleepFixture(t, "lid")
	awake(s, clock, 1)
	*clock = clock.Add(time.Hour)
	awake(s, clock, 15)
	time.Sleep(100 * time.Millisecond)
	if lines := sleepLines(t, f, 0); len(lines) != 0 {
		t.Fatalf("got %v", lines)
	}
}

// What the line says for each cause, and for none, with the day added when
// the sleep went past midnight.
func TestSleptWords(t *testing.T) {
	from := time.Date(2026, 10, 8, 23, 50, 0, 0, time.UTC)
	to := time.Date(2026, 10, 9, 7, 10, 0, 0, time.UTC)
	same := time.Date(2026, 10, 9, 6, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		cause    string
		from, to time.Time
		want     string
	}{
		{"lid", same, to, "The computer slept because its lid was closed. Babysat sessions could not be reached from 06:00 to 07:10."},
		{"asked", same, to, "The computer was put to sleep. Babysat sessions could not be reached from 06:00 to 07:10."},
		{"battery", same, to, "The computer slept because its battery ran low. Babysat sessions could not be reached from 06:00 to 07:10."},
		{"", same, to, "The computer slept. Babysat sessions could not be reached from 06:00 to 07:10."},
		{"", from, to, "The computer slept. Babysat sessions could not be reached from Oct 8 23:50 to Oct 9 07:10."},
	} {
		if got := sleptWords(c.cause, c.from, c.to); got != c.want {
			t.Errorf("sleptWords(%q) = %q, want %q", c.cause, got, c.want)
		}
	}
}

// A pass or an action that takes a minute, such as a resume waiting on a
// slow CLI, is no sleep: the loop was busy, not the computer asleep.
func TestABusyLoopIsNotASleep(t *testing.T) {
	a := "aaaaaaaa-0000-4000-8000-000000000022"
	f, s, clock := sleepFixture(t, "lid", a)
	rescue := resumeWithRC(f)
	f.r.SetRespond(func(args []string) (string, error) {
		if strings.HasPrefix(strings.Join(args, " "), "--bg --resume") {
			*clock = clock.Add(70 * time.Second)
		}
		return rescue(args)
	})
	awake(s, clock, 1, a)
	awake(s, clock, 3)
	awake(s, clock, 15, a)
	time.Sleep(100 * time.Millisecond)
	if lines := sleepLines(t, f, 0); len(lines) != 0 {
		t.Fatalf("a busy minute was named a sleep: %v", lines)
	}
}

// fakeLid is a lid the test opens and closes.
type fakeLid struct {
	mu     sync.Mutex
	closed bool
}

func (l *fakeLid) get() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.closed }

func (l *fakeLid) set(closed bool) { l.mu.Lock(); defer l.mu.Unlock(); l.closed = closed }

// A Mac with its lid shut can wake for an hour in the dark. While the lid
// stays shut, all of it is the one sleep, named once the lid is open, with
// the lid as its cause even when the power log names none.
func TestALongWakeWithTheLidShutIsTheSameSleep(t *testing.T) {
	a := "aaaaaaaa-0000-4000-8000-000000000023"
	lid := &fakeLid{}
	f, s, clock := sleepFixtureWith(t, func(context.Context, time.Time, time.Time) (string, time.Time, bool) {
		return "", time.Time{}, false
	}, lid.get, a)
	awake(s, clock, 1, a)
	lid.set(true)
	*clock = clock.Add(20 * time.Minute)
	awake(s, clock, 60, a)
	*clock = clock.Add(30 * time.Minute)
	awake(s, clock, 1, a)
	lid.set(false)
	awake(s, clock, 15, a)
	want := "The computer slept because its lid was closed. Babysat sessions could not be reached from 03:00 to 04:00."
	if lines := sleepLines(t, f, 1); len(lines) != 1 || lines[0] != want {
		t.Fatalf("got %v, want %q", lines, want)
	}
}

// The power log knows when the computer went to sleep, which is earlier
// than the last pass before it when a short wake came in between.
func TestThePowerLogGivesTheStart(t *testing.T) {
	a := "aaaaaaaa-0000-4000-8000-000000000024"
	f, s, clock := sleepFixtureWith(t, func(context.Context, time.Time, time.Time) (string, time.Time, bool) {
		return "lid", time.Date(2026, 10, 8, 2, 58, 30, 0, time.UTC), true
	}, nil, a)
	awake(s, clock, 1, a)
	*clock = clock.Add(12 * time.Minute)
	awake(s, clock, 15, a)
	want := "The computer slept because its lid was closed. Babysat sessions could not be reached from 02:58 to 03:12."
	if lines := sleepLines(t, f, 1); len(lines) != 1 || lines[0] != want {
		t.Fatalf("got %v, want %q", lines, want)
	}
}

// A gap the power log shows no sleep in, such as the clock set forward, is
// no sleep.
func TestAGapThePowerLogDoesNotShowIsNoSleep(t *testing.T) {
	a := "aaaaaaaa-0000-4000-8000-000000000025"
	f, s, clock := sleepFixtureWith(t, func(context.Context, time.Time, time.Time) (string, time.Time, bool) {
		return "", time.Time{}, true
	}, nil, a)
	awake(s, clock, 1, a)
	*clock = clock.Add(12 * time.Minute)
	awake(s, clock, 15, a)
	time.Sleep(100 * time.Millisecond)
	if lines := sleepLines(t, f, 0); len(lines) != 0 {
		t.Fatalf("got %v", lines)
	}
}

// Where nothing tells why the computer slept, the line names the sleep
// alone.
func TestASleepWithNoLogIsNamedAlone(t *testing.T) {
	a := "aaaaaaaa-0000-4000-8000-000000000026"
	f, s, clock := sleepFixtureWith(t, nil, nil, a)
	awake(s, clock, 1, a)
	*clock = clock.Add(12 * time.Minute)
	awake(s, clock, 15, a)
	want := "The computer slept. Babysat sessions could not be reached from 03:00 to 03:12."
	if lines := sleepLines(t, f, 1); len(lines) != 1 || lines[0] != want {
		t.Fatalf("got %v, want %q", lines, want)
	}
}
