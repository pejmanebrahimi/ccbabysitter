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
// between from, when it was last seen awake, and to, when it was seen
// again: "lid" for a closed lid, "asked" when something put it to sleep,
// "battery" for a battery running low, and "" for anything else or nothing
// found. The first sleep counts, and one up to two minutes before from
// too, since the computer may have been seen once more as it went.
func sleepCause(log io.Reader, from, to time.Time) string {
	from = from.Add(-2 * time.Minute)
	sc := bufio.NewScanner(log)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if len(line) < len(powerLogTime) {
			continue
		}
		i := strings.Index(line, sleepEntry)
		if i < 0 {
			continue
		}
		at, err := time.Parse(powerLogTime, line[:len(powerLogTime)])
		if err != nil || at.Before(from) || at.After(to) {
			continue
		}
		reason, _, _ := strings.Cut(line[i+len(sleepEntry):], "'")
		switch {
		case reason == "Clamshell Sleep":
			return "lid"
		case strings.HasPrefix(reason, "Software Sleep"):
			return "asked"
		case reason == "Low Power Sleep":
			return "battery"
		}
		return ""
	}
	return ""
}
