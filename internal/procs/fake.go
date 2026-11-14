package procs

import (
	"fmt"
	"sync"
)

// Fake is an in-memory Procs for tests that exercise code depending on
// process identity without touching real operating system processes.
type Fake struct {
	mu       sync.Mutex
	alive    map[int]bool
	stats    map[int]TreeStats
	created  map[int]int64
	parents  map[int]int
	calls    []string
	refuse   bool
	attached map[string]bool
}

// NewFake returns an empty Fake ready to use.
func NewFake() *Fake {
	return &Fake{
		alive:   make(map[int]bool),
		stats:   make(map[int]TreeStats),
		created: make(map[int]int64),
		parents: make(map[int]int),
	}
}

// SetAttached marks a terminal as attached to the background session
// short, or not.
func (f *Fake) SetAttached(short string, attached bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.attached == nil {
		f.attached = map[string]bool{}
	}
	f.attached[short] = attached
}

// Attached reports what SetAttached said for short.
func (f *Fake) Attached(short string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attached[short]
}

// SetAlive marks pid as running or not.
func (f *Fake) SetAlive(pid int, alive bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alive[pid] = alive
}

// IsAlive reports the value last recorded for pid, by SetAlive or by a
// successful Terminate.
func (f *Fake) IsAlive(pid int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive[pid]
}

// SetStats records the TreeStats that Tree should return for pid.
func (f *Fake) SetStats(pid int, s TreeStats) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats[pid] = s
}

// SetCreateTime records the creation time that CreateTime should return for
// pid.
func (f *Fake) SetCreateTime(pid int, createMs int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created[pid] = createMs
}

// SetParent records the parent process id that Parent should return for
// pid.
func (f *Fake) SetParent(pid, ppid int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.parents[pid] = ppid
}

// Parent returns the parent last recorded for pid by SetParent, or 0, false
// when none was set.
func (f *Fake) Parent(pid int) (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.parents[pid]
	return v, ok
}

// SetRefuse makes Terminate report failure and leave every pid untouched,
// simulating a process that will not die.
func (f *Fake) SetRefuse(refuse bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refuse = refuse
}

// CallList returns every call recorded so far, in the order they happened.
func (f *Fake) CallList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// Alive reports the value last recorded for pid by SetAlive. procStart is
// ignored: tests decide liveness directly.
func (f *Fake) Alive(pid int, _ string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive[pid]
}

// Terminate records the call and, unless SetRefuse(true) was called,
// removes pid from the alive set and reports success.
func (f *Fake) Terminate(pid int, _ string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("terminate %d", pid))
	if f.refuse {
		return false
	}
	delete(f.alive, pid)
	return true
}

// Tree returns the TreeStats last recorded for pid by SetStats.
func (f *Fake) Tree(pid int) (TreeStats, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.stats[pid]
	return s, ok
}

// CreateTime returns the creation time last recorded for pid by
// SetCreateTime, or 0, false when none was set.
func (f *Fake) CreateTime(pid int) (int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.created[pid]
	return v, ok
}

// Exists reports whether pid is alive. When SetCreateTime was called for
// pid, it also requires createMs to equal the stored value, so tests can
// simulate a pid that is alive but under a different process. Without a
// stored creation time it behaves exactly like IsAlive.
func (f *Fake) Exists(pid int, createMs int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	alive := f.alive[pid]
	if stored, ok := f.created[pid]; ok {
		return alive && stored == createMs
	}
	return alive
}
