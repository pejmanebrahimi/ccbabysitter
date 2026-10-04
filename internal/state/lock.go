package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Lock represents this process holding the single-instance lock.
type Lock struct{ path string }

func lockPath(dir string) string { return filepath.Join(dir, "ccbabysitter.lock") }

// pidAlive is replaced at wiring time with a real process check, so this
// package stays free of any process-inspection dependency.
var pidAlive = func(pid int, createMs int64) bool { return true }

// beforeStaleRemove runs between reading a stale lock file and removing
// it. It is nil in a running program and exists only so a test can make
// another start win that gap, which is the one moment where reclaiming a
// stale lock could otherwise delete a lock somebody else has just taken.
var beforeStaleRemove func()

// Acquire creates the lock file exclusively, recording the caller's pid and
// process creation time (Unix milliseconds). A lock whose recorded process is
// no longer alive is treated as stale and reclaimed.
func Acquire(dir string, createMs int64) (*Lock, error) {
	if err := EnsureDir(dir); err != nil {
		return nil, err
	}
	p := lockPath(dir)
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
		if err == nil {
			_, _ = f.WriteString(fmt.Sprintf("%d %d", os.Getpid(), createMs))
			_ = f.Close()
			return &Lock{path: p}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		stale, held := readLock(p)
		if held {
			return nil, errors.New("CC Babysitter is already running")
		}
		if !removeIfUnchanged(p, stale) {
			// Between reading the stale lock and removing it, somebody else
			// replaced it with their own. Theirs is the one that counts.
			return nil, errors.New("CC Babysitter is already running")
		}
	}
	return nil, errors.New("could not acquire lock")
}

// removeIfUnchanged deletes the lock file only while it still holds
// exactly the stale text that was read a moment ago. Two copies of this
// program starting at the same instant can both find the same stale lock;
// re-reading here is what stops the slower one from deleting the lock the
// faster one has already written in its place.
func removeIfUnchanged(path string, stale []byte) bool {
	if beforeStaleRemove != nil {
		beforeStaleRemove()
	}
	now, err := os.ReadFile(path)
	if err != nil {
		// It is already gone, which is the outcome that was wanted.
		return errors.Is(err, os.ErrNotExist)
	}
	if string(now) != string(stale) {
		return false
	}
	return os.Remove(path) == nil || !fileExists(path)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// readLock returns the raw contents of the lock file and whether the
// process it names is still alive. A file that cannot be read, or that
// does not parse as "<pid> <createMs>", is not held by anyone.
func readLock(path string) (data []byte, held bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		return data, false
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return data, false
	}
	createMs, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return data, false
	}
	return data, pidAlive(pid, createMs)
}

// Holder names the process that holds dir's lock, by its pid and creation
// time, and ok is false when the lock is not held by a live process.
func Holder(dir string) (pid int, createMs int64, ok bool) {
	data, held := readLock(lockPath(dir))
	if !held {
		return 0, 0, false
	}
	fields := strings.Fields(string(data))
	pid, _ = strconv.Atoi(fields[0])
	createMs, _ = strconv.ParseInt(fields[1], 10, 64)
	return pid, createMs, true
}

// IsHeld reports whether a lock file exists and its recorded process is
// still alive. A file that does not parse as "<pid> <createMs>" is treated
// as stale, not held.
func IsHeld(dir string) bool {
	_, held := readLock(lockPath(dir))
	return held
}

// Release removes the lock file.
func (l *Lock) Release() { _ = os.Remove(l.path) }

// SetPIDChecker wires the real process-and-creation-time check used by IsHeld.
func SetPIDChecker(f func(pid int, createMs int64) bool) { pidAlive = f }
