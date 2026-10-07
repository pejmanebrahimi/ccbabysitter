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

// retireTail is how much of the end of the daemon log Retired reads: the
// line it looks for was written moments before.
const retireTail = 256 << 10

// Retired reports whether the CLI daemon's log at path says the daemon
// stopped the idle background session short within window before now, and
// how long that session had been idle. It reads only the end of the log and
// never writes. A missing log, an odd line or a format it does not know is
// simply no answer: the daemon's log is the CLI's own and may change.
func Retired(path, short string, now time.Time, window time.Duration) (time.Duration, bool) {
	if !shortIDPattern.MatchString(short) {
		return 0, false
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > retireTail {
		if _, err := f.Seek(-retireTail, io.SeekEnd); err != nil {
			return 0, false
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, retireTail))
	if err != nil {
		return 0, false
	}
	var idle time.Duration
	found := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		m := retireLine.FindStringSubmatch(sc.Text())
		if m == nil || m[2] != short {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, m[1])
		if err != nil || at.After(now) || now.Sub(at) > window {
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
