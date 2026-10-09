// Package supervise keeps the promise made about a babysat session: the
// computer stays awake, and if the session's app goes away the session
// carries on as a background copy with Remote Control. This file is the
// pure reconcile policy; the rest of the package acts on it.
package supervise

import (
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// Decision is what a reconcile pass should do about one watch.
type Decision int

const (
	None Decision = iota
	Fallback
	Dedupe
	Rejoin
)

const (
	// AbsentSweepsRequired is how many consecutive sweeps must find a watch
	// with no live session before a fallback resume is attempted.
	AbsentSweepsRequired = 2
	// MaxFailures is how many resume failures within FailureWindow pause a
	// watch rather than trying again.
	MaxFailures = 3
	// FailureWindow is the sliding window failures are counted over.
	FailureWindow = 5 * time.Minute
)

// Decide is the whole reconcile policy for one watch against one snapshot.
// A paused watch is never acted on. A watch with no live session at all
// falls back to a background copy once it has been absent for
// AbsentSweepsRequired sweeps in a row, and stays put otherwise. A watch
// promised in place is left alone regardless of how many apps currently
// show it: the user's own apps are the user's business.
//
// A watch served by our own background copy is left alone too, even when
// an app shows the session beside it: a copy is never stopped to make room.
// Two things are done about such a watch. When the copy is gone and an app
// shows the session, that is Rejoin: the person reopened it, or the app
// restored it, and the watch goes back to watching it there. When there is
// more than one background copy, that is Dedupe: the watch's own short id
// is kept and the rest are stopped and removed.
func Decide(w state.Watch, snap observe.Snapshot, absentCount int) Decision {
	if w.Paused {
		return None
	}
	present := snap.All(w.SessionID)
	if len(present) == 0 {
		if absentCount >= AbsentSweepsRequired {
			return Fallback
		}
		return None
	}
	if w.PromiseState != "fallback" {
		return None
	}
	background := 0
	for _, s := range present {
		if s.Host == claude.HostBackground {
			background++
		}
	}
	if background == 0 {
		return Rejoin
	}
	if background > 1 {
		return Dedupe
	}
	return None
}

// ShouldPauseForFailures reports whether MaxFailures or more of the given
// failure times fall within FailureWindow of now.
func ShouldPauseForFailures(failures []time.Time, now time.Time) bool {
	n := 0
	for _, f := range failures {
		if now.Sub(f) <= FailureWindow {
			n++
		}
	}
	return n >= MaxFailures
}

const (
	// FrozenAfter is how long a busy session waiting on the model must show
	// no change before it counts as frozen. A reply being written uses CPU
	// all along, and every other wait, a tool running, a retry after an
	// API error, a task carried on after the turn, is not waiting on the
	// model.
	FrozenAfter = 20 * time.Minute
	// FrozenCPUSlack is how many seconds of CPU a session's process tree
	// may use over FrozenAfter and still count as quiet. An idle Claude
	// Code process was measured at 3 to 20 seconds over 20 minutes for its
	// timers, and one receiving a reply at over 120.
	FrozenCPUSlack = 30.0
	// MaxLookGap is the longest time between two looks at a session that
	// still counts the quiet time on. A longer gap means the computer
	// slept, or the program was held up, and nothing could be seen then.
	MaxLookGap = time.Minute
	// MaxFreezes is how many freezes within FreezeWindow pause a watch
	// rather than start it again once more.
	MaxFreezes = 3
	// FreezeWindow is the sliding window freezes are counted over.
	FreezeWindow = 2 * time.Hour
)

// Liveness is what a session has shown since its signals last changed:
// Fingerprint joins what can be seen of it (its status, the newest write
// to its transcripts, the processes in its tree), CPU is its tree's CPU
// time in seconds then, Since is when that was, and Looked is when it was
// last looked at.
type Liveness struct {
	Fingerprint string
	CPU         float64
	Since       time.Time
	Looked      time.Time
}

// Next folds one look at the session into l. A new fingerprint, CPU used
// beyond FrozenCPUSlack, less CPU than before, which means a process in
// the tree ended, or a gap since the last look longer than MaxLookGap by
// the wall clock, which also counts a sleep, starts the quiet time again
// from now.
func (l Liveness) Next(fingerprint string, cpu float64, now time.Time) Liveness {
	gap := now.Round(0).Sub(l.Looked.Round(0))
	if l.Since.IsZero() || gap > MaxLookGap || fingerprint != l.Fingerprint || cpu-l.CPU >= FrozenCPUSlack || cpu < l.CPU {
		return Liveness{Fingerprint: fingerprint, CPU: cpu, Since: now, Looked: now}
	}
	l.Looked = now
	return l
}

// Frozen reports whether a session with status, seen as l, is frozen: busy
// in Claude Code's own terms, which a session waiting on a question never
// is, waiting on the model, which a session running a tool, waiting to
// retry or past its turn never is, and quiet for FrozenAfter.
func Frozen(l Liveness, status string, awaitingModel bool, now time.Time) bool {
	return status == "busy" && awaitingModel && !l.Since.IsZero() && now.Sub(l.Since) >= FrozenAfter
}

// ShouldPauseForFreezes reports whether MaxFreezes or more of the given
// freeze times fall within FreezeWindow of now.
func ShouldPauseForFreezes(freezes []time.Time, now time.Time) bool {
	n := 0
	for _, f := range freezes {
		if now.Sub(f) <= FreezeWindow {
			n++
		}
	}
	return n >= MaxFreezes
}
