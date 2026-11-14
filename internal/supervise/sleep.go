package supervise

import (
	"context"
	"time"
)

// sleepSpan is a time the computer slept while it was kept awake for
// babysat sessions: from when it was last seen awake to when it was seen
// again, and whether its lid was seen closed meanwhile.
type sleepSpan struct {
	from, to time.Time
	lid      bool
}

const (
	// sleptAfter is how long the loop may sit idle before that says the
	// computer slept: while it is awake, the observer looks every few
	// seconds.
	sleptAfter = time.Minute
	// awakeFor is how long the computer must stay awake after a sleep before
	// the sleep is named. A Mac with its lid shut wakes in the dark now and
	// then, and those wakes are part of the one sleep.
	awakeFor = 2 * time.Minute
	// wakeHold is how long nothing is resumed after the computer wakes.
	// Just after a wake, Claude Code may not list a session that runs, and
	// may still be starting sessions again itself, as after an update it
	// installed while the computer slept; a resume then forks a copy.
	wakeHold = time.Minute
)

// markIdle notes when the loop finished its work, by the wall clock, which
// goes on while the computer sleeps. A long wait after it says the computer
// slept; a long piece of work does not. A wait that long is noted here as a
// wake, once, whether a pass or an action ends it: until then, justWoke
// sees the wait itself.
func (s *Supervisor) markIdle() {
	now := s.deps.Now().Round(0)
	if !s.idleSince.IsZero() && now.Sub(s.idleSince) >= sleptAfter {
		s.woke(now, now.Sub(s.idleSince))
	}
	s.idleSince = now
}

// lidClosed reports whether the lid is closed, putting the computer to
// sleep, where this system can tell.
func (s *Supervisor) lidClosed() bool { return s.deps.LidClosed != nil && s.deps.LidClosed() }

// noteSleep looks, at the start of every pass, for a sleep of the computer
// while it was kept awake for babysat sessions, as a closed laptop lid makes
// it sleep anyway, and names it in Activity once the computer has stayed
// awake. While the lid stays shut, a wake in the dark is the same sleep.
func (s *Supervisor) noteSleep(ctx context.Context) {
	now := s.deps.Now().Round(0)
	since := s.idleSince
	if since.IsZero() {
		return
	}
	switch {
	case now.Before(since):
		// The clock was set back: a sleep waiting to be named is named now.
		if s.slept != nil {
			span := *s.slept
			s.slept = nil
			s.tellSleep(ctx, span)
		}
		return
	case now.Sub(since) >= sleptAfter:
		switch {
		case s.slept != nil && since.Sub(s.slept.to) < awakeFor:
			s.slept.to = now
		case s.held:
			s.slept = &sleepSpan{from: since, to: now}
		}
		if s.slept != nil && s.lidClosed() {
			s.slept.lid = true
		}
		return
	}
	if s.slept == nil {
		return
	}
	if s.lidClosed() {
		s.slept.lid = true
		s.slept.to = now
		return
	}
	if now.Sub(s.slept.to) >= awakeFor {
		span := *s.slept
		s.slept = nil
		s.tellSleep(ctx, span)
	}
}

// woke notes that the computer woke at now from a sleep of gap, whether or
// not it was kept awake: nothing is resumed for wakeHold, and the wait for
// Claude Code to start a session again after an update, which counts only
// time awake, is moved on by the sleep.
func (s *Supervisor) woke(now time.Time, gap time.Duration) {
	s.wokeAt = now
	for id, note := range s.versions {
		if !note.goneAt.IsZero() {
			note.asleep += gap
			s.versions[id] = note
		}
	}
}

// justWoke reports whether the computer woke less than wakeHold ago, or
// slept since the last pass ended, which a sleep in the middle of this pass
// shows before noteSleep at the start of the next one can.
func (s *Supervisor) justWoke() bool {
	now := s.deps.Now()
	if !s.idleSince.IsZero() && now.Sub(s.idleSince) >= sleptAfter {
		return true
	}
	return !s.wokeAt.IsZero() && now.Before(s.wokeAt.Add(wakeHold))
}

// tellSleep writes the Activity line for a sleep, with its cause and its
// start when the system's power log tells. Reading the log can take
// seconds, so it is done aside, and the loop goes on meanwhile; Run waits
// for it before it returns. A gap the log shows no sleep in, as when the
// clock was set forward, is not named.
func (s *Supervisor) tellSleep(ctx context.Context, span sleepSpan) {
	read := s.deps.SleepCause
	s.sleepers.Add(1)
	go func() {
		defer s.sleepers.Done()
		cause, at, known := "", time.Time{}, false
		if read != nil {
			cause, at, known = read(ctx, span.from, span.to)
		}
		if ctx.Err() != nil {
			return
		}
		if cause == "" && span.lid {
			cause = "lid"
		}
		if known && cause == "" && at.IsZero() {
			return
		}
		from := span.from
		if !at.IsZero() && at.Before(from) {
			from = at
		}
		s.logInfo("", sleptWords(cause, from, span.to))
	}()
}

// sleptWords is the Activity line for a sleep from from to to, for its
// cause as power.SleepCause names it. The day is added when the sleep went
// past midnight.
func sleptWords(cause string, from, to time.Time) string {
	head := "The computer slept."
	switch cause {
	case "lid":
		head = "The computer slept because its lid was closed."
	case "asked":
		head = "The computer was put to sleep."
	case "battery":
		head = "The computer slept because its battery ran low."
	}
	to = to.In(from.Location())
	layout := "15:04"
	if from.YearDay() != to.YearDay() || from.Year() != to.Year() {
		layout = "Jan 2 15:04"
	}
	return head + " Babysat sessions could not be reached from " + from.Format(layout) + " to " + to.Format(layout) + "."
}
