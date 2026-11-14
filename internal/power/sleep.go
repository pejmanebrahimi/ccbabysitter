package power

import (
	"bufio"
	"io"
	"strings"
	"time"
)

// sleepEntry is how macOS's power log starts the line it writes as the
// computer goes to sleep, before the reason in single quotes.
const sleepEntry = "Entering Sleep state due to '"

// powerLogTime is the layout of the time that starts each line of the
// power log.
const powerLogTime = "2006-01-02 15:04:05 -0700"

// wakeLate is how long after the computer was seen awake again the power
// log may still write the full wake that ended the sleep.
const wakeLate = time.Minute

// sleepCause reads, from macOS's power log, why the computer went to sleep
// in a sleep it was last seen awake before, at from, and seen again after,
// at to, and when it went. It went at the first sleep after the computer
// was last fully awake before from: that can be a while before it was last
// seen, when a short wake in the dark came in between. The reason is the
// first one among the sleeps since then that says something: "lid" for a
// closed lid, "asked" when something put it to sleep, "battery" for a
// battery running low, and "" when none does, such as a maintenance sleep.
// Both are zero when the log shows no sleep then, and when the last wake
// before to is a maintenance wake in the dark: the computer is still asleep
// for anyone using it, and the sleep is not over yet.
func sleepCause(log io.Reader, from, to time.Time) (string, time.Time) {
	var cause string
	var start time.Time
	dark := false
	sc := bufio.NewScanner(log)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if len(line) < len(powerLogTime) {
			continue
		}
		at, err := time.Parse(powerLogTime, line[:len(powerLogTime)])
		if err != nil {
			continue
		}
		if at.After(to.Add(wakeLate)) {
			break
		}
		// The kind of entry is the column before the tab: "Wake" alone is
		// a full wake, while "Wake Requests", "WakeDetails" and the like,
		// written as the computer sleeps, are not.
		column, _, _ := strings.Cut(line[len(powerLogTime):], "\t")
		if at.After(to) {
			// Just after the computer was seen again, only the wake that
			// ended the sleep, written late, counts.
			if strings.TrimSpace(column) == "Wake" {
				dark = false
			}
			continue
		}
		switch strings.TrimSpace(column) {
		case "DarkWake":
			dark = true
		case "Wake":
			dark = false
			// Fully awake before it was last seen: any sleep before this
			// one is over and done with.
			if at.Before(from) {
				cause, start = "", time.Time{}
			}
		case "Sleep":
			i := strings.Index(line, sleepEntry)
			if i < 0 || cause != "" {
				continue
			}
			if start.IsZero() {
				start = at
			}
			reason, _, _ := strings.Cut(line[i+len(sleepEntry):], "'")
			switch {
			case reason == "Clamshell Sleep":
				cause = "lid"
			case strings.HasPrefix(reason, "Software Sleep"):
				cause = "asked"
			case reason == "Low Power Sleep":
				cause = "battery"
			}
		}
	}
	if dark {
		return "", time.Time{}
	}
	return cause, start
}

// lidClosed reads ioreg's answer about the clamshell: the lid is closed and
// closing it puts the computer to sleep. A Mac running on an external
// display keeps going with its lid shut, and does not count.
func lidClosed(out string) bool {
	return strings.Contains(out, `"AppleClamshellState" = Yes`) && strings.Contains(out, `"AppleClamshellCausesSleep" = Yes`)
}
