package supervise

import (
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// upgradeWait is how long after Claude Code's daemon restarted itself on an
// updated program a babysat background session that is gone is left to it
// to start again. The daemon replaces its background sessions' processes
// with ones on the new program shortly after it starts, and a session is
// gone for a moment in between.
const upgradeWait = 2 * time.Minute

// claudeUpdate is the latest restart of Claude Code's daemon on an updated
// program that its log tells of, within retireWindow and after lastResume:
// one before the session's last rescue was already dealt with.
func (s *Supervisor) claudeUpdate(lastResume time.Time) (claude.Upgrade, bool) {
	if s.deps.DaemonLog == "" {
		return claude.Upgrade{}, false
	}
	now := s.deps.Now()
	from := now.Add(-retireWindow)
	if lastResume.After(from) {
		from = lastResume
	}
	return claude.Upgraded(s.deps.DaemonLog, from, now)
}

// leftToClaudeCode reports whether w, a babysat session that is gone, is
// left for now to Claude Code to start again: it ran in the background,
// and Claude Code's daemon restarted itself on an updated program less than
// upgradeWait ago. Resuming it then would race the daemon starting the
// same session and fork a copy. Activity says so once for each update.
func (s *Supervisor) leftToClaudeCode(w state.Watch) bool {
	if s.hostOf(w) != claude.HostBackground {
		return false
	}
	up, ok := s.claudeUpdate(w.LastResume)
	if !ok || !s.deps.Now().Before(up.At.Add(upgradeWait)) {
		return false
	}
	if !s.upgradeSaid[w.SessionID].Equal(up.At) {
		s.upgradeSaid[w.SessionID] = up.At
		s.logInfo(sessionLabel(w.Name, w.SessionID), updateWords(up)+", so it waits for Claude Code to start it again on the new version")
	}
	return true
}
