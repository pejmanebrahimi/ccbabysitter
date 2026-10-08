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

// sleepCause reads, from macOS's power log, why the computer went to sleep
// in a sleep it was last seen awake before, at from, and seen again after,
// at to, and when it went. The sleep that counts is the first one after the
// computer was last fully awake before from: it may have gone to sleep a
// while before it was last seen, when a short wake in the dark came in
// between. The reason is "lid" for a closed lid, "asked" when something put
// it to sleep, "battery" for a battery running low, and "" for anything
// else. Both are zero when the log shows no sleep then.
func sleepCause(log io.Reader, from, to time.Time) (string, time.Time) {
	var cause string
	var start time.Time
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
		if at.After(to) {
			break
		}
		// The kind of entry is the column before the tab: "Wake" alone is
		// a full wake, while "Wake Requests", "WakeDetails" and the like,
		// written as the computer sleeps, are not.
		column, _, _ := strings.Cut(line[len(powerLogTime):], "\t")
		switch strings.TrimSpace(column) {
		case "Wake":
			// Fully awake before it was last seen: any sleep before this
			// one is over and done with.
			if at.Before(from) {
				cause, start = "", time.Time{}
			}
		case "Sleep":
			i := strings.Index(line, sleepEntry)
			if i < 0 || !start.IsZero() {
				continue
			}
			reason, _, _ := strings.Cut(line[i+len(sleepEntry):], "'")
			start = at
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
	return cause, start
}

// lidClosed reads ioreg's answer about the clamshell: the lid is closed and
// closing it puts the computer to sleep. A Mac running on an external
// display keeps going with its lid shut, and does not count.
func lidClosed(out string) bool {
	return strings.Contains(out, `"AppleClamshellState" = Yes`) && strings.Contains(out, `"AppleClamshellCausesSleep" = Yes`)
}
