package observe

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

func newTestObserver(t *testing.T, files *[][]byte, respond func([]string) (string, error)) (*Observer, *procs.Fake, *claude.FakeRunner) {
	p := procs.NewFake()
	r := claude.NewFakeRunner(respond)
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := New(func() [][]byte { return *files }, p, r, log)
	return o, p, r
}

func TestRefreshPublishesSweptAlwaysAndChangedOnChange(t *testing.T) {
	files := [][]byte{file(1, "11111111-2222-4333-8444-555555555501", "bg", "cli")}
	o, p, _ := newTestObserver(t, &files, nil)
	p.SetAlive(1, true)

	o.RefreshNow()
	select {
	case <-o.Swept:
	default:
		t.Fatal("swept expected")
	}
	select {
	case <-o.Changed:
	default:
		t.Fatal("changed expected on first sweep")
	}

	o.RefreshNow()
	select {
	case <-o.Swept:
	default:
		t.Fatal("swept expected again")
	}
	select {
	case <-o.Changed:
		t.Fatal("no change, no Changed")
	default:
	}

	files = nil
	o.RefreshNow()
	select {
	case s := <-o.Changed:
		if len(s.Sessions) != 0 {
			t.Fatal("removal must publish")
		}
	default:
		t.Fatal("changed expected on removal")
	}
}

func TestAgentsIsUnansweredWhenCLIDoesNotAnswer(t *testing.T) {
	files := [][]byte{}
	answers := []string{"error: could not start background service", "[timeout after 20s] partial", "[]"}
	i := 0
	o, _, _ := newTestObserver(t, &files, func(args []string) (string, error) {
		a := answers[i]
		i++
		if a == "[]" {
			return a, nil
		}
		return a, errors.New("exit 1")
	})
	ctx := context.Background()
	if _, answered := o.Agents(ctx); answered {
		t.Fatal("garbage is not an answer")
	}
	if _, answered := o.Agents(ctx); answered {
		t.Fatal("timeout marker is not an answer")
	}
	if list, answered := o.Agents(ctx); !answered || len(list) != 0 {
		t.Fatal("a real empty list is an answer")
	}
}

// The authority check runs before every resume and on a timer. A CLI that
// is slow to start, or missing, would otherwise write the same line into
// the activity feed over and over and bury everything worth reading; the
// first one still goes through, since that is the one that says something
// is wrong.
func TestSilentCLIIsReportedAtMostOncePerMinute(t *testing.T) {
	files := [][]byte{}
	p := procs.NewFake()
	r := claude.NewFakeRunner(func([]string) (string, error) {
		return "error: could not start background service", errors.New("exit 1")
	})
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := New(func() [][]byte { return files }, p, r, log)

	clock := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	o.now = func() time.Time { return clock }

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, ok := o.Agents(ctx); ok {
			t.Fatal("a silent CLI never answers")
		}
		clock = clock.Add(10 * time.Second)
	}
	if n := countSilenceLines(log); n != 1 {
		t.Fatalf("inside one minute the line is written once, got %d", n)
	}

	// Past the minute it is worth saying again.
	clock = clock.Add(time.Minute)
	if _, ok := o.Agents(ctx); ok {
		t.Fatal("still silent")
	}
	if n := countSilenceLines(log); n != 2 {
		t.Fatalf("after a minute the line is written again, got %d", n)
	}
}

// A machine without the claude CLI used to get "agents --json did not
// answer: " every minute, with nothing after the colon. Now it is told once
// that the CLI is not installed, and told again only after the CLI has
// answered in between.
func TestMissingCLIIsSaidOnce(t *testing.T) {
	files := [][]byte{}
	missing := true
	r := claude.NewFakeRunner(func([]string) (string, error) {
		if missing {
			return "", &exec.Error{Name: "claude", Err: exec.ErrNotFound}
		}
		return "[]", nil
	})
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := New(func() [][]byte { return files }, procs.NewFake(), r, log)
	clock := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	o.now = func() time.Time { return clock }
	ctx := context.Background()
	notInstalled := func() int {
		n := 0
		for _, e := range log.Recent(100, "") {
			if strings.Contains(e.Message, "did not answer") {
				t.Fatalf("a missing CLI is not a silent one: %q", e.Message)
			}
			if strings.Contains(e.Message, "The Claude Code CLI is not installed") {
				n++
			}
		}
		return n
	}
	for i := 0; i < 5; i++ {
		o.Agents(ctx)
		clock = clock.Add(2 * time.Minute)
	}
	if n := notInstalled(); n != 1 {
		t.Fatalf("said %d times, want once", n)
	}
	missing = false
	if _, ok := o.Agents(ctx); !ok {
		t.Fatal("an installed CLI answers")
	}
	missing = true
	o.Agents(ctx)
	if n := notInstalled(); n != 2 {
		t.Fatalf("after the CLI answered once, a new loss is said again: %d", n)
	}
}

// An empty answer logs the reason the CLI gave, not an empty line.
func TestSilentCLIGivesItsReason(t *testing.T) {
	files := [][]byte{}
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := claude.NewFakeRunner(func([]string) (string, error) { return "", errors.New("exit status 2") })
	o := New(func() [][]byte { return files }, procs.NewFake(), r, log)
	o.Agents(context.Background())
	for _, e := range log.Recent(10, "") {
		if strings.Contains(e.Message, "did not answer") {
			if !strings.HasSuffix(e.Message, "exit status 2") {
				t.Fatalf("the reason is missing: %q", e.Message)
			}
			return
		}
	}
	t.Fatal("no line was written")
}

func countSilenceLines(log *state.Log) int {
	n := 0
	for _, e := range log.Recent(100, "") {
		if strings.Contains(e.Message, "did not answer") {
			n++
		}
	}
	return n
}

func TestSweepTimerDrivesPublishing(t *testing.T) {
	files := [][]byte{}
	o, _, _ := newTestObserver(t, &files, nil)
	o.sweepEvery = 30 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o.Start(ctx, "")
	select {
	case <-o.Swept:
	case <-time.After(2 * time.Second):
		t.Fatal("sweep timer did not publish")
	}
}

// TestKickDebouncesToOneRefresh drives the sweep loop's kick channel
// directly, without any real file watcher, to check the debounce window
// collapses a burst of kicks into a single refresh. The sweep ticker is
// set far longer than the test so any refresh it counts must have come
// from the debounce timer rather than the ticker.
func TestKickDebouncesToOneRefresh(t *testing.T) {
	var calls int32
	files := [][]byte{}
	p := procs.NewFake()
	r := claude.NewFakeRunner(nil)
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := New(func() [][]byte {
		atomic.AddInt32(&calls, 1)
		return files
	}, p, r, log)
	o.sweepEvery = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o.Start(ctx, "") // Start's own initial RefreshNow accounts for one call.

	before := atomic.LoadInt32(&calls)

	for i := 0; i < 5; i++ {
		o.kick <- struct{}{}
		time.Sleep(20 * time.Millisecond) // well inside the debounce window
	}

	time.Sleep(500 * time.Millisecond) // let the debounce settle and fire once

	if after := atomic.LoadInt32(&calls); after != before+1 {
		t.Fatalf("expected exactly one debounced refresh, got %d (before=%d after=%d)", after-before, before, after)
	}
}

// TestRefreshNowConcurrentPublishNeverBlocks drives RefreshNow from two
// goroutines at once while a third drains both channels slowly. publish
// must never block the writers: the test finishes on its own well inside
// the deadline, and the race detector must find nothing to complain about.
func TestRefreshNowConcurrentPublishNeverBlocks(t *testing.T) {
	files := [][]byte{file(1, "11111111-2222-4333-8444-555555555501", "bg", "cli")}
	o, p, _ := newTestObserver(t, &files, nil)
	p.SetAlive(1, true)

	stop := make(chan struct{})
	var writers sync.WaitGroup
	writers.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer writers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					o.RefreshNow()
				}
			}
		}()
	}

	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for {
			select {
			case <-stop:
				return
			case <-o.Swept:
				time.Sleep(2 * time.Millisecond)
			case <-o.Changed:
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()

	time.Sleep(200 * time.Millisecond)
	close(stop)

	done := make(chan struct{})
	go func() {
		writers.Wait()
		<-drainDone
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent RefreshNow did not settle")
	}
}

// writeFile is a small helper for tests that exercise the real fsnotify
// path: it (re)writes a session file's full contents in one call.
func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWatcherPublishesChangedOnNewSessionFile(t *testing.T) {
	dir := t.TempDir()
	p := procs.NewFake()
	p.SetAlive(9, true)
	r := claude.NewFakeRunner(nil)
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := New(ReadSessionFiles(dir), p, r, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o.Start(ctx, dir)

	sessionPath := filepath.Join(dir, "9.json")
	deadline := time.After(3 * time.Second)
	// The retry period must stay comfortably above the observer's own
	// debounce window: rewriting faster than that would keep resetting the
	// debounce timer and the sweep would never actually run.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	writeFile(t, sessionPath, file(9, "99999999-2222-4333-8444-555555555509", "interactive", "cli"))
	for {
		select {
		case s := <-o.Changed:
			if _, ok := s.Find("99999999-2222-4333-8444-555555555509"); ok {
				return
			}
		case <-ticker.C:
			writeFile(t, sessionPath, file(9, "99999999-2222-4333-8444-555555555509", "interactive", "cli"))
		case <-deadline:
			t.Fatal("watcher did not report the new session file within 3s")
		}
	}
}

func TestWatcherPicksUpDirectoryCreatedAfterStart(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "sessions")
	p := procs.NewFake()
	p.SetAlive(5, true)
	r := claude.NewFakeRunner(nil)
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := New(ReadSessionFiles(dir), p, r, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o.Start(ctx, dir)

	// Wait for the watch goroutine to finish its initial setup (it can
	// only watch the parent at this point, since dir does not exist yet)
	// instead of guessing how long that takes with a fixed sleep.
	select {
	case <-o.watchReady:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher never became ready")
	}

	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(dir, "5.json")

	deadline := time.After(3 * time.Second)
	// See the comment in TestWatcherPublishesChangedOnNewSessionFile: this
	// period must stay above the observer's debounce window.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	writeFile(t, sessionPath, file(5, "55555555-2222-4333-8444-555555555505", "interactive", "cli"))
	for {
		select {
		case s := <-o.Changed:
			if _, ok := s.Find("55555555-2222-4333-8444-555555555505"); ok {
				return
			}
		case <-ticker.C:
			writeFile(t, sessionPath, file(5, "55555555-2222-4333-8444-555555555505", "interactive", "cli"))
		case <-deadline:
			t.Fatal("watcher did not pick up the directory created after Start within 3s")
		}
	}
}

// Peek exists for a caller the published snapshots drive: it must look at
// the world without waking that caller or moving what Current reports.
func TestPeekReadsTheFilesWithoutPublishingOrReplacingCurrent(t *testing.T) {
	files := [][]byte{}
	o, p, _ := newTestObserver(t, &files, nil)
	p.SetAlive(1, true)
	p.SetAlive(2, true)

	files = [][]byte{file(1, "11111111-2222-4333-8444-555555555501", "bg", "cli")}
	o.RefreshNow()
	<-o.Swept
	<-o.Changed
	before := o.Current()

	files = append(files, file(2, "22222222-2222-4333-8444-555555555502", "bg", "cli"))
	got := o.Peek()
	if len(got.Sessions) != 2 {
		t.Fatalf("Peek must read the files as they are now: %+v", got.Sessions)
	}
	if len(o.Current().Sessions) != len(before.Sessions) {
		t.Fatalf("Peek must not replace the current snapshot: %+v", o.Current().Sessions)
	}
	select {
	case s := <-o.Swept:
		t.Fatalf("Peek must publish nothing: %+v", s)
	default:
	}
	select {
	case s := <-o.Changed:
		t.Fatalf("Peek must publish nothing: %+v", s)
	default:
	}
}

// The supervisor tells one snapshot from the next by the time it was
// taken, so snapshots built one right after the other must each carry a
// later time than the one before, even where the clock moves in steps
// coarser than the time it takes to build one.
func TestSnapshotsBuiltBackToBackCarryLaterTimes(t *testing.T) {
	files := [][]byte{}
	o, _, _ := newTestObserver(t, &files, nil)
	var last time.Time
	for i := 0; i < 50; i++ {
		o.RefreshNow()
		at := o.Current().At
		if !at.After(last) {
			t.Fatalf("refresh %d was taken at %v, not after the snapshot before it at %v", i, at, last)
		}
		last = at
		at = o.Peek().At
		if !at.After(last) {
			t.Fatalf("peek %d was taken at %v, not after the snapshot before it at %v", i, at, last)
		}
		last = at
	}
}

// A clock that has not moved at all between two snapshots, the extreme of
// a coarse one, still gives each a later time than the one before.
func TestSnapshotsCarryLaterTimesWhenTheClockStandsStill(t *testing.T) {
	files := [][]byte{}
	o, _, _ := newTestObserver(t, &files, nil)
	clock := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	o.now = func() time.Time { return clock }
	o.RefreshNow()
	first := o.Current().At
	peeked := o.Peek().At
	o.RefreshNow()
	second := o.Current().At
	if !first.Equal(clock) || !peeked.After(first) || !second.After(peeked) {
		t.Fatalf("got %v, then %v, then %v from a clock standing at %v", first, peeked, second, clock)
	}
	// Once the clock moves on past them, snapshots carry its time again.
	clock = clock.Add(time.Second)
	o.RefreshNow()
	if got := o.Current().At; !got.Equal(clock) {
		t.Fatalf("got %v, want the clock's own %v", got, clock)
	}
}
