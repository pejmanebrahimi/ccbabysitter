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

// frozenStopWait is how long a frozen copy is given to go after `claude
// stop` answered.
var frozenStopWait = 5 * time.Second

// frozenCopy is the frozen background copy a watch was paused over: its
// process and what it showed, so Try again stops it only while it is
// still that copy and still frozen.
type frozenCopy struct {
	pid         int
	procStart   string
	fingerprint string
	cpu         float64
}

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
			s.frozenPaused[id] = frozenCopy{pid: sn.PID, procStart: sn.ProcStart, fingerprint: fp, cpu: tree.CPUSeconds}
			s.logAuto(label, frozePause, "stuck until you press Try again. The frozen copy is left as it is.")
			continue
		}
		err := s.stopFrozen(ctx, sn, live)
		switch {
		case ctx.Err() != nil:
			return changed
		case errors.Is(err, errAlreadyGone):
			// It went by itself: the usual rescue deals with it.
			continue
		case err != nil:
			s.logError(label, "could not stop it after it stopped responding: "+err.Error()+". "+stopByHand(sn, err))
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
	for id := range s.frozenPaused {
		if !watched[id] {
			delete(s.frozenPaused, id)
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
// session of the same conversation, or is not one a command can carry, so
// it is never named in one.
var errShortIDShared = errors.New("its short id is not one this program can name safely")

// errAlreadyGone says the copy was gone before it was stopped.
var errAlreadyGone = errors.New("it was already gone")

// stopFrozen stops the frozen background copy sn, one of live, with
// `claude stop`, which keeps the conversation: sessions are controlled only
// through the claude CLI. It then waits up to frozenStopWait for the
// process, matched by pid and creation time, to go. A short id that is not
// valid, or that an app session of the same conversation also answers to,
// is never named in a command. A copy already gone is errAlreadyGone.
func (s *Supervisor) stopFrozen(ctx context.Context, sn claude.Session, live []claude.Session) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !s.deps.Procs.Alive(sn.PID, sn.ProcStart) {
		return errAlreadyGone
	}
	for _, x := range live {
		if x.Host != claude.HostBackground && x.ShortID == sn.ShortID {
			return errShortIDShared
		}
	}
	if !validShortID(sn.ShortID) {
		return errShortIDShared
	}
	out, err := s.deps.Runner.Run(ctx, "", hosts.StopArgs(sn.ShortID)...)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return errors.New(why(out, err))
	}
	deadline := time.Now().Add(frozenStopWait)
	for s.deps.Procs.Alive(sn.PID, sn.ProcStart) {
		if time.Now().After(deadline) {
			return errors.New("`claude stop` answered, but it is still running")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil
}

// stopByHand says how to stop a frozen copy by hand after stopFrozen
// failed with err.
func stopByHand(sn claude.Session, err error) string {
	if errors.Is(err, errShortIDShared) {
		return "Find it with `claude agents` and stop it by hand with `claude stop` and its id."
	}
	return "Stop it by hand with `claude stop " + sn.ShortID + "`."
}

// stillFrozen reports whether sn is the copy a watch was paused over and
// still shows nothing new: the same process, busy waiting on the model,
// with the same fingerprint and no CPU used beyond the allowance.
func (s *Supervisor) stillFrozen(cp frozenCopy, sn claude.Session) bool {
	tree, ok := s.trees[sn.PID]
	return ok && !tree.CPUUnknown && sn.PID == cp.pid && sn.ProcStart == cp.procStart &&
		sn.Status == "busy" && s.stats[sn.ID].AwaitingModel &&
		s.fingerprint(sn, tree.PIDs) == cp.fingerprint && tree.CPUSeconds-cp.cpu < FrozenCPUSlack
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
