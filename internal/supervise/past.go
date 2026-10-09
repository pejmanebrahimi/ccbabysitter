package supervise

import (
	"sort"
	"strings"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
)

const (
	// pastWindow is how far back the Not running list reaches.
	pastWindow = 14 * 24 * time.Hour
	// pastMax is the most conversations the Not running list shows.
	pastMax = 30
	// pastInterval is how often the list is looked for again while the set
	// of live sessions stays the same. A change in that set asks at once,
	// since a session that just stopped belongs on the list.
	pastInterval = 30 * time.Second
)

// PastView is one conversation that is not running anywhere, as the Not
// running list shows it.
type PastView struct {
	ID           string    `json:"id"`
	ShortID      string    `json:"shortId"`
	Name         string    `json:"name"`
	Cwd          string    `json:"cwd"`
	LastActivity time.Time `json:"lastActivity"`
	ResumeCmd    string    `json:"resumeCmd"`
	// HandedBackTo is the app the session was handed back to from the
	// background in the last day, desktop, vscode or terminal, and empty
	// for every other conversation.
	HandedBackTo claude.Host `json:"handedBackTo"`
	// AttachCmd starts a background session this program stopped again,
	// as it was, and SSHAttachCmd does the same from another machine,
	// which only a machine with no display offers. Both are empty for
	// every other conversation, which is resumed with ResumeCmd instead.
	AttachCmd    string `json:"attachCmd,omitempty"`
	SSHAttachCmd string `json:"sshAttachCmd,omitempty"`
	// CopyShortID is the short id of the background copy that was stopped,
	// the one the command line showed for it, and ActivityLabel the name
	// Activity knew the session by, both for a session stopped or handed
	// back from here.
	CopyShortID   string `json:"copyShortId,omitempty"`
	ActivityLabel string `json:"activityLabel,omitempty"`
}

// handBackFor is how long a session handed back to its app is marked as
// such on the Not running list.
const handBackFor = 24 * time.Hour

// handBack is one session handed back from the background to its app.
type handBack struct {
	to claude.Host
	at time.Time
	// pid is the background copy that was stopped, which a snapshot can
	// still show for a moment while it goes. It is not the session
	// running again.
	pid int
	// label is the name Activity knew the session by.
	label string
}

// stoppedCopy is one background session whose copy Stop ended, without
// handing it back to an app. Claude keeps a stopped background session on
// its own list, and `claude attach` with its short id starts it again.
type stoppedCopy struct {
	short string
	at    time.Time
	// pid is the copy that was stopped, which a snapshot can still show
	// for a moment while it goes.
	pid int
	// label is the name Activity knew the session by.
	label string
}

// handBackTo names the app a session goes back to, the way the page's way
// back names it: a session from anywhere but Desktop or VS Code goes back
// to a terminal.
func handBackTo(origin claude.Host) claude.Host {
	switch origin {
	case claude.HostDesktop, claude.HostVSCode:
		return origin
	default:
		return claude.HostTerminal
	}
}

// forgetHandBacks drops every hand-back that is a day old, every stopped
// copy older than the Not running list reaches, and every one of either
// whose session is babysat or running again anywhere.
func (s *Supervisor) forgetHandBacks(snap observe.Snapshot) {
	now := s.deps.Now()
	for id, hb := range s.handedBack {
		if now.Sub(hb.at) >= handBackFor || s.runsAgain(id, hb.pid, snap) {
			delete(s.handedBack, id)
		}
	}
	for id, sc := range s.stopped {
		if now.Sub(sc.at) >= pastWindow || s.runsAgain(id, sc.pid, snap) {
			delete(s.stopped, id)
		}
	}
}

// runsAgain reports whether a session whose copy with the given pid was
// stopped is babysat again or running anywhere under another process.
func (s *Supervisor) runsAgain(id string, pid int, snap observe.Snapshot) bool {
	if s.find(id) != nil {
		return true
	}
	for _, sn := range snap.All(id) {
		if sn.PID != pid {
			return true
		}
	}
	return false
}

// askForPast asks the stats worker for the list of recent conversations
// when the one held is old or the live sessions have changed since. Reading
// the project folders is disk work, so it never happens on the loop.
//
// A list wanted while one is still being read is asked for again once
// that answer is in, since the answer on its way was asked for against an
// older set of live sessions.
func (s *Supervisor) askForPast(snap observe.Snapshot) {
	now := s.deps.Now()
	live := liveKey(snap)
	if !s.pastAt.IsZero() && live == s.pastLive && now.Sub(s.pastAt) < pastInterval {
		return
	}
	if s.pastInFlight {
		s.pastWanted = true
		return
	}
	// A session just handed back or stopped is still in the snapshot while
	// its copy goes, and is looked for anyway, so its row is there the
	// moment the copy is gone.
	skip := map[string]bool{}
	for _, sn := range snap.Sessions {
		_, handed := s.handedBack[sn.ID]
		_, stopped := s.stopped[sn.ID]
		if !handed && !stopped {
			skip[sn.ID] = true
		}
	}
	for _, w := range s.st.Watches {
		skip[w.SessionID] = true
	}
	if s.postStats(statsRequest{past: true, since: now.Add(-pastWindow), skip: skip}) {
		s.pastAt, s.pastLive, s.pastInFlight = now, live, true
	}
}

// liveKey names the set of live sessions in one string, so a change in it
// can be noticed.
func liveKey(snap observe.Snapshot) string {
	ids := make([]string, 0, len(snap.Sessions))
	for _, sn := range snap.Sessions {
		ids = append(ids, sn.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, " ")
}

// pastViews renders the list held, leaving out anything that has started
// running or been babysat since it was read. A session handed back to its
// app in the last day says where it went and comes first.
func (s *Supervisor) pastViews(watched map[string]bool) []PastView {
	now := s.deps.Now()
	out := []PastView{}
	for _, p := range s.past {
		if watched[p.ID] || len(s.snap.All(p.ID)) > 0 {
			continue
		}
		pv := PastView{
			ID: p.ID, ShortID: shortOf(p.ID), Name: p.Name, Cwd: p.Cwd,
			LastActivity: p.LastActivity, ResumeCmd: hosts.ResumeCommandIn(p.Cwd, p.ID),
		}
		if hb, ok := s.handedBack[p.ID]; ok && now.Sub(hb.at) < handBackFor {
			pv.HandedBackTo = hb.to
			pv.ActivityLabel = hb.label
		} else if sc, ok := s.stopped[p.ID]; ok {
			pv.AttachCmd = hosts.AttachCommand(sc.short)
			pv.SSHAttachCmd = s.sshAttachFor(sc.short)
			pv.CopyShortID = sc.short
			pv.ActivityLabel = sc.label
		}
		out = append(out, pv)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].HandedBackTo != "" && out[j].HandedBackTo == ""
	})
	if len(out) > pastMax {
		out = out[:pastMax]
	}
	return out
}
