package state

import (
	"os"
	"testing"
)

func TestLockIsExclusive(t *testing.T) {
	dir := t.TempDir()
	l1, err := Acquire(dir, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(dir, 1000); err == nil {
		t.Fatal("second acquire must fail")
	}
	if !IsHeld(dir) {
		t.Fatal("IsHeld")
	}
	l1.Release()
	if IsHeld(dir) {
		t.Fatal("released")
	}
	l2, err := Acquire(dir, 2000)
	if err != nil {
		t.Fatal(err)
	}
	l2.Release()
}

func TestLockReclaimedWhenCheckerSaysDead(t *testing.T) {
	dir := t.TempDir()
	l1, err := Acquire(dir, 1000)
	if err != nil {
		t.Fatal(err)
	}
	defer l1.Release()

	SetPIDChecker(func(pid int, createMs int64) bool { return false })
	t.Cleanup(func() { SetPIDChecker(func(pid int, createMs int64) bool { return true }) })

	if IsHeld(dir) {
		t.Fatal("lock should not be held once the checker reports it dead")
	}
	l2, err := Acquire(dir, 3000)
	if err != nil {
		t.Fatalf("expected the stale lock to be reclaimed, got %v", err)
	}
	l2.Release()
}

// Two copies starting at the same instant can both find the same stale
// lock. The one that gets there second must not delete the lock the first
// has already written in its place, or both would think they are the only
// one running.
func TestStaleLockIsNotReclaimedOnceSomebodyElseTookIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(lockPath(dir), []byte("4242 1000"), 0o600); err != nil {
		t.Fatal(err)
	}
	SetPIDChecker(func(pid int, createMs int64) bool { return pid != 4242 })
	t.Cleanup(func() { SetPIDChecker(func(pid int, createMs int64) bool { return true }) })

	var winner *Lock
	beforeStaleRemove = func() {
		if winner != nil {
			return
		}
		// The other start gets in between the read and the remove: it
		// reclaims the stale lock and writes its own.
		if err := os.Remove(lockPath(dir)); err != nil {
			t.Error(err)
			return
		}
		l, err := Acquire(dir, 5000)
		if err != nil {
			t.Error(err)
			return
		}
		winner = l
	}
	t.Cleanup(func() { beforeStaleRemove = nil })

	l, err := Acquire(dir, 6000)
	if err == nil {
		l.Release()
		t.Fatal("the loser of a simultaneous start must be refused")
	}
	if err.Error() != "CC Babysitter is already running" {
		t.Fatalf("the loser must be told plainly: %v", err)
	}
	if winner == nil {
		t.Fatal("the other start never acquired the lock")
	}
	if !IsHeld(dir) {
		t.Fatal("the winner's lock must still be there")
	}
	winner.Release()
}

func TestLockMalformedFileIsReclaimed(t *testing.T) {
	for _, content := range []string{"garbage", "123", "abc def", ""} {
		dir := t.TempDir()
		if err := os.WriteFile(lockPath(dir), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if IsHeld(dir) {
			t.Fatalf("content %q: IsHeld should be false for a malformed lock file", content)
		}
		l, err := Acquire(dir, 4000)
		if err != nil {
			t.Fatalf("content %q: expected malformed lock file to be reclaimed, got %v", content, err)
		}
		l.Release()
	}
}

func TestHolderNamesTheLiveProcess(t *testing.T) {
	dir := t.TempDir()
	if _, _, ok := Holder(dir); ok {
		t.Fatal("a holder with no lock")
	}
	l, err := Acquire(dir, 1234)
	if err != nil {
		t.Fatal(err)
	}
	pid, createMs, ok := Holder(dir)
	if !ok || pid != os.Getpid() || createMs != 1234 {
		t.Fatalf("holder %d %d %v", pid, createMs, ok)
	}
	l.Release()
	if _, _, ok := Holder(dir); ok {
		t.Fatal("a holder after release")
	}
}
