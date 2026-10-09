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

// frozePause is why a watch is paused after MaxFreezes freezes.
const frozePause = "it froze three times in two hours"

// frozeCauseFor is how long after a frozen session was stopped its going
// down is still put down to the freeze.
const frozeCauseFor = 10 * time.Minute

// notRespondingWhy says how a frozen session looked, for Activity.
func notRespondingWhy() string {
	return "not responding: busy for " + strconv.Itoa(int(FrozenAfter/time.Minute)) +
		" minutes waiting on the model, with no output and no CPU use"
}

// checkFrozen looks at each babysat session that is running and keeps what
// it has shown, once every statsInterval. A background session found
// frozen is stopped, which keeps the conversation, so the usual rescue
// starts it again; after MaxFreezes within FreezeWindow the watch is paused
// instead. A frozen session in a terminal or an app is only marked as not
// responding: it belongs to that app. Nothing is done once ctx is done. It
// reports whether the saved state changed.
func (s *Supervisor) checkFrozen(ctx context.Context, snap observe.Snapshot) bool {
	if ctx.Err() != nil {
		return false
	}
	now := s.deps.Now()
	changed := false
	watched := map[string]bool{}
	for i := range s.st.Watches {
		w := &s.st.Watches[i]
		id := w.SessionID
		watched[id] = true
		live := snap.All(id)
		sn, ok := primarySession(*w, live)
		tree, sampled := s.trees[sn.PID]
		if !ok || !sampled || w.Paused {
			s.forgetLiveness(id)
			continue
		}
		if sn.Status != "busy" {
			delete(s.notResponding, id)
		}
		if at, ok := s.livenessAt[id]; ok && now.Sub(at) < statsInterval {
			continue
		}
		s.livenessAt[id] = now
		fp := s.fingerprint(sn, tree.PIDs)
		l := s.liveness[id].Next(fp, tree.CPUSeconds, now)
		if tree.CPUUnknown {
			// Without the tree's whole CPU time there is no telling it is
			// quiet, so the wait starts again.
			l = Liveness{}.Next(fp, tree.CPUSeconds, now)
		}
		s.liveness[id] = l
		if !Frozen(l, sn.Status, s.stats[id].AwaitingModel, now) {
			delete(s.notResponding, id)
			continue
		}
		label := sessionLabel(w.Name, id)
		if sn.Host != claude.HostBackground {
			if !s.notResponding[id] {
				s.notResponding[id] = true
				s.logInfo(label, notRespondingWhy()+". It runs in "+hostLabel(sn.Host)+", so it is left as it is.")
			}
			continue
		}
		delete(s.liveness, id)
		freezes := append(recentFreezes(w.Freezes, now), now)
		if ShouldPauseForFreezes(freezes, now) {
			w.Freezes = freezes
			w.Paused = true
			w.PauseReason = frozePause
			changed = true
			s.logAuto(label, frozePause, "stuck until you press Try again. The frozen copy is left as it is.")
			continue
		}
		if err := s.stopFrozen(ctx, sn, live); err != nil {
			if ctx.Err() != nil {
				return changed
			}
			s.logError(label, "could not stop it after it stopped responding: "+err.Error()+". Stop it by hand with `claude stop "+sn.ShortID+"`.")
			continue
		}
		w.Freezes = freezes
		changed = true
		s.frozeStopped[id] = now
		s.logAuto(label, notRespondingWhy(), "stopped it to start it again in the background")
	}
	for id := range s.livenessAt {
		if !watched[id] {
			s.forgetLiveness(id)
		}
	}
	for id, at := range s.frozeStopped {
		if !watched[id] || now.Sub(at) > frozeCauseFor {
			delete(s.frozeStopped, id)
		}
	}
	return changed
}

// forgetLiveness drops what was seen of a session's liveness.
func (s *Supervisor) forgetLiveness(id string) {
	delete(s.liveness, id)
	delete(s.livenessAt, id)
	delete(s.notResponding, id)
}

// errShortIDShared says a background copy's short id also names an app
// session of the same conversation, so a command naming it could reach
// that session.
var errShortIDShared = errors.New("its short id is not one this program can name safely")

// stopFrozen stops the frozen background copy sn, one of live: with
// `claude stop`, which keeps the conversation and the CLI's own books
// right, and by ending the process, matched by pid and creation time, when
// that did not work or could not be used. A short id that is not valid,
// or that an app session of the same conversation also answers to, is
// never named in a command. A copy already gone needs nothing.
func (s *Supervisor) stopFrozen(ctx context.Context, sn claude.Session, live []claude.Session) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !s.deps.Procs.Alive(sn.PID, sn.ProcStart) {
		return nil
	}
	stopErr := errShortIDShared
	shared := false
	for _, x := range live {
		if x.Host != claude.HostBackground && x.ShortID == sn.ShortID {
			shared = true
		}
	}
	if validShortID(sn.ShortID) && !shared {
		out, err := s.deps.Runner.Run(ctx, "", hosts.StopArgs(sn.ShortID)...)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		stopErr = nil
		if err != nil {
			stopErr = errors.New(why(out, err))
		}
	}
	if !s.deps.Procs.Alive(sn.PID, sn.ProcStart) || s.deps.Procs.Terminate(sn.PID, sn.ProcStart) {
		return nil
	}
	if stopErr == nil {
		stopErr = errors.New("it is still running")
	}
	return stopErr
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
