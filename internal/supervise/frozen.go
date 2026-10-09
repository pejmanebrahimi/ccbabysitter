package supervise

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
)

// notRespondingCause is why a session stopped for being frozen went down,
// as its rescue line in Activity says.
const notRespondingCause = "it stopped responding"

// frozenWhy says how a frozen session looked, for Activity.
const frozenWhy = "not responding: busy for 20 minutes with no output and no CPU use."

// checkFrozen looks at each babysat session that is running and keeps what
// it has shown, once every statsInterval. A background session found
// frozen is stopped with `claude stop`, which keeps the conversation, so
// the usual rescue starts it again; after MaxFreezes within FreezeWindow
// the watch is paused instead. A frozen session in a terminal or an app is
// only marked as not responding: it belongs to that app. It reports
// whether the saved state changed.
func (s *Supervisor) checkFrozen(ctx context.Context, snap observe.Snapshot) bool {
	now := s.deps.Now()
	changed := false
	watched := map[string]bool{}
	for i := range s.st.Watches {
		w := &s.st.Watches[i]
		watched[w.SessionID] = true
		sn, ok := primarySession(*w, snap.All(w.SessionID))
		tree, sampled := s.trees[sn.PID]
		if !ok || !sampled || w.Paused {
			delete(s.liveness, w.SessionID)
			delete(s.notResponding, w.SessionID)
			continue
		}
		if sn.Status != "busy" {
			delete(s.notResponding, w.SessionID)
		}
		if at, ok := s.livenessAt[w.SessionID]; ok && now.Sub(at) < statsInterval {
			continue
		}
		s.livenessAt[w.SessionID] = now
		l := s.liveness[w.SessionID].Next(s.fingerprint(sn, tree.PIDs), tree.CPUSeconds, now)
		s.liveness[w.SessionID] = l
		if !Frozen(l, sn.Status, now) {
			delete(s.notResponding, w.SessionID)
			continue
		}
		label := sessionLabel(w.Name, w.SessionID)
		if sn.Host != claude.HostBackground {
			if !s.notResponding[w.SessionID] {
				s.notResponding[w.SessionID] = true
				s.logInfo(label, frozenWhy+" It runs in "+hostLabel(sn.Host)+", so it is left as it is.")
			}
			continue
		}
		delete(s.liveness, w.SessionID)
		freezes := append(recentFreezes(w.Freezes, now), now)
		w.Freezes = freezes
		changed = true
		if ShouldPauseForFreezes(freezes, now) {
			w.Paused = true
			w.PauseReason = "it froze three times in two hours"
			s.logInfo(label, frozenWhy+" It froze three times in two hours, so it is not started again. It is left as it is. Press Try again when you are ready.")
			continue
		}
		s.logInfo(label, frozenWhy+" Stopped it to start it again in the background.")
		// The CLI's own stop keeps the daemon's books right. Ending the
		// process is the fallback, matched by pid and creation time.
		err := errors.New("its short id is not one this program can pass on safely")
		if validShortID(sn.ShortID) {
			_, err = s.deps.Runner.Run(ctx, "", hosts.StopArgs(sn.ShortID)...)
		}
		if err != nil && !s.deps.Procs.Terminate(sn.PID, sn.ProcStart) {
			s.logError(label, "could not stop the frozen session: "+err.Error()+". Stop it by hand with `claude agents`.")
			continue
		}
		s.frozeStopped[w.SessionID] = true
	}
	for id := range s.frozeStopped {
		if !watched[id] {
			delete(s.frozeStopped, id)
		}
	}
	for id := range s.liveness {
		if !watched[id] {
			delete(s.liveness, id)
			delete(s.livenessAt, id)
			delete(s.notResponding, id)
		}
	}
	return changed
}

// fingerprint joins what can be seen of a session other than its CPU
// time: its status and when it was set, the newest write to its
// transcripts, and the processes in its tree.
func (s *Supervisor) fingerprint(sn claude.Session, pids []int) string {
	var wrote time.Time
	if path, ok := claude.FindTranscript(s.deps.ProjectsDir, sn.Cwd, sn.ID); ok {
		wrote = claude.TranscriptWrittenAt(path)
	}
	sorted := append([]int(nil), pids...)
	sort.Ints(sorted)
	parts := []string{sn.Status, strconv.FormatInt(sn.StatusUpdatedAt.UnixMilli(), 10), strconv.FormatInt(wrote.UnixNano(), 10)}
	for _, p := range sorted {
		parts = append(parts, strconv.Itoa(p))
	}
	return strings.Join(parts, "|")
}

// recentFreezes keeps the freezes that still fall within FreezeWindow.
func recentFreezes(freezes []time.Time, now time.Time) []time.Time {
	var out []time.Time
	for _, f := range freezes {
		if now.Sub(f) <= FreezeWindow {
			out = append(out, f)
		}
	}
	return out
}
