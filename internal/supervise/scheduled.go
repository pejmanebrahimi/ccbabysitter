package supervise

import (
	"context"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// A Claude Desktop scheduled task starts a new session for each run. Such a
// run is never babysat: Desktop starts the task again on its schedule, and
// a copy brought back in the background could repeat the task's work, or
// run alongside the next scheduled run. Whether a session is one is in its
// transcript's first prompt, which is read, a little and once, wherever it
// is decided: before a babysit, before an adoption on a server, and before
// a babysat session is brought back.

// ScheduledRunReason is why a run is not babysat, for the answer to a
// babysit, for Activity and for the demonstration engine.
const ScheduledRunReason = "Claude Desktop starts a scheduled task again on its schedule, so its runs are not babysat."

// scheduledRunTag is the reason Activity gives for letting a run go.
const scheduledRunTag = "scheduled task run"

// isScheduledRun reports whether the session is a scheduled task's run, as
// far as is known: from its transcript read for its counts, or from an
// earlier checkRun. It reads nothing.
func (s *Supervisor) isScheduledRun(id string) bool {
	return s.stats[id].ScheduledTask || s.runs[id]
}

// checkRun reports whether the session in cwd is a scheduled task's run,
// reading the start of its transcript the first time it is asked, and
// remembering the answer: a first prompt never changes. A transcript that
// is not there yet says nothing, and is looked for again next time.
func (s *Supervisor) checkRun(id, cwd string) bool {
	if s.isScheduledRun(id) {
		return true
	}
	if _, known := s.runs[id]; known {
		return false
	}
	path, found := claude.FindTranscript(s.deps.ProjectsDir, cwd, id)
	if !found {
		return false
	}
	run, ok := claude.ScheduledRun(path)
	if ok {
		s.runs[id] = run
	}
	return run
}

// settleScheduledRuns lets go of every babysat session found to be a
// scheduled task's run, which was babysat before that was known, and says
// why in Activity. A run our own background copy already carries has that
// copy stopped too, matched by its id, as a stop from the page does. It
// reports whether anything changed.
func (s *Supervisor) settleScheduledRuns(ctx context.Context, snap observe.Snapshot) bool {
	changed := false
	for _, w := range append([]state.Watch(nil), s.st.Watches...) {
		if !s.isScheduledRun(w.SessionID) {
			continue
		}
		id := w.SessionID
		label := sessionLabel(w.Name, id)
		live := snap.All(id)
		s.removeWatch(id)
		delete(s.absent, id)
		delete(s.backoffUntil, id)
		s.persist()
		changed = true
		bg, ours := copyToStop(live, w.ShortID)
		if _, running := backgroundCopy(live); !running || w.PromiseState != "fallback" || !ours || !validShortID(bg.ShortID) {
			s.logAuto(label, scheduledRunTag, "no longer babysitting. "+ScheduledRunReason)
			continue
		}
		out, err := s.deps.Runner.Run(ctx, "", hosts.StopArgs(bg.ShortID)...)
		if err != nil {
			s.logAuto(label, scheduledRunTag, "no longer babysitting. "+ScheduledRunReason+
				" Its background copy "+bg.ShortID+" could not be stopped: "+why(out, err)+". Stop it by hand with `claude stop "+bg.ShortID+"`.")
			continue
		}
		s.stopped[id] = stoppedCopy{short: bg.ShortID, at: s.deps.Now(), pid: bg.PID}
		s.logAuto(label, scheduledRunTag, "no longer babysitting, and stopped its background copy "+bg.ShortID+". "+ScheduledRunReason)
	}
	return changed
}
