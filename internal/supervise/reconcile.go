package supervise

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// reconcile brings the world back in line with what was promised, once per
// snapshot. It runs on the goroutine that owns the state, so it is free to
// take as long as closing and resuming a session really takes.
func (s *Supervisor) reconcile(ctx context.Context, snap observe.Snapshot) {
	s.snap = snap
	s.env = s.readEnv()
	s.terminal = s.readTerminal()
	s.maybeCheckAutostart()

	// Everything already running when the program started is taken as
	// known: a session that was there before us was not started by us and
	// is not something we adopt on our own. Only a real snapshot counts as
	// that starting point; an empty one taken before the observer has ever
	// read the session files would otherwise use the first pass up and let
	// the sessions it has not seen yet be adopted.
	if !s.firstPass && !snap.At.IsZero() {
		s.firstPass = true
		for _, sn := range snap.Sessions {
			s.seen[sn.ID] = true
		}
	}

	s.countAbsence(snap)

	changed := s.settleLegacyPauses(snap)
	// One pass resumes at most one absent session. Resuming is the slowest
	// thing this program does, and a machine that has just woken up can
	// find every watch absent at once; starting them one at a time lets the
	// hosts that are merely slow come back on their own, and keeps a single
	// pass from holding the loop for minutes. The rest wait for the next
	// snapshot, which the sweep brings along within seconds.
	//
	// Nothing is acted on at all once ctx is done: a pass that runs into a
	// shutdown would otherwise start work it cannot finish and record the
	// result as a failure of the session rather than of the timing.
	resumesLeft := 1
	waiting := 0
	// The list is copied because acting on a watch can change the list, and
	// an action reads the watch back out of the live state by id.
	for _, w := range append([]state.Watch(nil), s.st.Watches...) {
		if ctx.Err() != nil {
			// Out of the loop, not out of the pass: whatever the watches
			// already dealt with changed still has to reach the disk, or a
			// resume that has just started a real background copy would be
			// forgotten the moment the process goes.
			break
		}
		switch Decide(w, snap, s.absent[w.SessionID]) {
		case Fallback:
			// For a short while after start, apps that restore their own
			// sessions go first. Absence is still counted, so a session that
			// is really gone is started the moment the grace is over.
			if s.inGrace() {
				continue
			}
			if resumesLeft == 0 {
				waiting++
				continue
			}
			touched, attempted := s.fallback(ctx, w)
			changed = touched || changed
			if attempted {
				resumesLeft--
			}
		case Dedupe:
			changed = s.dedupe(ctx, w, snap) || changed
		case Rejoin:
			changed = s.rejoin(w, snap) || changed
		}
	}
	if waiting > 0 {
		s.logInfo("", plural(waiting, "more babysat session is", "more babysat sessions are")+" waiting to be resumed")
	}

	changed = s.autoBabysit(snap) || changed
	s.sampleStats(snap)
	s.forgetHandBacks(snap)
	s.askForPast(snap)
	s.askForTrust()
	if changed {
		s.persist()
	}
}

// plural renders a count with the wording that fits it.
func plural(n int, one, many string) string {
	word := many
	if n == 1 {
		word = one
	}
	return strconv.Itoa(n) + " " + word
}

// countAbsence counts one absence per snapshot, never per pass: the same
// snapshot can reach the loop twice, and treating that as two absences
// would halve the time a host is given to come back on its own.
func (s *Supervisor) countAbsence(snap observe.Snapshot) {
	if snap.At.Equal(s.lastCounted) {
		return
	}
	s.lastCounted = snap.At
	live := map[string]bool{}
	for _, sn := range snap.Sessions {
		live[sn.ID] = true
	}
	for _, w := range s.st.Watches {
		if live[w.SessionID] {
			s.absent[w.SessionID] = 0
			continue
		}
		s.absent[w.SessionID]++
	}
	for id := range s.absent {
		if s.find(id) == nil {
			delete(s.absent, id)
		}
	}
}

// fallback resumes a babysat session as a background copy after its host
// process has gone. Nothing is resumed until the CLI itself agrees the
// session is no longer running, since starting a second copy of a live
// session is worse than waiting.
//
// changed says whether the saved state now differs and needs writing out.
// attempted says whether a resume was actually run, which is what the
// pass's one-resume budget counts: a watch that was in its backoff, had
// nothing to resume from, or could not get an answer out of the CLI never
// reached the CLI at all, so it must not use the budget up.
func (s *Supervisor) fallback(ctx context.Context, w state.Watch) (changed, attempted bool) {
	label := sessionLabel(w.Name, w.SessionID)
	if until, ok := s.backoffUntil[w.SessionID]; ok && s.deps.Now().Before(until) {
		return false, false
	}
	// Claude saves a conversation only once something has been said in it,
	// so a session closed before that has nothing to start again from, and
	// no amount of trying would change it. Its babysitting ends here rather
	// than waiting on a Try again that could never work.
	switch s.deps.transcriptSaved(w.Cwd, w.SessionID) {
	case savedNone:
		return s.endEmpty(w.SessionID, label), false
	case savedUnknown:
		// The look could not be finished, which is not the same as there
		// being nothing to resume. The watch is left as it is and looked
		// at again on the next pass.
		return false, false
	}
	// Claude's own list is the authority that must clear before anything
	// is resumed. An unanswered check never counts as a clear one, and a
	// session it lists in any form is not resumed. A background copy it
	// lists as running is taken as this watch's own: resuming beside it
	// would fork a second copy of a live session.
	list, answered := s.deps.Obs.Agents(ctx)
	if !answered {
		return false, false
	}
	if running, ok := claude.RunningCopy(list, w.SessionID, w.ShortID); ok {
		return s.adoptRunningCopy(w, running), false
	}
	if claude.Listed(list, w.SessionID) {
		return false, false
	}
	s.logInfo(label, "resuming: "+agentsSeen(list, w.SessionID, w.ShortID))
	// A shutdown between the check above and the resume below would leave a
	// resume that was cut short looking exactly like a session that refused
	// to come back. Nothing is recorded in that case, so a service that is
	// restarted often cannot count its own restarts as failures and pause
	// every watch for good.
	if ctx.Err() != nil {
		return false, false
	}

	short, res := s.deps.resumeBackground(ctx, w.SessionID, w.Name, w.Cwd, w.HasSavedOptions)
	cur := s.find(w.SessionID)
	if cur == nil {
		return false, true
	}
	now := s.deps.Now()
	// A resume that found the session running somewhere else did nothing
	// wrong: there was nothing to do. Counting it would let three of them
	// pause a watch over a session that was never in trouble.
	if res.AlreadyLiveIn != "" {
		s.notedAlreadyLive(w.SessionID, res)
		s.logAuto(label, "session found running elsewhere", res.Message)
		return true, true
	}
	// A resume that worked is recorded even when the shutdown landed while
	// it was running: a background copy really is out there now, and not
	// writing that down would leave it running with nothing tracking it.
	// A resume that failed under a shutdown is a different matter, and is
	// handled below.
	if res.OK {
		cur.ShortID = short
		cur.HasSavedOptions = true
		// A session that lives in the background to begin with is back where
		// it belongs, so it is still watched there. Any other session is now
		// carried by our copy.
		if cur.OriginHost != claude.HostBackground {
			cur.PromiseState = "fallback"
			// A copy started again after the last one died carries on the
			// same stay in the background, so the time it began is kept.
			if cur.BackgroundSince.IsZero() {
				cur.BackgroundSince = now
			}
		}
		cur.LastResume = now
		cur.Failures = nil
		delete(s.backoffUntil, w.SessionID)
		s.absent[w.SessionID] = 0
		msg := "resumed as background " + short
		if clause := remoteControlClauseOf(res.Message); clause != "" {
			msg += ". " + clause
		}
		s.logAuto(label, "host process exited", msg)
		return true, true
	}

	// A resume cut short by a shutdown is a failure of the timing, not of
	// the session. Counting it would let a service that is restarted often
	// count its own restarts as failures and pause every watch for good.
	if ctx.Err() != nil {
		return false, true
	}

	cur.Failures = append(withinWindow(cur.Failures, now), now)
	s.backoffUntil[w.SessionID] = now.Add(s.deps.Backoff)
	s.logAuto(label, "host process exited", res.Message)
	if ShouldPauseForFailures(cur.Failures, now) {
		cur.Paused = true
		cur.PauseReason = "three failed resumes in five minutes: " + res.Message
		s.logAuto(label, "three failed resumes in five minutes", "stuck until you press Try again: "+res.Message)
	}
	return true, true
}

// agentsSeen says which rows of Claude's list were about a session, by its
// id or by the short id already known for it, for the line written before
// it is resumed. Each row is its short id, kind and status, each kept to
// printable text by logField.
func agentsSeen(list []claude.AgentEntry, id, short string) string {
	var rows []string
	for _, a := range list {
		if a.SessionID != id && (short == "" || !strings.EqualFold(a.ID, short)) {
			continue
		}
		name := a.ID
		if name == "" {
			name = shortOf(a.SessionID)
		}
		var parts []string
		for _, field := range []string{name, a.Kind, a.Status} {
			if field = logField(field); field != "" {
				parts = append(parts, field)
			}
		}
		rows = append(rows, strings.Join(parts, " "))
	}
	if len(rows) == 0 {
		return "agents listed nothing for this session"
	}
	return "agents listed " + strings.Join(rows, ", ")
}

// logFieldMax is the most characters of one field read from the CLI's
// output that a log line repeats.
const logFieldMax = 64

// logField keeps a field read from the CLI's output to printable ASCII:
// whitespace and every other byte outside that range are dropped, and what
// is left is cut at logFieldMax characters, so the line it goes into stays
// one short line with nothing in it a terminal would act on.
func logField(s string) string {
	var b strings.Builder
	for _, r := range strings.Join(strings.Fields(s), "") {
		if r < 0x21 || r > 0x7e {
			continue
		}
		if b.Len() == logFieldMax {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// adoptRunningCopy takes a background copy Claude lists as running as the
// watch's own, rather than resuming the session beside it. The watch is
// told about the copy, and the activity feed hears about it, only when that
// changes what the watch knew, so a copy found the same way on every pass
// is one line rather than one per pass.
func (s *Supervisor) adoptRunningCopy(w state.Watch, row claude.AgentEntry) bool {
	cur := s.find(w.SessionID)
	if cur == nil {
		return false
	}
	short := strings.ToLower(row.ID)
	if !validShortID(short) {
		short = cur.ShortID
	}
	promise := cur.PromiseState
	if cur.OriginHost != claude.HostBackground {
		promise = "fallback"
	}
	if short == cur.ShortID && promise == cur.PromiseState && cur.HasSavedOptions && len(cur.Failures) == 0 {
		return false
	}
	res := Result{
		OK:            true,
		Message:       "the session is already running in " + hostLabel(claude.HostBackground) + ", nothing to resume",
		ShortID:       short,
		AlreadyLiveIn: claude.HostBackground,
	}
	s.notedAlreadyLive(w.SessionID, res)
	s.logAuto(sessionLabel(w.Name, w.SessionID), "session found running elsewhere", res.Message)
	return true
}

// copyTrouble is what the loop remembers about extra background copies it
// could not deal with, so the same sentence is not written on every pass.
// A copy that could not be cleaned up and a copy whose id cannot be used
// at all are different news, so each has its own memo.
//
// cleaned is the extra copies already stopped and removed, per watch, so a
// copy the next snapshot still shows, because it has not gone yet or the
// same snapshot came round again, is never asked to stop a second time.
type copyTrouble struct {
	saidDedupe         map[string]string
	saidDedupeUnusable map[string]string
	cleaned            map[string]map[string]bool
}

func newCopyTrouble() *copyTrouble {
	return &copyTrouble{
		saidDedupe:         map[string]string{},
		saidDedupeUnusable: map[string]string{},
		cleaned:            map[string]map[string]bool{},
	}
}

// forget drops everything remembered about a watch, so a watch that came
// good, or that was dropped and taken up again, starts from silence.
func (c *copyTrouble) forget(id string) {
	delete(c.saidDedupe, id)
	delete(c.saidDedupeUnusable, id)
	delete(c.cleaned, id)
}

// copyKey names one running copy: its short id and the process carrying
// it, so a copy started again later under the same short id is new.
func copyKey(sn claude.Session) string {
	return sn.ShortID + "/" + strconv.Itoa(sn.PID)
}

// handled reports whether a copy has already been stopped and removed.
func (c *copyTrouble) handled(id string, sn claude.Session) bool {
	return c.cleaned[id][copyKey(sn)]
}

// markHandled records that a copy is being stopped and removed.
func (c *copyTrouble) markHandled(id string, sn claude.Session) {
	if c.cleaned[id] == nil {
		c.cleaned[id] = map[string]bool{}
	}
	c.cleaned[id][copyKey(sn)] = true
}

// keepHandled forgets the copies that are no longer running, so the memo
// only ever holds copies still in sight.
func (c *copyTrouble) keepHandled(id string, live []claude.Session) {
	done := c.cleaned[id]
	if done == nil {
		return
	}
	still := map[string]bool{}
	for _, sn := range live {
		still[copyKey(sn)] = true
	}
	for k := range done {
		if !still[k] {
			delete(done, k)
		}
	}
	if len(done) == 0 {
		delete(c.cleaned, id)
	}
}

// sayOnce reports whether something is worth saying about a watch: the
// first time, and again whenever what it is about has changed. A copy that
// goes on refusing to stop is one sentence, not one per pass; a different
// copy refusing is news again.
func sayOnce(said map[string]string, id, about string) bool {
	if previous, seen := said[id]; seen && previous == about {
		return false
	}
	said[id] = about
	return true
}

// backgroundCopyLive reports whether a background session with the given
// session id and short id is still in the newest snapshot.
func backgroundCopyLive(snap observe.Snapshot, id, short string) bool {
	for _, sn := range snap.All(id) {
		if sn.Host == claude.HostBackground && sn.ShortID == short {
			return true
		}
	}
	return false
}

// describeID renders an id that failed its own shape check for a sentence,
// without ever putting it somewhere it could be copied into a command.
func describeID(short string) string {
	if short == "" {
		return "no id at all"
	}
	return "id " + strconv.Quote(short)
}

// sortedKeys returns a map's keys in order, so a run of log lines reads
// the same way every time.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// rejoin hands a watch back to an app. Our background copy is gone and the
// session is live in an app again, because the person reopened it there
// after stopping the copy or the app restored it on its own. Nothing is
// started or stopped: the promise simply carries on from that app.
func (s *Supervisor) rejoin(w state.Watch, snap observe.Snapshot) bool {
	cur := s.find(w.SessionID)
	if cur == nil {
		return false
	}
	for _, sn := range snap.All(w.SessionID) {
		if sn.Host == claude.HostBackground || sn.Host == claude.HostOther {
			continue
		}
		cur.PromiseState = "inplace"
		cur.BackgroundSince = time.Time{}
		cur.OriginHost = sn.Host
		// Remote Control is never recorded as switched off by an app that
		// simply does not report it: it is a promise this watch made once.
		if sn.RemoteControl {
			cur.OriginRemoteControl = true
		}
		cur.Failures = nil
		s.absent[w.SessionID] = 0
		delete(s.backoffUntil, w.SessionID)
		s.trouble.forget(w.SessionID)
		s.logAuto(sessionLabel(w.Name, w.SessionID), "session open in "+hostLabel(sn.Host)+" again",
			"babysitting it in "+hostLabel(sn.Host))
		return true
	}
	return false
}

// dedupe leaves exactly one background copy running. The copy the watch
// already knows about is the one kept; every other copy is stopped and
// removed, since an extra copy of a session is ours to clean up. Only
// background copies are ever looked at: a session open in an app beside
// them is the person's own. An app session with no job id answers to the
// first eight characters of the session id, which can be the very short id
// a copy was given, so a copy sharing a short id with an app session is
// never named in a command either. Each copy is dealt with once, however
// many passes see it before it is gone.
func (s *Supervisor) dedupe(ctx context.Context, w state.Watch, snap observe.Snapshot) bool {
	var live []claude.Session
	appShort := map[string]bool{}
	for _, sn := range snap.All(w.SessionID) {
		if sn.Host == claude.HostBackground {
			live = append(live, sn)
			continue
		}
		appShort[sn.ShortID] = true
	}
	s.trouble.keepHandled(w.SessionID, live)
	if len(live) < 2 {
		return false
	}
	keep := -1
	for i, sn := range live {
		if sn.ShortID == w.ShortID {
			keep = i
			break
		}
	}
	cur := s.find(w.SessionID)
	if cur == nil {
		return false
	}
	if keep < 0 {
		keep = 0
		cur.ShortID = live[0].ShortID
	}
	label := sessionLabel(w.Name, w.SessionID)
	reason := "more than one background copy of the same session"

	var removed, refused []string
	failures := map[string]string{}
	for i, sn := range live {
		if i == keep || s.trouble.handled(w.SessionID, sn) {
			continue
		}
		if !validShortID(sn.ShortID) || appShort[sn.ShortID] {
			refused = append(refused, sn.ShortID)
			continue
		}
		s.trouble.markHandled(w.SessionID, sn)
		stopOut, stopErr := s.deps.Runner.Run(ctx, "", hosts.StopArgs(sn.ShortID)...)
		if ctx.Err() != nil {
			// A call cut short by a shutdown says nothing about the copy.
			return false
		}
		// The removal is asked for whether or not the stop worked. An
		// extra copy left in Claude's own list is what the next pass would
		// trip over, and a stop that failed is no reason to leave it there
		// as well; if the removal fails too, the sentence below names both
		// commands to run by hand.
		rmOut, rmErr := s.deps.Runner.Run(ctx, "", hosts.RemoveArgs(sn.ShortID)...)
		if ctx.Err() != nil {
			return false
		}
		removed = append(removed, sn.ShortID)
		switch {
		case stopErr != nil:
			failures[sn.ShortID] = why(stopOut, stopErr)
		case rmErr != nil:
			failures[sn.ShortID] = why(rmOut, rmErr)
		}
	}

	if len(refused) > 0 {
		// Said once per watch: a copy this program cannot name safely stays
		// where it is, so repeating it on every pass would say the same
		// thing for as long as the copy is running. The whole set is what
		// is remembered, so two of them are one piece of news rather than a
		// memo that flips between them.
		sort.Strings(refused)
		if sayOnce(s.trouble.saidDedupeUnusable, w.SessionID, strings.Join(refused, " ")) {
			for _, short := range refused {
				s.logAuto(label, reason, "left an extra background copy with "+describeID(short)+
					" running because this program cannot use that id safely. Remove it by hand after checking `claude agents`")
			}
		}
	}
	if len(removed) == 0 {
		return false
	}
	if len(failures) > 0 {
		stuck := sortedKeys(failures)
		if sayOnce(s.trouble.saidDedupe, w.SessionID, strings.Join(stuck, " ")) {
			for _, short := range stuck {
				s.logAuto(label, reason, "could not clean up the extra background copy "+short+": "+failures[short]+
					". Remove it by hand: run `claude stop "+short+"`, then `claude rm "+short+"`")
			}
		}
	} else {
		delete(s.trouble.saidDedupe, w.SessionID)
	}
	kept := removed
	if len(failures) > 0 {
		kept = nil
		for _, short := range removed {
			if _, bad := failures[short]; !bad {
				kept = append(kept, short)
			}
		}
	}
	if len(kept) > 0 {
		s.logAuto(label, reason, "stopped and removed "+listOf(kept)+" and kept "+live[keep].ShortID)
	}
	return true
}

// autoBabysit adopts background sessions that appear on a machine with no
// display, where nobody is watching a window to notice one dying. A
// session that was already running when this program started is never
// adopted, and neither is one the user has chosen to stop babysitting.
func (s *Supervisor) autoBabysit(snap observe.Snapshot) bool {
	changed := false
	if s.firstPass && s.st.Settings.AutoBabysit && s.env.Headless {
		for _, sn := range snap.Sessions {
			if sn.Host != claude.HostBackground || s.seen[sn.ID] || s.find(sn.ID) != nil {
				continue
			}
			s.st.Watches = append(s.st.Watches, newWatch(sn, s.deps.Now()))
			s.logAuto(sessionLabel(sn.Name, sn.ID), "server auto-babysit",
				"adopted background session "+sn.ShortID+" on a machine with no display")
			changed = true
		}
	}
	for _, sn := range snap.Sessions {
		s.seen[sn.ID] = true
	}
	return changed
}

// sampleStats asks for each live session's token counts, no more often
// than statsInterval per session, and reads each process tree, and forgets
// whatever belongs to a session that is gone and not babysat.
//
// The token counts are asked for rather than read here: the transcript
// read happens on the stats worker, and its answer reaches the loop later.
// Process trees stay here, because reading one is cheap and its numbers
// are only worth anything alongside the snapshot they were taken with.
func (s *Supervisor) sampleStats(snap observe.Snapshot) {
	now := s.deps.Now()
	livePID := map[int]bool{}
	liveID := map[string]bool{}
	for _, sn := range snap.Sessions {
		liveID[sn.ID] = true
		livePID[sn.PID] = true

		due := false
		if at, ok := s.statsAt[sn.ID]; !ok || now.Sub(at) >= statsInterval {
			due = true
		}
		if due && !s.statsInFlight[sn.ID] {
			if path, found := claude.FindTranscript(s.deps.ProjectsDir, sn.Cwd, sn.ID); found {
				if s.postStats(statsRequest{id: sn.ID, path: path}) {
					s.statsAt[sn.ID] = now
					s.statsInFlight[sn.ID] = true
				}
			} else {
				// There is nothing to read yet, but the session is known:
				// remembering that keeps the search off every pass.
				s.statsAt[sn.ID] = now
			}
		}

		if at, ok := s.treesAt[sn.PID]; !ok || now.Sub(at) >= statsInterval {
			s.treesAt[sn.PID] = now
			if tree, ok := s.deps.Procs.Tree(sn.PID); ok {
				s.trees[sn.PID] = tree
			} else {
				delete(s.trees, sn.PID)
			}
		}
	}
	for id := range s.statsAt {
		if liveID[id] || s.find(id) != nil {
			continue
		}
		delete(s.statsAt, id)
		delete(s.stats, id)
		s.statsForget = append(s.statsForget, id)
	}
	s.flushStatsForget()
	for pid := range s.treesAt {
		if livePID[pid] {
			continue
		}
		delete(s.treesAt, pid)
		delete(s.trees, pid)
	}
}

// flushStatsForget tells the worker about the sessions whose readers it
// can drop, keeping whatever did not fit in the queue for a later pass so
// a busy worker never turns into a reader that is never released.
func (s *Supervisor) flushStatsForget() {
	kept := s.statsForget[:0]
	for _, id := range s.statsForget {
		if !s.postStats(statsRequest{id: id, forget: true}) {
			kept = append(kept, id)
		}
	}
	s.statsForget = kept
}

// EmptyReason is the reason given in the activity log when the babysitting
// of a session ends because nothing was ever said in it.
const EmptyReason = "no saved conversation"

// endEmpty ends the babysitting of a session that closed before anything
// was said in it, and says so. It is an automatic action, so it is written
// down with its reason like every other one.
func (s *Supervisor) endEmpty(id, label string) bool {
	if s.find(id) == nil {
		return false
	}
	s.removeWatch(id)
	delete(s.absent, id)
	delete(s.backoffUntil, id)
	s.logAuto(label, EmptyReason, label+" ended before anything was said in it, so there is nothing to bring back.")
	return true
}

// legacyEmptyPause is the reason an earlier version paused a watch with
// when its session had no saved conversation, which asked for a Try again
// that could never work.
const legacyEmptyPause = "transcript missing"

// settleLegacyPauses deals with the watches an earlier version paused that
// way. One whose session is still running nowhere with nothing saved ends
// as it would today; any other is watched again, since what paused it is no
// longer so. Only a real snapshot is judged, never one taken before the
// session files were first read.
func (s *Supervisor) settleLegacyPauses(snap observe.Snapshot) bool {
	if snap.At.IsZero() {
		return false
	}
	changed := false
	for _, w := range append([]state.Watch(nil), s.st.Watches...) {
		if !w.Paused || w.PauseReason != legacyEmptyPause {
			continue
		}
		label := sessionLabel(w.Name, w.SessionID)
		live := len(snap.All(w.SessionID)) > 0
		if !live {
			switch s.deps.transcriptSaved(w.Cwd, w.SessionID) {
			case savedNone:
				changed = s.endEmpty(w.SessionID, label) || changed
				continue
			case savedUnknown:
				// Settled on a later pass, once the look can be finished.
				continue
			}
		}
		cur := s.find(w.SessionID)
		if cur == nil {
			continue
		}
		cur.Paused = false
		cur.PauseReason = ""
		cur.Failures = nil
		s.absent[w.SessionID] = 0
		delete(s.backoffUntil, w.SessionID)
		reason := "a saved conversation is there now"
		if live {
			reason = "the session is running again"
		}
		s.logAuto(label, reason, "babysitting it again")
		changed = true
	}
	return changed
}

// newWatch promises a session stays exactly where it is right now.
// HasSavedOptions records whether the session has already been started in
// the background once, because a session that has will ignore any flag
// passed to a later resume.
func newWatch(sn claude.Session, now time.Time) state.Watch {
	return state.Watch{
		SessionID:           sn.ID,
		ShortID:             sn.ShortID,
		Name:                sn.Name,
		Cwd:                 sn.Cwd,
		OriginHost:          sn.Host,
		OriginRemoteControl: sn.RemoteControl,
		HasSavedOptions:     sn.Host == claude.HostBackground,
		WatchedSince:        now,
		PromiseState:        "inplace",
	}
}

// remoteControlClauseOf picks the sentence about Remote Control out of a
// resume result, so an automatic log entry can repeat it without having to
// work the answer out a second time.
func remoteControlClauseOf(msg string) string {
	if i := strings.Index(msg, "Remote Control"); i >= 0 {
		return msg[i:]
	}
	return ""
}

// withinWindow drops failure times that have fallen out of the window the
// pause rule counts over, so a watch that has struggled for hours does not
// carry a list that only grows.
func withinWindow(failures []time.Time, now time.Time) []time.Time {
	kept := make([]time.Time, 0, len(failures)+1)
	for _, f := range failures {
		if now.Sub(f) <= FailureWindow {
			kept = append(kept, f)
		}
	}
	return kept
}

// listOf renders short ids for a sentence, or a placeholder when there are
// none.
func listOf(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}
