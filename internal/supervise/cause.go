package supervise

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// AppsNow is what the desktop app looks like at one moment: whether this
// system can tell its own process is running at all (Linux cannot),
// whether it is, and the version installed on disk ("" or "installed"
// when that is not known).
type AppsNow struct {
	DesktopKnown   bool
	DesktopRunning bool
	DesktopVersion string
}

// unknownExit is the reason given when nothing tells why a session's
// process went away.
const unknownExit = "the process running it exited"

// Reasons that name the app or the computer going down, which close every
// session in them at once and are worth one line for all of them.
const (
	desktopClosed     = "Claude Desktop closed"
	computerRestarted = "the computer restarted"
	desktopUpdatedFmt = "Claude Desktop restarted for an update from %s to %s"
)

// desktopKeptRunning is the reason for a session that went down while
// Claude Desktop stayed up.
const desktopKeptRunning = "the session ended while Claude Desktop kept running"

var versionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)+$`)

// realVersion reports whether v is an actual version number rather than
// "installed" or nothing.
func realVersion(v string) bool { return versionPattern.MatchString(v) }

// causeFor is the reason a babysat session went down, from the app it ran
// in, the desktop app as it was the moment the session went missing, the
// desktop version last seen running before that, and whether the computer
// had just restarted. It is "" when nothing tells.
func causeFor(host claude.Host, apps AppsNow, knownVersion string, restarted bool) string {
	if restarted {
		return computerRestarted
	}
	switch host {
	case claude.HostDesktop:
		if !apps.DesktopKnown {
			return ""
		}
		if realVersion(knownVersion) && realVersion(apps.DesktopVersion) && knownVersion != apps.DesktopVersion {
			return fmt.Sprintf(desktopUpdatedFmt, knownVersion, apps.DesktopVersion)
		}
		if apps.DesktopRunning {
			return desktopKeptRunning
		}
		return desktopClosed
	case claude.HostVSCode:
		return "VS Code closed, or the session ended in it"
	case claude.HostTerminal:
		return "its terminal closed, or the session was quit"
	case claude.HostBackground:
		return "the background copy ended"
	}
	return ""
}

// desktopRank orders the reasons a look at Claude Desktop can give by how
// much each explains, so a later look replaces only a weaker one. Other
// reasons are 0.
func desktopRank(reason string) int {
	switch {
	case strings.HasPrefix(reason, "Claude Desktop restarted for an update"):
		return 3
	case reason == desktopClosed:
		return 2
	case reason == desktopKeptRunning:
		return 1
	}
	return 0
}

// groupable reports whether a reason closes several sessions at once, so
// one line can name it for all of them.
func groupable(reason string) bool {
	return reason == desktopClosed || reason == computerRestarted || strings.HasPrefix(reason, "Claude Desktop restarted for an update")
}

// pendingDesktop is a desktop app that looked closed when sessions went
// down in it: if it comes back with a new version soon after, it had
// restarted for an update. told says a rescue has already given "Claude
// Desktop closed" as its reason in Activity.
type pendingDesktop struct {
	at   time.Time
	from string
	told bool
}

const (
	// desktopFollowUpFor is how long after the app looked closed its return
	// with a new version still explains that close.
	desktopFollowUpFor = 10 * time.Minute
	// desktopVersionEvery is how often the desktop version is read again
	// while sessions in the app are babysat, so an update that came and
	// went without taking a babysat session down is soon the version known.
	desktopVersionEvery = time.Minute
	// bootSlack is how far, in seconds, two readings of the boot time may
	// be apart and still be the same boot: Windows works it out from the
	// time since boot, and a clock change moves it. A restart moves it by
	// at least the time the computer was up.
	bootSlack = 60
)

// appsNow asks for the desktop app's state, which is nothing known when the
// program has no way to ask.
func (s *Supervisor) appsNow() AppsNow {
	if s.deps.Apps == nil {
		return AppsNow{}
	}
	return s.deps.Apps()
}

// anyInDesktop reports whether a babysat session was last seen running in
// the desktop app, or was babysat there and has not been seen since.
func (s *Supervisor) anyInDesktop() bool {
	for _, w := range s.st.Watches {
		host, ok := s.lastHost[w.SessionID]
		if !ok {
			host = w.OriginHost
		}
		if host == claude.HostDesktop && s.absent[w.SessionID] == 0 {
			return true
		}
	}
	return false
}

// noteDesktopVersion keeps the desktop version last seen running, read at
// most every desktopVersionEvery while a babysat session runs in the app.
func (s *Supervisor) noteDesktopVersion() {
	if !s.desktopVersionAt.IsZero() && s.deps.Now().Sub(s.desktopVersionAt) < desktopVersionEvery {
		return
	}
	s.desktopVersionAt = s.deps.Now()
	if apps := s.appsNow(); apps.DesktopRunning && realVersion(apps.DesktopVersion) {
		s.desktopVersion = apps.DesktopVersion
	}
}

// noteBoot saves the boot time this run starts in, and remembers whether
// the computer restarted since the run before, which saved its own.
func (s *Supervisor) noteBoot() {
	if s.deps.BootTime == nil {
		return
	}
	now := s.deps.BootTime()
	if now == 0 {
		return
	}
	prev := s.st.BootTime
	s.rebooted = prev != 0 && (now > prev+bootSlack || prev > now+bootSlack)
	if prev != now {
		s.st.BootTime = now
		s.persist()
	}
}

// explainExits works out, the moment babysat sessions are first seen
// missing, why they went down, and keeps the reason for their rescue line.
// first says this is the first look since the program started, when a
// session missing after a restart went down with the computer.
func (s *Supervisor) explainExits(gone []state.Watch, first bool) {
	apps := s.appsNow()
	restarted := first && s.rebooted
	for _, w := range gone {
		host, ok := s.lastHost[w.SessionID]
		if !ok {
			host = w.OriginHost
		}
		reason := causeFor(host, apps, s.desktopVersion, restarted)
		if reason == "" {
			continue
		}
		s.causes[w.SessionID] = reason
		if desktopRank(reason) > 0 {
			// Read the version afresh once sessions run in the app again,
			// whatever this turns out to have been.
			s.desktopVersionAt = time.Time{}
		}
		if reason == desktopClosed && s.pending == nil {
			s.pending = &pendingDesktop{at: s.deps.Now(), from: s.desktopVersion}
		}
	}
}

// lookAgain looks at Claude Desktop again for sessions seen missing a
// second time, before any is brought back. The app ends its sessions a
// moment before it quits, so the first look can find it still running,
// and an update can change its version only after it quit: what this look
// tells replaces a weaker reason.
func (s *Supervisor) lookAgain(again []state.Watch) {
	var apps *AppsNow
	for _, w := range again {
		old := s.causes[w.SessionID]
		if desktopRank(old) == 0 {
			continue
		}
		if apps == nil {
			now := s.appsNow()
			apps = &now
		}
		reason := causeFor(claude.HostDesktop, *apps, s.desktopVersion, false)
		if desktopRank(reason) <= desktopRank(old) {
			continue
		}
		s.causes[w.SessionID] = reason
		if reason == desktopClosed && s.pending == nil {
			s.pending = &pendingDesktop{at: s.deps.Now(), from: s.desktopVersion}
		}
	}
}

// announce writes, as the first of several sessions taken down by the same
// app or restart is brought back, one line naming the cause for all of
// them: every babysat session due to be brought back for it. A session
// that comes back by itself before then is not counted, and nothing is
// said for one session alone.
func (s *Supervisor) announce(id, reason string) {
	if !groupable(reason) || s.announced[id] {
		return
	}
	var due []string
	for _, w := range s.st.Watches {
		if !w.Paused && !s.announced[w.SessionID] && s.absent[w.SessionID] >= AbsentSweepsRequired && s.causes[w.SessionID] == reason {
			due = append(due, w.SessionID)
		}
	}
	if len(due) < 2 {
		return
	}
	for _, d := range due {
		s.announced[d] = true
	}
	s.logInfo("", strings.ToUpper(reason[:1])+reason[1:]+". Bringing back "+plural(len(due), "babysat session", "babysat sessions")+".")
}

// toldCause notes that a rescue gave reason in Activity, so a desktop app
// said to have closed gets its follow-up once it is back.
func (s *Supervisor) toldCause(reason string) {
	if reason == desktopClosed && s.pending != nil {
		s.pending.told = true
	}
}

// followUpDesktop deals with a desktop app that looked closed, once it is
// running again soon after. Back with a new version, it had restarted for
// an update: sessions still to be brought back give the update as their
// reason, and when a rescue already said the app closed, one line says
// what really happened.
func (s *Supervisor) followUpDesktop() {
	if s.pending == nil {
		return
	}
	if s.deps.Now().Sub(s.pending.at) > desktopFollowUpFor {
		s.pending = nil
		return
	}
	apps := s.appsNow()
	if !apps.DesktopRunning {
		return
	}
	p := *s.pending
	s.pending = nil
	if realVersion(apps.DesktopVersion) {
		s.desktopVersion, s.desktopVersionAt = apps.DesktopVersion, s.deps.Now()
	}
	if !realVersion(p.from) || !realVersion(apps.DesktopVersion) || p.from == apps.DesktopVersion {
		return
	}
	updated := fmt.Sprintf(desktopUpdatedFmt, p.from, apps.DesktopVersion)
	for id, reason := range s.causes {
		if reason == desktopClosed {
			s.causes[id] = updated
		}
	}
	if p.told {
		s.logInfo("", "Claude Desktop is back as "+apps.DesktopVersion+": it had restarted for an update from "+p.from+".")
	}
}
