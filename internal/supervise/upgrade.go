package supervise

import (
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// upgradeWait is how long a babysat background session that went away on
// an older Claude Code than the one installed is left to Claude Code to
// start again. Once Claude Code has updated itself, it replaces a
// background session's process still on the old version with one on the
// new version, and the session is gone for a moment in between.
const upgradeWait = time.Minute

// versionNote is what the loop remembers about a babysat session's
// version: the Claude Code version its process ran when last seen, when it
// was first seen gone since, and whether Activity has said it is left to
// Claude Code for that.
type versionNote struct {
	version string
	goneAt  time.Time
	said    bool
}

// outdatedBy reports the version a babysat background session last ran and
// the one installed now, when it is gone and the two differ: Claude Code
// has updated since that process started.
func (s *Supervisor) outdatedBy(w state.Watch) (was, now string, ok bool) {
	if s.hostOf(w) != claude.HostBackground {
		return "", "", false
	}
	was, now = s.versions[w.SessionID].version, s.env.CLIVersion
	if was == "" || now == "" || was == now {
		return "", "", false
	}
	return was, now, true
}

// leftToClaudeCode reports whether w, a babysat session that is gone, is
// left for now to Claude Code to start again: it ran in the background on
// an older version than the one installed, and it has been gone for less
// than upgradeWait. Resuming it then would race Claude Code starting the
// same session on the new version, and fork a copy. Activity says so once.
func (s *Supervisor) leftToClaudeCode(w state.Watch) bool {
	was, now, ok := s.outdatedBy(w)
	if !ok {
		return false
	}
	note := s.versions[w.SessionID]
	if note.goneAt.IsZero() || !s.deps.Now().Before(note.goneAt.Add(upgradeWait)) {
		return false
	}
	if !note.said {
		note.said = true
		s.versions[w.SessionID] = note
		s.logInfo(sessionLabel(w.Name, w.SessionID), updateWords(was, now)+", so it waits a minute for Claude Code to start it again on the new version")
	}
	return true
}

// noteVersions records, for every babysat session running in snap, the
// version its process runs, the background copy's when there is one, and
// for every one that is gone, when it was first seen gone.
func (s *Supervisor) noteVersions(snap observe.Snapshot) {
	running := map[string]string{}
	for _, sn := range snap.Sessions {
		if sn.Version != "" && (running[sn.ID] == "" || sn.Host == claude.HostBackground) {
			running[sn.ID] = sn.Version
		}
	}
	for _, w := range s.st.Watches {
		note := s.versions[w.SessionID]
		if len(snap.All(w.SessionID)) > 0 {
			note = versionNote{version: running[w.SessionID]}
		} else if note.goneAt.IsZero() {
			note.goneAt = s.deps.Now()
		}
		s.versions[w.SessionID] = note
	}
	for id := range s.versions {
		if s.find(id) == nil {
			delete(s.versions, id)
		}
	}
}
