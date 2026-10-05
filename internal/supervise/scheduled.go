package supervise

// A Claude Desktop scheduled task starts a new session for each run. Such a
// run is never babysat: Desktop starts the task again on its schedule, and
// a copy brought back in the background could repeat the task's work, or
// run alongside the next scheduled run. Whether a session is one is known
// once its transcript has been read.

// ScheduledRunReason is why a run is not babysat, for the answer to a
// babysit, for Activity and for the demonstration engine.
const ScheduledRunReason = "Claude Desktop starts a scheduled task again on its schedule, so its runs are not babysat."

// isScheduledRun reports whether the session's transcript says it is a
// scheduled task's run. A session whose transcript has not been read yet
// is not one, so far.
func (s *Supervisor) isScheduledRun(id string) bool {
	return s.stats[id].ScheduledTask
}

// letGoScheduledRun stops babysitting a session found to be a scheduled
// task's run, which was babysat before that was known, and says why in
// Activity. Nothing is closed or started. It reports whether a watch went.
func (s *Supervisor) letGoScheduledRun(id string) bool {
	for i, w := range s.st.Watches {
		if w.SessionID != id {
			continue
		}
		s.st.Watches = append(s.st.Watches[:i], s.st.Watches[i+1:]...)
		s.persist()
		s.logAuto(sessionLabel(w.Name, w.SessionID), "scheduled task run",
			"no longer babysitting. "+ScheduledRunReason)
		return true
	}
	return false
}
