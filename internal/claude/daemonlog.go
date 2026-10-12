package claude

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
// stopped the idle background session short after from and no later than
// to, and how long that session had been idle. It reads only the end of the log and
// never writes. A missing log, an odd line or a format it does not know is
// simply no answer: the daemon's log is the CLI's own and may change.
func Retired(path, short string, from, to time.Time) (time.Duration, bool) {
	if !shortIDPattern.MatchString(short) {
		return 0, false
	}
	data, ok := daemonTail(path)
	if !ok {
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

// daemonTail reads the end of the daemon log at path, as much as
// retireTail, and never writes. A log that is missing or cannot be read is
// no answer.
func daemonTail(path string) ([]byte, bool) {
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
	return data, true
}

// Upgrade is the CLI daemon restarting itself on an updated program: the
// versions before and after, and when it said so.
type Upgrade struct {
	From, To string
	At       time.Time
}

// upgradeLine matches the daemon's line for noticing its program changed,
// such as "[2026-10-09T18:21:38.719Z] [supervisor] binary at
// /home/dev/.local/bin/claude changed (/home/dev/.local/share/claude/versions/2.1.295
// -> /home/dev/.local/share/claude/versions/2.1.296) - self-restarting", with
// an arrow and a long dash where this comment has plain ones. The daemon
// then starts on the new program and replaces its background sessions'
// processes.
var upgradeLine = regexp.MustCompile(`^\[([^\]]+)\] \[supervisor\] binary at .* changed \((.+) \x{2192} (.+)\)`)

// versionWord is what a version taken from the daemon's line must look
// like to be repeated in a sentence.
var versionWord = regexp.MustCompile(`^[0-9][0-9A-Za-z.+-]{0,31}$`)

// Upgraded reports the latest time the CLI daemon's log at path says the
// daemon restarted itself on an updated program, after from and no later
// than to, with the versions before and after. It reads only the end of the
// log and never writes, and a missing log, an odd line or a format it does
// not know is simply no answer, as for Retired.
func Upgraded(path string, from, to time.Time) (Upgrade, bool) {
	data, ok := daemonTail(path)
	if !ok {
		return Upgrade{}, false
	}
	var up Upgrade
	found := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		m := upgradeLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, m[1])
		if err != nil || !at.After(from) || at.After(to) {
			continue
		}
		before, after := versionOf(m[2]), versionOf(m[3])
		if before == "" || after == "" {
			continue
		}
		up, found = Upgrade{From: before, To: after, At: at}, true
	}
	return up, found
}

// versionOf is the version a program path in the daemon's log names: its
// last part, such as 2.1.296 in .../claude/versions/2.1.296, or "" when
// that does not look like a version.
func versionOf(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		path = path[i+1:]
	}
	if !versionWord.MatchString(path) {
		return ""
	}
	return path
}
