package supervise

import (
	"context"
	"strings"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// noCLI is the refusal for anything that would have to run the CLI when
// there is no CLI to run. It is decided on the binary being absent, from
// PATH and from the folders its installers use, never on a CLI that is
// merely slow to answer: refusing while one is starting up would be
// wrong, and a program that is not installed cannot be started at all.
const noCLI = "Claude Code CLI not found."

// Babysit starts watching a live session where it already is. With
// startAtLogin it also switches start at login on, exactly as the Settings
// switch does, so the promise survives a restart of the computer.
func (s *Supervisor) Babysit(id string, startAtLogin bool, via Via) Result {
	return s.ask(func(ctx context.Context) Result { return s.babysit(ctx, id, startAtLogin, via) })
}

func (s *Supervisor) babysit(_ context.Context, id string, startAtLogin bool, via Via) Result {
	sn, ok := s.liveSession(id)
	if !ok {
		return Result{Message: "This session is not running, so it cannot be babysat."}
	}
	if sn.Host == claude.HostOther {
		return Result{Message: "This session belongs to another program and cannot be babysat."}
	}
	label := sessionLabel(sn.Name, sn.ID)
	if s.isScheduledRun(id, sn.Cwd) {
		return Result{Message: label + " is a scheduled task run. " + ScheduledRunReason}
	}
	if s.find(id) != nil {
		return Result{OK: true, Message: label + " is already being babysat."}
	}
	s.st.Watches = append(s.st.Watches, newWatch(sn, s.deps.Now()))
	s.seen[id] = true
	s.persist()

	msg := "Babysitting " + label + " in " + hostLabel(sn.Host) + ". " + remoteControlSentence(sn)
	if warning := s.fallbackWarning(sn.Cwd, sn.Host); warning != "" {
		msg += " " + warning
	}
	if startAtLogin && !s.st.Settings.Autostart {
		msg += " " + s.switchOnStartAtLogin(via)
	}
	s.logInfo(label, "babysitting in "+hostLabel(sn.Host)+via.Suffix())
	return Result{OK: true, Message: msg, ShortID: sn.ShortID}
}

// Unbabysit stops watching a session. Nothing is closed or started: the
// session keeps running wherever it is. When it is allowed is CanUnbabysit's
// answer.
func (s *Supervisor) Unbabysit(id string, via Via) Result {
	return s.ask(func(ctx context.Context) Result { return s.unbabysit(ctx, id, via) })
}

func (s *Supervisor) unbabysit(_ context.Context, id string, via Via) Result {
	w := s.find(id)
	if w == nil {
		return Result{Message: "That session is not being babysat."}
	}
	label := sessionLabel(w.Name, w.SessionID)
	s.refreshSnap()
	if st := StateOf(*w, s.snap.All(id)); !CanUnbabysit(st, s.env.Headless) {
		if st == StateStarting {
			return Result{Message: label + " is being started in the background. Wait for it, then stop the background copy."}
		}
		return Result{Message: label + " is running in the background. Stop the background copy instead."}
	}
	s.removeWatch(id)
	s.persist()
	delete(s.absent, id)
	delete(s.backoffUntil, id)
	s.logInfo(label, "no longer babysitting"+via.Suffix())
	return Result{OK: true, Message: "Stopped babysitting " + label + ". " + whereItIs(s.snap.All(id))}
}

// ResumeWatch clears a pause and lets the watch act again.
func (s *Supervisor) ResumeWatch(id string, via Via) Result {
	return s.ask(func(context.Context) Result {
		w := s.find(id)
		if w == nil {
			return Result{Message: "That session is not being babysat."}
		}
		w.Paused = false
		w.PauseReason = ""
		w.Failures = nil
		delete(s.backoffUntil, id)
		s.absent[id] = 0
		s.persist()
		label := sessionLabel(w.Name, w.SessionID)
		s.logInfo(label, "babysitting resumed"+via.Suffix())
		return Result{OK: true, Message: "Babysitting " + label + " again."}
	})
}

// SetSettings stores the user's preferences, applying the ones that have an
// effect outside this program straight away.
func (s *Supervisor) SetSettings(next state.Settings, via Via) Result {
	return s.ask(func(context.Context) Result { return s.setSettings(next, via) })
}

func (s *Supervisor) setSettings(next state.Settings, via Via) Result {
	// The keep-awake rule is not a preference: whatever arrives here, the
	// request follows the watches. The field is still stored, so a build that
	// reads the file finds an answer it understands.
	next.KeepAwakeMode = state.KeepAwakeWhileBabysitting
	if !state.ValidTheme(next.Theme) {
		return Result{Message: "Theme must be dark, light or auto."}
	}
	previous := s.st.Settings
	changingAutostart := next.Autostart != previous.Autostart
	if changingAutostart && s.deps.Autostart == nil {
		return Result{Message: "Starting at login is not supported on this system."}
	}

	// The settings are saved before the one that reaches outside this
	// program is applied. A crash between the two then leaves a file this
	// program wrote itself, rather than a login entry nothing remembers
	// asking for.
	s.st.Settings = next
	s.persist()

	if changingAutostart {
		path, err := s.deps.Autostart(next.Autostart)
		if err != nil {
			// The stored preference has to go back to what it was: leaving
			// it switched on would tell the user, every time they look,
			// that something is set up which is not.
			s.st.Settings = previous
			s.persist()
			return Result{Message: "Could not change starting at login: " + err.Error()}
		}
		if next.Autostart {
			s.logInfo("", "start at login enabled: wrote "+path+via.Suffix())
		} else {
			s.logInfo("", "start at login disabled: removed "+path+via.Suffix())
		}
	}

	return Result{OK: true, Message: "Settings saved."}
}

// CanUnbabysit reports whether a watch in this state may be let go. With a
// display, only a session still in its own app, or a Stuck watch, is let
// go: once our copy carries a session, the way out is to stop that copy. A
// server has no app to hand a session back to, so there a watch may be let
// go in every state but Starting, and what is left is an ordinary
// background session. It is exported because the demonstration engine
// follows the same rule.
func CanUnbabysit(st WatchState, headless bool) bool {
	switch st {
	case StateWatching, StateStuck:
		return true
	case StateInBackground:
		return headless
	default:
		return false
	}
}

// switchOnStartAtLogin turns start at login on for a babysit that asked for
// it, the same way the Settings switch does, and says how that went.
func (s *Supervisor) switchOnStartAtLogin(via Via) string {
	next := s.st.Settings
	next.Autostart = true
	if res := s.setSettings(next, via); !res.OK {
		return res.Message
	}
	return "Start at login is on."
}

// refreshSnap takes the observer's newest snapshot when it is newer than
// the one the loop last reconciled, so an action never judges a session by
// a snapshot older than the page it was asked from.
func (s *Supervisor) refreshSnap() {
	if s.deps.Obs != nil {
		if snap := s.deps.Obs.Current(); snap.At.After(s.snap.At) {
			s.snap = snap
		}
	}
}

// liveSession finds a session by id in the newest snapshot available,
// preferring a host we can act on over one we cannot.
func (s *Supervisor) liveSession(id string) (claude.Session, bool) {
	s.refreshSnap()
	live := s.snap.All(id)
	if len(live) == 0 {
		return claude.Session{}, false
	}
	for _, sn := range live {
		if sn.Host != claude.HostOther {
			return sn, true
		}
	}
	return live[0], true
}

// removeWatch drops the watch for id from the list, along with everything
// the loop was remembering about it. A session taken up again later starts
// from silence rather than inheriting a complaint about a copy that is
// long gone.
func (s *Supervisor) removeWatch(id string) {
	kept := make([]state.Watch, 0, len(s.st.Watches))
	for _, w := range s.st.Watches {
		if w.SessionID == id {
			continue
		}
		kept = append(kept, w)
	}
	s.st.Watches = kept
	s.trouble.forget(id)
}

// whereItIs describes where a session is running right now.
func whereItIs(live []claude.Session) string {
	if len(live) == 0 {
		return "It is not running anywhere right now."
	}
	names := make([]string, 0, len(live))
	for _, sn := range live {
		names = append(names, hostLabel(sn.Host))
	}
	return "It is now in " + strings.Join(names, " and ") + "."
}

// remoteControlSentence says whether a session can be reached through
// Remote Control, and what to do about it when it cannot.
func remoteControlSentence(sn claude.Session) string {
	if sn.RemoteControl {
		return "Remote Control is on."
	}
	return "Remote Control is off, so your other devices can't reach it yet. " + RCHint(sn.Host, sn.ShortID)
}

// notedAlreadyLive brings a watch back in step with a session a resume found
// running already. Nothing was started, so the watch is only told where the
// session really is.
func (s *Supervisor) notedAlreadyLive(id string, res Result) {
	cur := s.find(id)
	if cur == nil {
		return
	}
	cur.Failures = nil
	if res.AlreadyLiveIn == claude.HostBackground {
		// The thing still running is our own kind of copy, so the promise is
		// the one a fallback makes, unless the background is where the
		// session lived to begin with. A copy that is running has also saved
		// its own options, which any later resume of it obeys.
		if cur.OriginHost != claude.HostBackground {
			cur.PromiseState = "fallback"
			if cur.BackgroundSince.IsZero() {
				cur.BackgroundSince = s.deps.Now()
			}
		}
		cur.HasSavedOptions = true
		if res.ShortID != "" {
			cur.ShortID = res.ShortID
		}
	} else {
		cur.PromiseState = "inplace"
		cur.BackgroundSince = time.Time{}
		cur.OriginHost = res.AlreadyLiveIn
	}
	s.absent[id] = 0
	delete(s.backoffUntil, id)
	s.persist()
}
