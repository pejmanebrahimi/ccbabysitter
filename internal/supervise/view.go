package supervise

import (
	"sort"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/buildinfo"
	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// TreeView is one session's process tree as the page shows it. It exists
// rather than the raw process tree type so every field carries the name
// the page reads it under.
type TreeView struct {
	CPUPercent    float64  `json:"cpuPercent"`
	RSSBytes      uint64   `json:"rssBytes"`
	Children      []string `json:"children"`
	UptimeSeconds int64    `json:"uptimeSeconds"`
	Processes     int      `json:"processes"`
}

// SessionView is one live session that is not being babysat.
type SessionView struct {
	ID      string `json:"id"`
	ShortID string `json:"shortId"`
	PID     int    `json:"pid"`
	// ProcStart is the start time the session's file recorded for PID, as
	// written there, and empty when the session runs nowhere. A command
	// running inside the session uses it to tell its own session's process
	// from a later one that reused the pid.
	ProcStart string `json:"procStart"`
	Cwd       string `json:"cwd"`
	Name      string `json:"name"`
	// AlsoCalled is the name in the session's own record when Name, the
	// one its app shows, is a different one, and empty otherwise.
	AlsoCalled    string       `json:"alsoCalled"`
	Host          claude.Host  `json:"host"`
	Entrypoint    string       `json:"entrypoint"`
	RemoteControl bool         `json:"remoteControl"`
	Status        string       `json:"status"`
	Stats         claude.Stats `json:"stats"`
	Tree          TreeView     `json:"tree"`
	Watched       bool         `json:"watched"`
	// Live is every host the session is running in, one for each process,
	// the host this entry is taken from first. A session open in two apps
	// at once is still one entry.
	Live []claude.Host `json:"live"`
	// FallbackWarning says why a background copy would not start for this
	// session, for the babysit dialog to show before anything is promised.
	FallbackWarning string `json:"fallbackWarning"`
	// ScheduledTask reports whether the session is a run of a Claude
	// Desktop scheduled task, which is never babysat. The page lists runs
	// apart from the other sessions.
	ScheduledTask bool `json:"scheduledTask"`
	// CanStop reports whether the page offers to stop this session's
	// background copy, which only a machine with no display does for a
	// session that is not babysat.
	CanStop bool `json:"canStop"`
	// AttachCmd is the command that attaches a terminal to a background
	// session, and SSHAttachCmd the same from another machine, which only a
	// machine with no display offers. Both are empty for any other session.
	AttachCmd    string `json:"attachCmd,omitempty"`
	SSHAttachCmd string `json:"sshAttachCmd,omitempty"`
	// Actionable is false for a session belonging to another program:
	// nothing here knows how to close or resume one, so the page offers no
	// buttons for it.
	Actionable bool `json:"actionable"`
}

// WatchView is one babysat session: what was promised about it, where it
// actually is right now, and what the user can do about it.
type WatchView struct {
	state.Watch
	// State is what the watch is doing right now; the page's card and its
	// one action follow it.
	State WatchState `json:"state"`
	// AlsoCalled is the name the watch was saved with when the name shown,
	// the one the session's app shows, is a different one.
	AlsoCalled string `json:"alsoCalled"`
	// Host is the app the session is running in right now, our own copy
	// while it carries the session, and empty when it runs nowhere.
	Host claude.Host `json:"host"`
	// Live is every host the session is currently running in.
	Live []claude.Host `json:"live"`
	// FallbackWarning says why a background copy would not start for this
	// session, and is empty while the copy runs or when there is no reason.
	FallbackWarning string `json:"fallbackWarning"`
	// CanStop reports whether the page offers to stop the session's
	// background copy, and CanUnbabysit whether it offers to let the watch
	// go, which is Stop watching for a Stuck one.
	CanStop      bool `json:"canStop"`
	CanUnbabysit bool `json:"canUnbabysit"`
	// RCOn is Remote Control's state in the session's current host.
	RCOn bool `json:"remoteControl"`
	// RCHint is the exact thing to do when Remote Control is off, and is
	// empty when it is on.
	RCHint       string `json:"rcHint"`
	AttachCmd    string `json:"attachCmd,omitempty"`
	SSHAttachCmd string `json:"sshAttachCmd,omitempty"`
	// Status is what the session says about itself, busy or idle, in the
	// host it is actually running in; it is empty when it is not running.
	Status string       `json:"status"`
	Stats  claude.Stats `json:"stats"`
	Tree   TreeView     `json:"tree"`
	PID    int          `json:"pid"`
	// ProcStart is the start time the session's file recorded for PID, as
	// written there, and empty when the session runs nowhere. A command
	// running inside the session uses it to tell its own session's process
	// from a later one that reused the pid.
	ProcStart string `json:"procStart"`
	// BackgroundSince is when the watch went to the background, in RFC
	// 3339 and UTC, and empty unless it is In background. It stands in for
	// the saved time of the same name, which the page never reads.
	BackgroundSince string `json:"backgroundSince"`
	// RemoteURL opens the background copy through Remote Control, and is
	// empty when its bridge id is not one an address can be built from.
	RemoteURL string `json:"remoteUrl"`
	// ResumeCmd resumes the session in a terminal from its own folder, for
	// a person taking it back from the background by hand.
	ResumeCmd string `json:"resumeCmd"`
}

// KeepAwakeView is the state of the keep-awake request.
type KeepAwakeView struct {
	Mode      string `json:"mode"`
	Held      bool   `json:"held"`
	Supported bool   `json:"supported"`
	// ActiveWatches is how many babysat sessions are actually being kept
	// alive right now, which is the whole reason the request is held. A
	// paused watch is not one of them.
	ActiveWatches int `json:"activeWatches"`
}

// KeepAwakeFixedMode is the only keep-awake answer this program gives: the
// request is held while at least one session is babysat.
const KeepAwakeFixedMode = state.KeepAwakeWhileBabysitting

// Totals sums every live session, babysat or not.
type Totals struct {
	Sessions         int                 `json:"sessions"`
	ByHost           map[claude.Host]int `json:"byHost"`
	RemoteControlled int                 `json:"remoteControlled"`
	Babysat          int                 `json:"babysat"`
	Processes        int                 `json:"processes"`
	CPUPercent       float64             `json:"cpuPercent"`
	RSSBytes         uint64              `json:"rssBytes"`
}

// View is everything the page shows, built fresh after every change and
// never modified afterwards.
type View struct {
	Version   string        `json:"version"`
	Env       hosts.Env     `json:"env"`
	KeepAwake KeepAwakeView `json:"keepAwake"`
	Sessions  []SessionView `json:"sessions"`
	Watches   []WatchView   `json:"watches"`
	// NotRunning lists conversations from the last two weeks that are
	// running nowhere and not babysat, newest first.
	NotRunning         []PastView     `json:"notRunning"`
	Totals             Totals         `json:"totals"`
	Settings           state.Settings `json:"settings"`
	AutostartSupported bool           `json:"autostartSupported"`
	// GraceUntil is when the start-up grace ends, in RFC 3339, and empty
	// when no grace is running.
	GraceUntil string `json:"graceUntil"`
	URL        string `json:"url"`
	// TerminalLabel is the button that opens a terminal on a background
	// copy, naming what really opens, and empty where none can be opened.
	TerminalLabel string `json:"terminalLabel"`
}

// buildView renders the supervisor's current state. It is only ever called
// from the goroutine that owns that state, and the result is handed out
// without being touched again.
func (s *Supervisor) buildView() View {
	snap := s.snap
	v := View{
		Version:            buildinfo.Version,
		Env:                s.env,
		Settings:           s.st.Settings,
		Sessions:           []SessionView{},
		Watches:            []WatchView{},
		NotRunning:         []PastView{},
		AutostartSupported: s.deps.Autostart != nil,
		URL:                s.deps.URL,
		KeepAwake: KeepAwakeView{
			Mode:          KeepAwakeFixedMode,
			Held:          s.held,
			Supported:     s.deps.Power != nil && s.deps.Power.Supported(),
			ActiveWatches: activeWatches(s.st.Watches),
		},
	}

	watched := make(map[string]bool, len(s.st.Watches))
	for _, w := range s.st.Watches {
		watched[w.SessionID] = true
	}
	placed := map[string]bool{}
	for _, first := range snap.Sessions {
		if watched[first.ID] || placed[first.ID] {
			continue
		}
		placed[first.ID] = true
		v.Sessions = append(v.Sessions, s.sessionView(snap.All(first.ID)))
	}
	for _, w := range s.st.Watches {
		v.Watches = append(v.Watches, s.watchView(w))
	}
	v.NotRunning = s.pastViews(watched)
	if s.inGrace() {
		v.GraceUntil = s.graceUntil().UTC().Format(time.RFC3339)
	}
	if !s.env.Headless && s.terminal != "" {
		v.TerminalLabel = "Open in " + s.terminal
	}
	v.Totals = s.totals(snap)
	return v
}

// sessionView renders one session that is not babysat from every process
// it is live in. It is one entry however many there are: the process in an
// app rather than the background, the lowest pid among equals, is the one
// the entry is taken from, so the same processes always give the same entry.
func (s *Supervisor) sessionView(live []claude.Session) SessionView {
	ranked := append([]claude.Session{}, live...)
	sort.SliceStable(ranked, func(i, j int) bool {
		bi, bj := ranked[i].Host == claude.HostBackground, ranked[j].Host == claude.HostBackground
		if bi != bj {
			return bj
		}
		return ranked[i].PID < ranked[j].PID
	})
	sn := ranked[0]
	name, also := displayName(sn.Host, sn.Name, s.stats[sn.ID].Title)
	out := SessionView{
		ID: sn.ID, ShortID: sn.ShortID, PID: sn.PID, ProcStart: sn.ProcStart, Cwd: sn.Cwd, Name: name, AlsoCalled: also,
		Host: sn.Host, Entrypoint: sn.Entrypoint,
		Status: sn.Status, Stats: s.stats[sn.ID], Tree: treeView(s.trees[sn.PID]),
		Live:            make([]claude.Host, 0, len(ranked)),
		Actionable:      sn.Host != claude.HostOther,
		ScheduledTask:   s.stats[sn.ID].ScheduledTask,
		FallbackWarning: s.fallbackWarning(sn.Cwd, sn.Host),
		CanStop:         s.env.Headless && sn.Host == claude.HostBackground,
		AttachCmd:       s.attachCmd(sn),
		SSHAttachCmd:    s.sshAttachCmd(sn),
	}
	for _, x := range ranked {
		out.Live = append(out.Live, x.Host)
		out.RemoteControl = out.RemoteControl || x.RemoteControl
	}
	return out
}

// watchView renders one watch against the sessions that are live right now.
func (s *Supervisor) watchView(w state.Watch) WatchView {
	live := s.snap.All(w.SessionID)
	// The watch is a copy, but its failure times are not: the view must own
	// every piece of what it publishes, so that handing it out cannot let
	// anyone reach back into the state the loop is still using.
	w.Failures = append([]time.Time{}, w.Failures...)
	out := WatchView{Watch: w, State: StateOf(w, live), Live: []claude.Host{}}
	if out.State != StateInBackground {
		out.FallbackWarning = s.fallbackWarning(w.Cwd, w.OriginHost)
	}
	out.CanStop = s.canStop(&w, live)
	out.CanUnbabysit = CanUnbabysit(out.State, s.env.Headless)
	for _, sn := range live {
		out.Live = append(out.Live, sn.Host)
		if sn.Host == claude.HostBackground {
			out.AttachCmd = s.attachCmd(sn)
			out.SSHAttachCmd = s.sshAttachCmd(sn)
			out.RemoteURL = claude.RemoteURL(sn.BridgeSessionID)
		}
	}
	if out.State == StateInBackground && !w.BackgroundSince.IsZero() {
		out.BackgroundSince = w.BackgroundSince.UTC().Format(time.RFC3339)
	}
	out.ResumeCmd = hosts.ResumeCommandIn(w.Cwd, w.SessionID)

	primary, ok := primarySession(w, live)
	if ok {
		out.Host = primary.Host
		out.RCOn = primary.RemoteControl
		out.Status = primary.Status
		out.PID = primary.PID
		out.ProcStart = primary.ProcStart
		out.Tree = treeView(s.trees[primary.PID])
		if !out.RCOn {
			out.RCHint = RCHint(primary.Host, primary.ShortID)
		}
	}
	out.Stats = s.stats[w.SessionID]
	out.Name, out.AlsoCalled = displayName(w.OriginHost, w.Name, out.Stats.Title)
	return out
}

// displayName picks the name a session is shown by: the title its own app
// shows, read from the transcript, with the name from its record, when
// that differs, as the other name it goes by. The record's name is cleaned
// the same way a title is, so whitespace alone is never a second name.
// Desktop writes its sidebar title into the record and keeps it current,
// so there the record's name comes first and the title only stands in for
// an empty one.
func displayName(host claude.Host, record, title string) (name, also string) {
	record = claude.CleanTitle(record)
	if title == "" || (host == claude.HostDesktop && record != "") {
		return record, ""
	}
	if record != "" && record != title {
		return title, record
	}
	return title, ""
}

// attachCmd is the command that attaches a terminal to a background
// session, and empty for any other.
func (s *Supervisor) attachCmd(sn claude.Session) string {
	if sn.Host != claude.HostBackground {
		return ""
	}
	return hosts.AttachCommand(sn.ShortID)
}

// sshAttachCmd is the same command run from another machine over ssh,
// offered only on a machine with no display, where the person is always
// somewhere else.
func (s *Supervisor) sshAttachCmd(sn claude.Session) string {
	if sn.Host != claude.HostBackground {
		return ""
	}
	return s.sshAttachFor(sn.ShortID)
}

// sshAttachFor is the command that attaches to the background session with
// the given short id from another machine, and empty anywhere but on a
// machine with no display that knows its own ssh address.
func (s *Supervisor) sshAttachFor(short string) string {
	target := s.sshTarget()
	if !s.env.Headless || target == "" || short == "" {
		return ""
	}
	return hosts.SSHAttachCommand(target, s.env.CLIPath, short)
}

// sshTarget is the user@host a person on another machine would ssh to,
// as it is now, or empty when there is none.
func (s *Supervisor) sshTarget() string {
	if s.deps.SSHTarget == nil {
		return ""
	}
	return s.deps.SSHTarget()
}

// primarySession picks the one live session a watch is really about: our
// own background copy while the promise is being served by one, and
// otherwise whatever host the session is in.
func primarySession(w state.Watch, live []claude.Session) (claude.Session, bool) {
	if len(live) == 0 {
		return claude.Session{}, false
	}
	if w.PromiseState == "fallback" {
		for _, sn := range live {
			if sn.Host == claude.HostBackground {
				return sn, true
			}
		}
	}
	for _, sn := range live {
		if sn.Host != claude.HostBackground {
			return sn, true
		}
	}
	return live[0], true
}

// totals sums every live session. Processes counts the union of the pids in
// every session's process tree, so a process that two sessions both descend
// from is counted once; CPU and memory add up per session, skipping a
// session whose own process already sits inside another session's tree.
func (s *Supervisor) totals(snap observe.Snapshot) Totals {
	t := Totals{ByHost: map[claude.Host]int{}, Sessions: len(snap.Sessions), Babysat: len(s.st.Watches)}
	union := map[int]bool{}
	for _, sn := range snap.Sessions {
		t.ByHost[sn.Host]++
		if sn.RemoteControl {
			t.RemoteControlled++
		}
		for _, pid := range s.trees[sn.PID].PIDs {
			union[pid] = true
		}
	}
	for _, sn := range snap.Sessions {
		if nested(sn, snap, s.trees) {
			continue
		}
		tree := s.trees[sn.PID]
		t.CPUPercent += tree.CPUPercent
		t.RSSBytes += tree.RSS
	}
	t.Processes = len(union)
	return t
}

// nested reports whether this session's own process is a descendant of
// another session's process tree, in which case its resource use is
// already included in that other session's numbers.
func nested(sn claude.Session, snap observe.Snapshot, trees map[int]procs.TreeStats) bool {
	for _, other := range snap.Sessions {
		if other.PID == sn.PID {
			continue
		}
		for _, pid := range trees[other.PID].PIDs {
			if pid == sn.PID {
				return true
			}
		}
	}
	return false
}

// treeView renders a process tree for the page, never leaving a nil slice
// behind for it to guard against.
func treeView(t procs.TreeStats) TreeView {
	children := t.Children
	if children == nil {
		children = []string{}
	}
	return TreeView{
		CPUPercent:    t.CPUPercent,
		RSSBytes:      t.RSS,
		Children:      children,
		UptimeSeconds: int64(t.Uptime / time.Second),
		Processes:     t.Processes,
	}
}

// RCHint is the exact thing to do to switch Remote Control on for a
// session living in host h. Claude cannot switch it on from outside, so
// every answer here is something the person does themselves.
//
// It is exported because the page has to say the same four sentences for a
// session that is not babysat, where there is no watch to carry the hint,
// and a demonstration of the page has to say them too. One wording, in one
// place, is what keeps all three the same.
func RCHint(h claude.Host, short string) string {
	switch h {
	case claude.HostTerminal:
		return "Type /rc in that terminal session."
	case claude.HostDesktop:
		return "Switch Remote Control on in the desktop app for this session."
	case claude.HostVSCode:
		return "Switch Remote Control on in the VS Code panel for this session."
	case claude.HostBackground:
		return "Attach with `" + hosts.AttachCommand(short) + "` and type /rc."
	default:
		return ""
	}
}

// activeWatches counts the babysat sessions that are actually being kept
// alive: every watch that is Watching, In background or Starting. A Stuck
// watch, which is a paused one, is not keeping anything awake, so it counts
// neither towards the request nor towards what the page says about it.
func activeWatches(watches []state.Watch) int {
	n := 0
	for _, w := range watches {
		if !w.Paused {
			n++
		}
	}
	return n
}

// hostLabel names a host the way a sentence shown to the user would.
func hostLabel(h claude.Host) string {
	switch h {
	case claude.HostTerminal:
		return "a terminal window"
	case claude.HostBackground:
		return "the background"
	case claude.HostDesktop:
		return "the desktop app"
	case claude.HostVSCode:
		return "VS Code"
	case claude.HostNone:
		return "no host of its own"
	case claude.HostOther:
		return "another program"
	default:
		return string(h)
	}
}
