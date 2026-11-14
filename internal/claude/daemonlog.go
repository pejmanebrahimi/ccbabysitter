package claude

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// DaemonLogPath returns the CLI daemon's log file, ~/.claude/daemon.log.
func DaemonLogPath() string { return filepath.Join(Dir(), "daemon.log") }

// retireLine matches the daemon's line for stopping an idle background
// session, such as
// "[2026-10-06T14:07:36.616Z] [bg] bg retire 1a2b3c4d: settled, idle 8h".
var retireLine = regexp.MustCompile(`^\[([^\]]+)\] \[bg\] bg retire ([0-9a-f]{8}): [^,]+, idle ([0-9hms.]+)`)

var shortIDPattern = regexp.MustCompile(`^[0-9a-f]{8}$`)

// retireTail is how much of the end of the daemon log Retired and
// DaemonBusy read: the lines they look for were written moments before.
const retireTail = 256 << 10

// settledLine matches the daemon's line for a job that has finished
// stopping, such as "[2026-10-16T11:34:31.527Z] [bg] bg settled 304152c6 (done)".
var settledLine = regexp.MustCompile(`^\[([^\]]+)\] \[bg\] bg settled ([0-9a-f]{8}) `)

// startLine matches the line the daemon writes when it starts, such as
// "[2026-10-06T21:17:05.472Z] [supervisor] --- daemon start --- version=...",
// drawn with box characters rather than dashes.
var startLine = regexp.MustCompile(`^\[([^\]]+)\] \[supervisor\] \S+ daemon start `)

// retiringFor is how long after a retire line DaemonBusy takes the job as
// still stopping when no settled line follows. The daemon settles a job
// within a second; a log that never says so must not hold a resume back
// for longer than this.
const retiringFor = 15 * time.Second

// startingFor is how long after it starts DaemonBusy takes the daemon as
// still loading its jobs. It has loaded them within two seconds.
const startingFor = 5 * time.Second

// DaemonBusy reports whether the CLI daemon's log at path says that, at
// now, the daemon is still stopping the background job short, or has only
// just started and not yet loaded its jobs. A resume in either moment
// starts a new copy instead of waking the job. Only recent lines count, and
// a missing log or a format it does not know is not busy.
func DaemonBusy(path, short string, now time.Time) bool {
	if !shortIDPattern.MatchString(short) {
		return false
	}
	sc, ok := daemonTail(path)
	if !ok {
		return false
	}
	var retiring, started time.Time
	for sc.Scan() {
		line := sc.Text()
		if m := retireLine.FindStringSubmatch(line); m != nil && m[2] == short {
			if at, err := time.Parse(time.RFC3339Nano, m[1]); err == nil {
				retiring = at
			}
			continue
		}
		if m := settledLine.FindStringSubmatch(line); m != nil && m[2] == short {
			retiring = time.Time{}
			continue
		}
		if m := startLine.FindStringSubmatch(line); m != nil {
			if at, err := time.Parse(time.RFC3339Nano, m[1]); err == nil {
				started = at
			}
		}
	}
	recent := func(at time.Time, d time.Duration) bool {
		return !at.IsZero() && !at.After(now) && now.Sub(at) < d
	}
	return recent(retiring, retiringFor) || recent(started, startingFor)
}

// daemonTail opens the daemon log at path and returns a scanner over its
// last retireTail bytes. It never writes, and a log it cannot read is no
// answer.
func daemonTail(path string) (*bufio.Scanner, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > retireTail {
		if _, err := f.Seek(-retireTail, io.SeekEnd); err != nil {
			return nil, false
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, retireTail))
	if err != nil {
		return nil, false
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	return sc, true
}

// Retired reports whether the CLI daemon's log at path says the daemon
// stopped the idle background session short after from and no later than
// to, and how long that session had been idle. It reads only the end of the log and
// never writes. A missing log, an odd line or a format it does not know is
// simply no answer: the daemon's log is the CLI's own and may change.
func Retired(path, short string, from, to time.Time) (time.Duration, bool) {
	if !shortIDPattern.MatchString(short) {
		return 0, false
	}
	sc, ok := daemonTail(path)
	if !ok {
		return 0, false
	}
	var idle time.Duration
	found := false
	for sc.Scan() {
		m := retireLine.FindStringSubmatch(sc.Text())
		if m == nil || m[2] != short {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, m[1])
		if err != nil || !at.After(from) || at.After(to) {
			continue
		}
		d, err := time.ParseDuration(m[3])
		if err != nil {
			continue
		}
		idle, found = d, true
	}
	return idle, found
}
