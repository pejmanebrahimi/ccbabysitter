package supervise

import (
	"context"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// StopRefused is the answer to a stop the page does not offer: on a machine
// with a display, only a babysat session carried by our own background
// copy is stopped from here.
const StopRefused = "Only a babysat session running in the background can be stopped here."

// StopAmbiguous is the answer when more than one background copy of a
// session is running and none of them is the one the watch knows, so there
// is no telling which one is meant.
const StopAmbiguous = "That session has more than one background copy running. Check `claude agents` and stop the one you mean by hand."

// StopDone is the answer to a stop that worked. Everything needed to open
// the session again is on its Not running row, where it stays, rather than
// in a message that goes away. It is exported because the demonstration
// engine says the same thing.
const StopDone = "Stopped. Open it again any time from Not running."

// Stop stops a session's background copy with `claude stop`, which keeps
// the conversation, and removes the watch on it, if there is one. It never
// runs `claude rm`.
func (s *Supervisor) Stop(id string, via Via) Result {
	return s.ask(func(ctx context.Context) Result { return s.stop(ctx, id, via) })
}

func (s *Supervisor) stop(ctx context.Context, id string, via Via) Result {
	if !s.env.CLIFound {
		return Result{Message: noCLI}
	}
	s.refreshSnap()
	live := s.snap.All(id)
	if _, running := backgroundCopy(live); !running {
		return Result{Message: "That session has no background copy running."}
	}
	w := s.find(id)
	if !s.canStop(w, live) {
		return Result{Message: StopRefused}
	}
	known := ""
	if w != nil {
		known = w.ShortID
	}
	bg, ok := copyToStop(live, known)
	if !ok {
		return Result{Message: StopAmbiguous}
	}
	if !validShortID(bg.ShortID) {
		return Result{Message: "That background copy has an id this program cannot use safely. Check `claude agents` and stop it by hand."}
	}
	name, origin := bg.Name, claude.HostBackground
	// Stopping the copy of a watch that is In background is what the way
	// back to its app does, so a stop that works hands it back.
	handing := w != nil && StateOf(*w, live) == StateInBackground
	var kept *state.Watch
	if w != nil {
		name, origin = w.Name, w.OriginHost
		saved := *w
		kept = &saved
	}
	label := sessionLabel(name, id)

	// The watch is removed and saved before the copy is stopped, so a copy
	// that goes away is never mistaken for one the promise has to start
	// again. A stop that fails puts the watch back as it was.
	if kept != nil {
		s.removeWatch(id)
		s.persist()
		delete(s.absent, id)
		delete(s.backoffUntil, id)
	}
	out, err := s.deps.Runner.Run(ctx, "", hosts.StopArgs(bg.ShortID)...)
	if err != nil {
		if kept != nil {
			s.st.Watches = append(s.st.Watches, *kept)
			s.persist()
		}
		return Result{Message: "Could not stop " + label + ": " + why(out, err) + ". Stop it by hand with `claude stop " + bg.ShortID + "`."}
	}
	if handing {
		s.handedBack[id] = handBack{to: handBackTo(origin), at: s.deps.Now(), pid: bg.PID, label: label}
		delete(s.stopped, id)
	} else {
		s.stopped[id] = stoppedCopy{short: bg.ShortID, at: s.deps.Now(), pid: bg.PID, label: label}
		delete(s.handedBack, id)
	}
	// A session that has just stopped belongs on the Not running list, so
	// the list is looked for again straight away.
	s.pastAt = time.Time{}
	s.askForPast(s.snap)
	s.logInfo(label, "stopped background copy "+bg.ShortID+via.Suffix())
	return Result{OK: true, Message: StopDone, ShortID: bg.ShortID}
}

// canStop reports whether the page may offer to stop a session's background
// copy: a babysat session carried by our copy, and on a machine with no
// display any background session at all, since there the page is the only
// way to end one without a shell.
func (s *Supervisor) canStop(w *state.Watch, live []claude.Session) bool {
	if _, running := backgroundCopy(live); !running {
		return false
	}
	if s.env.Headless {
		return true
	}
	return w != nil && StateOf(*w, live) == StateInBackground
}

// backgroundCopy returns the background session among live, if there is
// one.
func backgroundCopy(live []claude.Session) (claude.Session, bool) {
	for _, sn := range live {
		if sn.Host == claude.HostBackground {
			return sn, true
		}
	}
	return claude.Session{}, false
}

// copyToStop picks the background copy Stop means: the one with the short
// id the watch knows when it is running, otherwise the only copy there is.
// With several copies and none of them the watch's own, there is no telling
// which one is meant, and ok is false.
func copyToStop(live []claude.Session, known string) (claude.Session, bool) {
	var copies []claude.Session
	for _, sn := range live {
		if sn.Host != claude.HostBackground {
			continue
		}
		if known != "" && sn.ShortID == known {
			return sn, true
		}
		copies = append(copies, sn)
	}
	if len(copies) != 1 {
		return claude.Session{}, false
	}
	return copies[0], true
}
