package supervise

import (
	"context"
	"regexp"
	"strconv"
	"strings"
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

// versionAskTimeout is how long the CLI is given to say its version when
// the one kept may be out of date.
const versionAskTimeout = 5 * time.Second

// versionNote is what the loop remembers about a babysat session's
// version: the Claude Code version its process ran when last seen, when it
// was first seen gone since, the version the CLI gave when asked while it
// was gone, whether it was asked, and whether Activity has said the
// session is left to Claude Code.
type versionNote struct {
	version   string
	goneAt    time.Time
	installed string
	asked     bool
	said      bool
}

// cliVersion matches a version in what claude --version prints.
var cliVersion = regexp.MustCompile(`(\d+\.\d+\.\d+)`)

// installedFor is the Claude Code version installed now, as far as w's
// question needs it. The version kept from the last look at the
// environment can be a minute old, and Claude Code can replace a process
// sooner than that after updating, so when it does not already say the
// session's version is out of date, the CLI is asked, once while the
// session is gone.
func (s *Supervisor) installedFor(ctx context.Context, w state.Watch) string {
	note := s.versions[w.SessionID]
	if olderVersion(note.version, s.env.CLIVersion) || note.version == "" {
		return s.env.CLIVersion
	}
	if !note.asked && s.deps.Runner != nil {
		note.asked = true
		actx, cancel := context.WithTimeout(ctx, versionAskTimeout)
		out, err := s.deps.Runner.Run(actx, "", "--version")
		cancel()
		if m := cliVersion.FindStringSubmatch(out); err == nil && m != nil {
			note.installed = m[1]
		}
		s.versions[w.SessionID] = note
	}
	if note.installed != "" {
		return note.installed
	}
	return s.env.CLIVersion
}

// outdatedBy reports the version a gone babysat background session last
// ran and the newer one installed, when Claude Code has updated since its
// process started and the session went away within retireWindow. A
// session CC Babysitter stopped itself for freezing, or one that went down
// with the computer, has that as its cause instead.
func (s *Supervisor) outdatedBy(w state.Watch, installed string) (was, now string, ok bool) {
	if s.hostOf(w) != claude.HostBackground || specificCause(s.causes[w.SessionID]) {
		return "", "", false
	}
	note := s.versions[w.SessionID]
	if note.goneAt.IsZero() || s.deps.Now().Sub(note.goneAt) > retireWindow {
		return "", "", false
	}
	if !olderVersion(note.version, installed) {
		return "", "", false
	}
	return note.version, installed, true
}

// specificCause reports whether a session's cause says more than that its
// process went away: CC Babysitter stopped it for freezing, or the
// computer restarted.
func specificCause(cause string) bool {
	return cause == notRespondingCause || cause == computerRestarted
}

// leftToClaudeCode reports whether w, a babysat session that is gone, is
// left for now to Claude Code to start again: it ran in the background on
// an older version than the one installed, and it has been gone for less
// than upgradeWait. Resuming it then would race Claude Code starting the
// same session on the new version, and fork a copy. Activity says so once.
func (s *Supervisor) leftToClaudeCode(ctx context.Context, w state.Watch) bool {
	if s.hostOf(w) != claude.HostBackground || specificCause(s.causes[w.SessionID]) {
		return false
	}
	was, now, ok := s.outdatedBy(w, s.installedFor(ctx, w))
	if !ok {
		return false
	}
	note := s.versions[w.SessionID]
	if !s.deps.Now().Before(note.goneAt.Add(upgradeWait)) {
		return false
	}
	if !note.said {
		note.said = true
		s.versions[w.SessionID] = note
		s.logInfo(sessionLabel(w.Name, w.SessionID), updateWords(was, now)+", so it waits up to "+idleWords(upgradeWait)+" for Claude Code to start it again on the new version")
	}
	return true
}

// installedKnown is the installed version as far as it is known without
// asking: what the CLI said while the session was gone, else the version
// kept from the last look at the environment.
func (s *Supervisor) installedKnown(id string) string {
	if v := s.versions[id].installed; v != "" {
		return v
	}
	return s.env.CLIVersion
}

// olderVersion reports whether version a comes before version b, both
// dotted numbers such as 2.1.296. Anything else is never older.
func olderVersion(a, b string) bool {
	pa, oka := versionParts(a)
	pb, okb := versionParts(b)
	if !oka || !okb {
		return false
	}
	for i := 0; i < len(pa) || i < len(pb); i++ {
		x, y := 0, 0
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// versionParts splits a dotted version into its numbers, and is false for
// anything that is not one.
func versionParts(v string) ([]int, bool) {
	if v == "" {
		return nil, false
	}
	var parts []int
	for _, p := range strings.Split(v, ".") {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		parts = append(parts, n)
	}
	return parts, true
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
