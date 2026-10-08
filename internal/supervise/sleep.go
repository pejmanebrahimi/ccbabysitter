package supervise

import "time"

// sleepSpan is a time the computer slept while it was kept awake for
// babysat sessions: from when it was last seen awake to when it was seen
// again.
type sleepSpan struct {
	from, to time.Time
}

const (
	// sleptAfter is how long between two looks says the computer slept:
	// while it is awake, the observer looks every few seconds.
	sleptAfter = time.Minute
	// awakeFor is how long the computer must stay awake after a sleep before
	// the sleep is named. A Mac with its lid shut wakes for a minute now and
	// then, and those wakes are part of the one sleep.
	awakeFor = 2 * time.Minute
)

// noteSleep looks, on every pass, for a sleep of the computer while it was
// kept awake for babysat sessions, as a closed laptop lid makes it sleep
// anyway, and names it in Activity once the computer has stayed awake. The
// wall clock is read, since it goes on while the computer sleeps.
func (s *Supervisor) noteSleep() {
	now := s.deps.Now().Round(0)
	last := s.lastLook
	s.lastLook = now
	if last.IsZero() {
		return
	}
	if now.Sub(last) >= sleptAfter {
		switch {
		case s.slept != nil && last.Sub(s.slept.to) < awakeFor:
			s.slept.to = now
		case s.held:
			s.slept = &sleepSpan{from: last, to: now}
		}
		return
	}
	if s.slept != nil && now.Sub(s.slept.to) >= awakeFor {
		span := *s.slept
		s.slept = nil
		s.tellSleep(span)
	}
}

// tellSleep writes the Activity line for a sleep, with its cause when the
// system's power log tells. Reading the log can take seconds, so it is
// done aside, and the loop goes on meanwhile.
func (s *Supervisor) tellSleep(span sleepSpan) {
	cause := s.deps.SleepCause
	go func() {
		c := ""
		if cause != nil {
			c = cause(span.from, span.to)
		}
		s.logInfo("", sleptWords(c, span.from, span.to))
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
