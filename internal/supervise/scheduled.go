package supervise

import "ccbabysitter.dev/ccbabysitter/internal/claude"

// A Claude Desktop scheduled task starts a new session for each run. Such a
// run is never babysat: Desktop starts the task again on its schedule, and
// a copy brought back in the background could repeat the task's work, or
// run alongside the next scheduled run. Whether a session is one is in its
// transcript's first prompt.

// ScheduledRunReason is why a run is not babysat, for the answer to a
// babysit and for the demonstration engine.
const ScheduledRunReason = "Claude Desktop starts a scheduled task again on its schedule, so its runs are not babysat."

// isScheduledRun reports whether the session in cwd is a scheduled task's
// run: known from its transcript once that was read for its counts, and
// before that from the start of the transcript, read here. A transcript
// that is not there yet, or cannot be read, says it is not one.
func (s *Supervisor) isScheduledRun(id, cwd string) bool {
	if s.stats[id].ScheduledTask {
		return true
	}
	path, found := claude.FindTranscript(s.deps.ProjectsDir, cwd, id)
	if !found {
		return false
	}
	run, _ := claude.ScheduledRun(path)
	return run
}
