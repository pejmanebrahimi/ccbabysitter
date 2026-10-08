package supervise

import (
	"fmt"
	"regexp"
	"strconv"
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
	desktopClosed       = "Claude Desktop closed"
	computerRestarted   = "the computer restarted"
	desktopUpdatedFmt   = "Claude Desktop closed and updated from %s to %s"
	desktopRestartedFmt = "Claude Desktop restarted for an update from %s to %s"
)

// desktopKeptRunning is the reason for a session that went down while
// Claude Desktop stayed up.
const desktopKeptRunning = "the session ended while Claude Desktop kept running"

var versionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)+$`)

// realVersion reports whether v is an actual version number rather than
// "installed" or nothing.
func realVersion(v string) bool { return versionPattern.MatchString(v) }

// causeFor is the reason a babysat session went down, from the app it ran
// in, the desktop app as it is now, and the desktop version last seen
// running. A new version on disk means the app updated; seen running
// again, it restarted for the update. It is "" when nothing tells.
func causeFor(host claude.Host, apps AppsNow, knownVersion string) string {
	switch host {
	case claude.HostDesktop:
		if !apps.DesktopKnown {
			return ""
		}
		updated := realVersion(knownVersion) && realVersion(apps.DesktopVersion) && knownVersion != apps.DesktopVersion
		switch {
		case updated && apps.DesktopRunning:
			return fmt.Sprintf(desktopRestartedFmt, knownVersion, apps.DesktopVersion)
		case updated:
			return fmt.Sprintf(desktopUpdatedFmt, knownVersion, apps.DesktopVersion)
		case apps.DesktopRunning:
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

// Ranks of the reasons a look at Claude Desktop can give, by how much each
// explains.
const (
	rankKeptRunning = 1 + iota
	rankClosed
	rankUpdated
	rankRestarted
)

// desktopRank orders the reasons a look at Claude Desktop can give by how
// much each explains, so a later look replaces only a weaker one. Other
// reasons are 0.
func desktopRank(reason string) int {
	switch {
	case strings.HasPrefix(reason, "Claude Desktop restarted for an update"):
		return rankRestarted
	case strings.HasPrefix(reason, "Claude Desktop closed and updated"):
		return rankUpdated
	case reason == desktopClosed:
		return rankClosed
	case reason == desktopKeptRunning:
		return rankKeptRunning
	}
	return 0
}

// groupable reports whether a reason closes several sessions at once, so
// one line can name it for all of them.
func groupable(reason string) bool {
	return reason == computerRestarted || desktopRank(reason) >= rankClosed
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
	// at least the time the computer was up. A shutdown with Windows Fast
	// Startup does not move it at all, and is not told.
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

// appsOnce asks for the desktop app's state the first time it is wanted,
// and not again.
func (s *Supervisor) appsOnce() func() AppsNow {
	var apps *AppsNow
	return func() AppsNow {
		if apps == nil {
			now := s.appsNow()
			apps = &now
		}
		return *apps
	}
}

// hostOf is the app a babysat session was last seen running in. Until it
// is seen, that is the background for a watch our copy carries, and the
// app it was babysat in otherwise.
func (s *Supervisor) hostOf(w state.Watch) claude.Host {
	if host, ok := s.lastHost[w.SessionID]; ok {
		return host
	}
	if w.PromiseState == "fallback" {
		return claude.HostBackground
	}
	return w.OriginHost
}

// anyInDesktop reports whether a babysat session runs in the desktop app,
// as far as the last look saw.
func (s *Supervisor) anyInDesktop() bool {
	for _, w := range s.st.Watches {
		if s.hostOf(w) == claude.HostDesktop && s.absent[w.SessionID] == 0 {
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
// A session not seen running since the program started went down while it
// was not running, so how the apps look now says nothing about why: only a
// restart of the computer since the run before is told.
func (s *Supervisor) explainExits(gone []state.Watch) {
	apps := s.appsOnce()
	for _, w := range gone {
		host, seen := s.lastHost[w.SessionID]
		if !seen {
			if s.rebooted {
				s.setCause(w.SessionID, computerRestarted)
			}
			continue
		}
		var now AppsNow
		if host == claude.HostDesktop {
			now = apps()
		}
		if reason := causeFor(host, now, s.desktopVersion); reason != "" {
			s.setCause(w.SessionID, reason)
		}
	}
}

// lookAgain looks at Claude Desktop again for sessions seen missing a
// second time, before any is brought back. The app ends its sessions a
// moment before it quits, so the first look can find it still running,
// and an update can change its version only after it quit: what this look
// tells replaces a weaker reason.
func (s *Supervisor) lookAgain(again []state.Watch) {
	apps := s.appsOnce()
	for _, w := range again {
		old := s.causes[w.SessionID]
		if desktopRank(old) == 0 {
			continue
		}
		if reason := causeFor(claude.HostDesktop, apps(), s.desktopVersion); desktopRank(reason) > desktopRank(old) {
			s.setCause(w.SessionID, reason)
		}
	}
}

// setCause keeps reason as why id went down. After any reason from Claude
// Desktop its version is read afresh once sessions run in it again, and a
// desktop app that closed is followed up, to tell whether it restarted for
// an update.
func (s *Supervisor) setCause(id, reason string) {
	s.causes[id] = reason
	rank := desktopRank(reason)
	if rank > 0 {
		s.desktopVersionAt = time.Time{}
	}
	if (rank == rankClosed || rank == rankUpdated) && s.pending == nil {
		s.pending = &pendingDesktop{at: s.deps.Now(), from: s.desktopVersion}
	}
}

// announce writes, as the first of several sessions taken down by the same
// app or restart is brought back, one line naming the cause for all of
// them: every babysat session due to be brought back for it. Nothing is
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
	s.logInfo("", strings.ToUpper(reason[:1])+reason[1:]+": "+strconv.Itoa(len(due))+" babysat sessions went down.")
}

// rescued notes that id was brought back with reason: it now runs in the
// background, and what explained its way down is done with. A rescue that
// said Claude Desktop closed gets its follow-up once the app is back.
func (s *Supervisor) rescued(id, reason string) {
	s.lastHost[id] = claude.HostBackground
	delete(s.causes, id)
	delete(s.announced, id)
	s.toldCause(reason)
}

// toldCause notes that a rescue gave reason in Activity, so a desktop app
// said to have closed gets its follow-up once it is back.
func (s *Supervisor) toldCause(reason string) {
	if reason == desktopClosed && s.pending != nil {
		s.pending.told = true
	}
}

// forgetGone drops what is kept about the way down of sessions that are no
// longer babysat.
func (s *Supervisor) forgetGone() {
	for id := range s.absent {
		if s.find(id) == nil {
			delete(s.absent, id)
		}
	}
	for id := range s.lastHost {
		if s.find(id) == nil {
			delete(s.lastHost, id)
		}
	}
	for id := range s.causes {
		if s.find(id) == nil {
			delete(s.causes, id)
		}
	}
	for id := range s.announced {
		if s.find(id) == nil {
			delete(s.announced, id)
		}
	}
}

// followUpDesktop deals with a desktop app that looked closed, once it is
// running again soon after. Back with a new version, it had restarted for
// an update: sessions still to be brought back give that as their reason,
// and when a rescue already said the app closed, one line says it is back
// updated.
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
	restarted := fmt.Sprintf(desktopRestartedFmt, p.from, apps.DesktopVersion)
	for id, reason := range s.causes {
		if rank := desktopRank(reason); rank == rankClosed || rank == rankUpdated {
			s.causes[id] = restarted
		}
	}
	if p.told {
		s.logInfo("", "Claude Desktop is back, updated from "+p.from+" to "+apps.DesktopVersion+".")
	}
}
