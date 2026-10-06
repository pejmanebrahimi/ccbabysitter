package web

import (
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/buildinfo"
	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/state"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
)

// Fixed ids for the demo's scripted sessions. They are valid UUIDs so
// every id-shaped check in this package (claude.ValidID, in particular)
// treats them exactly like a real session id.
const (
	demoVSCodeID     = "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
	demoDesktopID    = "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb"
	demoTerminalID   = "cccccccc-3333-4333-8333-cccccccccccc"
	demoBackgroundID = "dddddddd-4444-4444-8444-dddddddddddd"
	demoOtherID      = "eeeeeeee-5555-4555-8555-eeeeeeeeeeee"
	demoCarriedID    = "a1a1a1a1-6666-4666-8666-a1a1a1a1a1a1"
	demoStartingID   = "b2b2b2b2-7777-4777-8777-b2b2b2b2b2b2"
	demoStuckID      = "c3c3c3c3-8888-4888-8888-c3c3c3c3c3c3"
	demoScheduledID  = "c4c4c4c4-8888-4888-8888-c4c4c4c4c4c4"
	demoCodeBgID     = "d4d4d4d4-9999-4999-8999-d4d4d4d4d4d4"
	demoTermBgID     = "e5e5e5e5-aaaa-4aaa-8aaa-e5e5e5e5e5e5"
	demoTwoAppsID    = "f6f6f6f6-bbbb-4bbb-8bbb-f6f6f6f6f6f6"
	demoPanelID      = "a7a7a7a7-cccc-4ccc-8ccc-a7a7a7a7a7a7"
)

const (
	// demoAppClosesAfter is when the VS Code session's app closes, so the
	// scripted world shows a watch going from Watching to Starting.
	demoAppClosesAfter = 20
	// demoStartTakes is how many ticks a scripted background copy takes to
	// start, so Starting is on screen long enough to be read.
	demoStartTakes = 4
)

// demoItem is one session in the scripted world: running or not, watched
// or not, with everything the page needs to render it.
type demoItem struct {
	id      string
	shortID string
	name    string
	// alsoCalled is the name in the session's own record when name, the
	// one its app shows, is a different one.
	alsoCalled string
	cwd        string
	host       claude.Host
	// alsoIn is a second app the session is open in at the same time, and
	// empty for a session open in one.
	alsoIn        claude.Host
	entrypoint    string
	remoteControl bool
	// bridgeID is the Remote Control bridge id of a background copy, which
	// its address on claude.ai is built from.
	bridgeID string
	status   string
	running  bool
	warning  string

	watched      bool
	originHost   claude.Host
	originRC     bool
	hasSaved     bool
	promiseState string
	watchedSince time.Time
	// backgroundSince is when the watch went to the background.
	backgroundSince time.Time
	paused          bool
	pauseReason     string
	// startsAt is the tick at which a watch that is Starting has its
	// background copy running, or zero.
	startsAt int

	stats claude.Stats
}

// live is what the real engine would find running for this item.
func (it *demoItem) live() []claude.Session {
	if !it.running {
		return nil
	}
	out := []claude.Session{{ID: it.id, ShortID: it.shortID, Host: it.host, RemoteControl: it.remoteControl, BridgeSessionID: it.bridgeID, Name: it.name, Cwd: it.cwd}}
	if it.alsoIn != "" {
		out = append(out, claude.Session{ID: it.id, ShortID: it.shortID, Host: it.alsoIn, Name: it.name, Cwd: it.cwd})
	}
	return out
}

// liveHosts is every host the item is running in, its own first.
func (it *demoItem) liveHosts() []claude.Host {
	out := []claude.Host{}
	for _, sn := range it.live() {
		out = append(out, sn.Host)
	}
	return out
}

// watch is the item as the real engine would have stored it.
func (it *demoItem) watch() state.Watch {
	return state.Watch{
		SessionID: it.id, ShortID: it.shortID, Name: it.name, Cwd: it.cwd,
		OriginHost: it.originHost, OriginRemoteControl: it.originRC, HasSavedOptions: it.hasSaved,
		WatchedSince: it.watchedSince, Paused: it.paused, PauseReason: it.pauseReason,
		Failures: []time.Time{}, PromiseState: it.promiseState, BackgroundSince: it.backgroundSince,
	}
}

// DemoEngine is a scripted, in-memory Engine that touches nothing real: no
// process is started or stopped, no file is read or written outside this
// struct. It exists so the page can be built and demonstrated without a
// live Claude Code session. It shows every watch state and the Not running
// list, and its actions change its own model exactly as the real engine
// would, with the same messages a person would see for real.
type DemoEngine struct {
	log *state.Log

	mu       sync.Mutex
	items    map[string]*demoItem
	order    []string
	past     []supervise.PastView
	settings state.Settings
	notify   func()
	elapsed  int

	tickCh <-chan time.Time
	ackCh  chan struct{}
	stopCh chan struct{}
	once   sync.Once
}

// NewDemoEngine returns a demo engine ticking once a second on its own
// goroutine. The activity log is the caller's: the demo writes the same
// kind of entries the real supervisor would, so the page's activity feed
// works identically against either engine.
func NewDemoEngine(log *state.Log) Engine {
	return newDemoEngine(log, time.NewTicker(time.Second).C)
}

// newDemoEngine takes the tick channel as a parameter so tests can drive
// the scripted timeline deterministically, one simulated second per value
// sent, instead of waiting on the wall clock.
func newDemoEngine(log *state.Log, tickCh <-chan time.Time) *DemoEngine {
	d := &DemoEngine{
		log:      log,
		items:    map[string]*demoItem{},
		settings: state.DefaultSettings(),
		tickCh:   tickCh,
		ackCh:    make(chan struct{}, 1),
		stopCh:   make(chan struct{}),
	}
	d.seed(time.Now())
	go d.run()
	return d
}

// SetNotify registers the callback the demo engine calls after every
// change, exactly as Deps.Notify is called on the real supervisor. It is
// not part of the Engine interface because nothing but the wiring code
// that constructed this engine needs to call it.
func (d *DemoEngine) SetNotify(fn func()) {
	d.mu.Lock()
	d.notify = fn
	d.mu.Unlock()
}

// Close ends the ticking goroutine. Tests call it during cleanup; a program
// that runs a demo engine for its whole lifetime has no need to.
func (d *DemoEngine) Close() {
	d.once.Do(func() { close(d.stopCh) })
}

func (d *DemoEngine) run() {
	for {
		select {
		case <-d.tickCh:
			d.tick()
			select {
			case d.ackCh <- struct{}{}:
			default:
			}
		case <-d.stopCh:
			return
		}
	}
}

// waitTick blocks until the goroutine has processed one tick sent on the
// injected tick channel. Tests use it to advance the scripted timeline
// deterministically instead of sleeping and hoping.
func (d *DemoEngine) waitTick() {
	<-d.ackCh
}

// seed sets up the scripted world's starting point: one watch in each of
// the four states, with an In background one for each app a session can
// come from, seven sessions that are not babysat, one of them open in two
// apps at once, one owned by another program and one a scheduled task's
// run, and four conversations
// that are not running, one of them handed back to VS Code and one a
// background session whose copy was stopped. The In
// background ones went there today, yesterday and four days ago.
func (d *DemoEngine) seed(now time.Time) {
	y, m, day := now.Date()
	midnight := time.Date(y, m, day, 0, 0, 0, 0, now.Location())
	today := now.Add(-2 * time.Hour)
	if today.Before(midnight) {
		today = midnight
	}
	yesterday := midnight.AddDate(0, 0, -1).Add(21*time.Hour + 30*time.Minute)
	items := []*demoItem{
		{
			id: demoVSCodeID, name: "shop-api", cwd: "~/projects/shop-api",
			host: claude.HostVSCode, entrypoint: "claude-vscode", remoteControl: true, status: "busy", running: true,
			watched: true, originHost: claude.HostVSCode, originRC: true, promiseState: "inplace", watchedSince: now.Add(-40 * time.Minute),
			stats: claude.Stats{Model: "claude-sonnet-5", Turns: 23, InputTokens: 42_000, OutputTokens: 6_100, CacheReadTokens: 180_000},
		},
		{
			id: demoCarriedID, name: "api-gateway", cwd: "~/projects/api-gateway",
			host: claude.HostBackground, entrypoint: "cli", remoteControl: true, bridgeID: "session_01DEMOGATEWAY", status: "idle", running: true,
			watched: true, originHost: claude.HostDesktop, originRC: true, hasSaved: true, promiseState: "fallback", watchedSince: now.Add(-3 * time.Hour),
			backgroundSince: today,
			stats:           claude.Stats{Model: "claude-opus-5", Turns: 41, InputTokens: 96_000, OutputTokens: 12_400},
		},
		{
			id: demoCodeBgID, name: "ui-kit", cwd: "~/projects/ui-kit",
			host: claude.HostBackground, entrypoint: "cli", status: "idle", running: true,
			watched: true, originHost: claude.HostVSCode, hasSaved: true, promiseState: "fallback", watchedSince: now.AddDate(0, 0, -2),
			backgroundSince: yesterday,
			stats:           claude.Stats{Model: "claude-sonnet-5", Turns: 17, InputTokens: 31_000, OutputTokens: 4_900},
		},
		{
			id: demoTermBgID, name: "log-digest", cwd: "~/projects/log-digest",
			host: claude.HostBackground, entrypoint: "cli", remoteControl: true, bridgeID: "cse_01DEMOLOGDIGEST", status: "busy", running: true,
			watched: true, originHost: claude.HostTerminal, originRC: true, hasSaved: true, promiseState: "fallback", watchedSince: now.AddDate(0, 0, -5),
			backgroundSince: now.AddDate(0, 0, -4),
			stats:           claude.Stats{Model: "claude-opus-5", Turns: 112, InputTokens: 410_000, OutputTokens: 58_000, CacheReadTokens: 2_100_000},
		},
		{
			id: demoStartingID, name: "report-gen", cwd: "~/projects/report-gen",
			host: claude.HostBackground, entrypoint: "cli", remoteControl: true, status: "idle",
			watched: true, originHost: claude.HostTerminal, originRC: true, hasSaved: true, promiseState: "fallback", watchedSince: now.Add(-20 * time.Minute),
			startsAt: demoStartTakes,
			stats:    claude.Stats{Model: "claude-sonnet-5", Turns: 9, InputTokens: 15_000, OutputTokens: 2_300},
		},
		{
			id: demoStuckID, name: "data-sync", cwd: "~/projects/data-sync",
			host: claude.HostTerminal, entrypoint: "cli", status: "idle", warning: supervise.UntrustedWarning("~/projects/data-sync"),
			watched: true, originHost: claude.HostTerminal, promiseState: "paused", watchedSince: now.Add(-90 * time.Minute),
			paused: true, pauseReason: "three failed resumes in five minutes: Resume did not start: Workspace not trusted",
			stats: claude.Stats{Model: "claude-haiku-4-5", Turns: 4, InputTokens: 5_200, OutputTokens: 800},
		},
		{
			// A scheduled task's run, which is listed apart and never babysat.
			id: demoScheduledID, name: "Daily report", cwd: "~/notes",
			host: claude.HostDesktop, entrypoint: "claude-desktop", status: "busy", running: true,
			stats: claude.Stats{Model: "claude-sonnet-5", Turns: 1, InputTokens: 2_100, OutputTokens: 400, ScheduledTask: true},
		},
		{
			id: demoDesktopID, name: "docs-site", cwd: "~/projects/docs-site",
			host: claude.HostDesktop, entrypoint: "claude-desktop", remoteControl: true, status: "idle", running: true,
			stats: claude.Stats{Model: "claude-sonnet-5", Turns: 6, InputTokens: 8_400, OutputTokens: 1_200},
		},
		{
			id: demoTerminalID, name: "infra-cli", cwd: "~",
			host: claude.HostTerminal, entrypoint: "cli", status: "idle", running: true, warning: supervise.FallbackHome,
			stats: claude.Stats{Model: "claude-haiku-4-5", Turns: 2, InputTokens: 3_000, OutputTokens: 500},
		},
		{
			id: demoTwoAppsID, name: "web-app", cwd: "~/projects/web-app",
			host: claude.HostTerminal, alsoIn: claude.HostVSCode, entrypoint: "cli", remoteControl: true, status: "busy", running: true,
			stats: claude.Stats{Model: "claude-sonnet-5", Turns: 14, InputTokens: 27_000, OutputTokens: 3_800},
		},
		{
			id: demoPanelID, name: "Fix flaky login test", alsoCalled: "web-portal-2-94", cwd: "~/projects/web-portal",
			host: claude.HostVSCode, entrypoint: "claude-vscode", remoteControl: true, status: "idle", running: true,
			stats: claude.Stats{Model: "claude-sonnet-5", Turns: 11, InputTokens: 19_000, OutputTokens: 2_700},
		},
		{
			id: demoBackgroundID, name: "batch-jobs", cwd: "~/projects/batch-jobs",
			host: claude.HostBackground, entrypoint: "cli", remoteControl: true, status: "busy", running: true,
			stats: claude.Stats{Model: "claude-opus-5", Turns: 68, InputTokens: 120_000, OutputTokens: 24_000, CacheWriteTokens: 50_000},
		},
		{
			id: demoOtherID, name: "notebook", cwd: "~/projects/notebook",
			host: claude.HostOther, entrypoint: "third-party-tool", status: "idle", running: true,
			stats: claude.Stats{Model: "claude-sonnet-5", Turns: 1, InputTokens: 900, OutputTokens: 150},
		},
	}
	for _, it := range items {
		it.shortID = demoShortID(it.id)
		d.items[it.id] = it
		d.order = append(d.order, it.id)
	}
	for i, p := range []struct {
		name, cwd string
		handed    claude.Host
		stopped   bool
	}{
		{"landing-page", "~/projects/landing-page", claude.HostVSCode, false},
		{"nightly-etl", "~/projects/etl", "", true},
		{"", "~/scratch", "", false},
		{"billing-fix", "~/projects/billing", "", false},
	} {
		id := demoNewID(900 + i)
		pv := supervise.PastView{
			ID: id, ShortID: demoShortID(id), Name: p.name, Cwd: p.cwd,
			LastActivity: now.Add(-time.Duration(2+i*30) * time.Hour), ResumeCmd: hosts.ResumeCommandIn(p.cwd, id),
			HandedBackTo: p.handed,
		}
		// A background session whose copy was stopped from the page is
		// started again with its attach command, as the real engine says.
		if p.stopped {
			pv.AttachCmd = hosts.AttachCommand(pv.ShortID)
		}
		d.past = append(d.past, pv)
	}
}

// tick advances the scripted world by one simulated second: token counts
// drift a little so the page visibly updates, the VS Code session's app
// closes at demoAppClosesAfter, and every watch that is Starting has its
// background copy running once its start has taken its time.
func (d *DemoEngine) tick() {
	d.mu.Lock()
	d.elapsed++
	for _, it := range d.items {
		if !it.running {
			continue
		}
		it.stats.InputTokens += 137
		it.stats.OutputTokens += 41
		it.stats.CacheReadTokens += 250
		if d.elapsed%12 == 0 {
			it.stats.Turns++
		}
	}
	type said struct{ label, short string }
	var started []said
	if d.elapsed == demoAppClosesAfter {
		if it, ok := d.items[demoVSCodeID]; ok && it.watched && it.running && it.host == claude.HostVSCode {
			it.running = false
			it.startsAt = d.elapsed + demoStartTakes
		}
	}
	for _, id := range d.order {
		it := d.items[id]
		if it.startsAt == 0 || it.startsAt > d.elapsed || it.paused {
			continue
		}
		it.startsAt = 0
		it.running = true
		it.host = claude.HostBackground
		it.entrypoint = "cli"
		it.remoteControl = true
		it.bridgeID = "session_01DEMO" + it.shortID
		it.hasSaved = true
		if it.originHost != claude.HostBackground {
			it.promiseState = "fallback"
			if it.backgroundSince.IsZero() {
				it.backgroundSince = time.Now()
			}
		}
		started = append(started, said{demoLabel(it), it.shortID})
	}
	notify := d.notify
	d.mu.Unlock()

	for _, s := range started {
		d.log.Auto(s.label, "host process exited", "resumed as background "+s.short+". Remote Control is on.")
	}
	if notify != nil {
		notify()
	}
}

// View renders the current scripted state exactly as the real supervisor
// renders its own: no nil slices, camelCase field names carried by the
// same types the page already knows how to read.
func (d *DemoEngine) View() supervise.View {
	d.mu.Lock()
	defer d.mu.Unlock()

	v := supervise.View{
		Version: buildinfo.Version,
		Env: hosts.Env{
			Platform: "demo", CLIFound: true, CLIPresent: true, CLIVersion: "2.1.280", CLILoggedIn: hosts.LoginYes,
			DesktopInstalled: true, DesktopRunning: true, DesktopVersion: "2.7032.0",
			VSCodeInstalled: true, VSCodeExtVersion: "2.1.280",
		},
		Settings:           d.settings,
		Sessions:           []supervise.SessionView{},
		Watches:            []supervise.WatchView{},
		NotRunning:         append([]supervise.PastView{}, d.past...),
		AutostartSupported: true,
		KeepAwake: supervise.KeepAwakeView{
			Mode: supervise.KeepAwakeFixedMode, Held: d.activeWatches() > 0,
			Supported: true, ActiveWatches: d.activeWatches(),
		},
		URL:           "http://127.0.0.1:47391",
		TerminalLabel: "Open in Terminal",
	}

	byHost := map[claude.Host]int{}
	running, babysat, rc := 0, 0, 0
	for _, id := range d.order {
		it := d.items[id]
		if it.watched {
			babysat++
			v.Watches = append(v.Watches, d.watchView(it))
		}
		if !it.running {
			continue
		}
		for _, sn := range it.live() {
			running++
			byHost[sn.Host]++
			if sn.RemoteControl {
				rc++
			}
		}
		if !it.watched {
			sv := supervise.SessionView{
				ID: it.id, ShortID: it.shortID, PID: demoPID(it), ProcStart: demoProcStart, Cwd: it.cwd, Name: it.name, AlsoCalled: it.alsoCalled,
				Host: it.host, Entrypoint: it.entrypoint, RemoteControl: it.remoteControl,
				Status: it.status, Stats: it.stats, Tree: demoTree(), Actionable: it.host != claude.HostOther,
				FallbackWarning: it.warning, Live: it.liveHosts(), ScheduledTask: it.stats.ScheduledTask,
			}
			if it.host == claude.HostBackground {
				sv.AttachCmd = hosts.AttachCommand(it.shortID)
			}
			v.Sessions = append(v.Sessions, sv)
		}
	}
	v.Totals = supervise.Totals{
		Sessions: running, ByHost: byHost, RemoteControlled: rc, Babysat: babysat,
		Processes: running, CPUPercent: float64(running) * 1.5, RSSBytes: uint64(running) * 90 << 20,
	}
	return v
}

// watchView renders one scripted watch the way the real engine renders a
// real one, its state read off the same rule. The caller holds the lock.
func (d *DemoEngine) watchView(it *demoItem) supervise.WatchView {
	w := it.watch()
	live := it.live()
	wv := supervise.WatchView{
		Watch: w, State: supervise.StateOf(w, live), Live: []claude.Host{}, AlsoCalled: it.alsoCalled,
		Stats: it.stats, Tree: supervise.TreeView{Children: []string{}},
	}
	if it.running {
		wv.Host = it.host
		wv.Live = it.liveHosts()
		wv.RCOn = it.remoteControl
		wv.Status = it.status
		wv.Tree = demoTree()
		wv.PID = demoPID(it)
		wv.ProcStart = demoProcStart
		if !wv.RCOn {
			// The same sentence the real engine would give for this host,
			// from the same place, so a demonstration of the page can never
			// show advice the program itself would not give.
			wv.RCHint = supervise.RCHint(it.host, it.shortID)
		}
		if it.host == claude.HostBackground {
			wv.AttachCmd = hosts.AttachCommand(it.shortID)
			wv.RemoteURL = claude.RemoteURL(it.bridgeID)
		}
	}
	if wv.State != supervise.StateInBackground {
		wv.FallbackWarning = it.warning
	} else if !it.backgroundSince.IsZero() {
		wv.BackgroundSince = it.backgroundSince.UTC().Format(time.RFC3339)
	}
	wv.ResumeCmd = hosts.ResumeCommandIn(it.cwd, it.id)
	wv.CanStop = wv.State == supervise.StateInBackground
	wv.CanUnbabysit = supervise.CanUnbabysit(wv.State, false)
	return wv
}

// activeWatches counts the scripted babysat sessions that are keeping the
// computer awake, the same way the real engine counts them. The caller
// holds the lock.
func (d *DemoEngine) activeWatches() int {
	n := 0
	for _, it := range d.items {
		if it.watched && !it.paused {
			n++
		}
	}
	return n
}

func demoTree() supervise.TreeView {
	return supervise.TreeView{CPUPercent: 1.5, RSSBytes: 90 << 20, Children: []string{}, UptimeSeconds: 300, Processes: 1}
}

// demoProcStart is the start time every scripted process claims, in Unix
// milliseconds, the way a session file on Linux and Windows records it.
const demoProcStart = "1790000000000"

func demoPID(it *demoItem) int {
	return 10000 + int(it.shortID[0])
}

// demoLabel names a session the way a sentence shown to the user would:
// its name if it has one, its short id otherwise.
func demoLabel(it *demoItem) string {
	if it.name != "" {
		return it.name
	}
	return it.shortID
}

func demoHostLabel(h claude.Host) string {
	switch h {
	case claude.HostTerminal:
		return "a terminal window"
	case claude.HostBackground:
		return "the background"
	case claude.HostDesktop:
		return "the desktop app"
	case claude.HostVSCode:
		return "VS Code"
	default:
		return "another program"
	}
}

// changed tells whoever asked to be told that the scripted world moved on.
func (d *DemoEngine) changed(notify func()) {
	if notify != nil {
		notify()
	}
}

// Babysit starts watching a running, actionable session where it already
// is, and switches start at login on when asked to, as the real engine does.
func (d *DemoEngine) Babysit(id string, startAtLogin bool, via supervise.Via) supervise.Result {
	d.mu.Lock()
	it, ok := d.items[id]
	if !ok || !it.running {
		d.mu.Unlock()
		return supervise.Result{Message: "This session is not running, so it cannot be babysat."}
	}
	if it.host == claude.HostOther {
		d.mu.Unlock()
		return supervise.Result{Message: "This session belongs to another program and cannot be babysat."}
	}
	label := demoLabel(it)
	if it.stats.ScheduledTask {
		d.mu.Unlock()
		return supervise.Result{Message: label + " is a scheduled task run. " + supervise.ScheduledRunReason}
	}
	if it.watched {
		d.mu.Unlock()
		return supervise.Result{OK: true, Message: label + " is already being babysat."}
	}
	it.watched = true
	it.originHost = it.host
	it.originRC = it.remoteControl
	it.hasSaved = it.host == claude.HostBackground
	it.promiseState = "inplace"
	it.watchedSince = time.Now()
	hostLbl := demoHostLabel(it.host)
	msg := "Babysitting " + label + " in " + hostLbl + ". "
	if it.remoteControl {
		msg += "Remote Control is on."
	} else {
		msg += "Remote Control is off, so your other devices can't reach it yet. " + supervise.RCHint(it.host, it.shortID)
	}
	if it.warning != "" {
		msg += " " + it.warning
	}
	if startAtLogin && !d.settings.Autostart {
		d.settings.Autostart = true
		msg += " Start at login is on."
	}
	short := it.shortID
	notify := d.notify
	d.mu.Unlock()

	d.log.Info(label, "babysitting in "+hostLbl+via.Suffix())
	d.changed(notify)
	return supervise.Result{OK: true, Message: msg, ShortID: short}
}

// Unbabysit stops watching a session and leaves it where it is. As with
// the real engine, it is refused once the background copy carries the
// session or is about to.
func (d *DemoEngine) Unbabysit(id string, via supervise.Via) supervise.Result {
	d.mu.Lock()
	it, ok := d.items[id]
	if !ok || !it.watched {
		d.mu.Unlock()
		return supervise.Result{Message: "That session is not being babysat."}
	}
	label := demoLabel(it)
	if st := supervise.StateOf(it.watch(), it.live()); !supervise.CanUnbabysit(st, false) {
		d.mu.Unlock()
		if st == supervise.StateStarting {
			return supervise.Result{Message: label + " is being started in the background. Wait for it, then stop the background copy."}
		}
		return supervise.Result{Message: label + " is running in the background. Stop the background copy instead."}
	}
	it.watched = false
	it.promiseState = ""
	it.paused = false
	it.pauseReason = ""
	// The message is built while the lock is held: once it is let go the
	// ticking world may change this session at any moment.
	msg := "Stopped babysitting " + label + ". It is not running anywhere right now."
	if it.running {
		msg = "Stopped babysitting " + label + ". It is now in " + demoHostLabel(it.host) + "."
	} else {
		d.forget(it)
	}
	notify := d.notify
	d.mu.Unlock()

	d.log.Info(label, "no longer babysitting"+via.Suffix())
	d.changed(notify)
	return supervise.Result{OK: true, Message: msg}
}

// Stop stops a babysat session's background copy and removes the watch,
// the way the real engine does, and puts it on the Not running list.
func (d *DemoEngine) Stop(id string, via supervise.Via) supervise.Result {
	d.mu.Lock()
	it, ok := d.items[id]
	if !ok || !it.running || it.host != claude.HostBackground {
		d.mu.Unlock()
		return supervise.Result{Message: "That session has no background copy running."}
	}
	if !it.watched || supervise.StateOf(it.watch(), it.live()) != supervise.StateInBackground {
		d.mu.Unlock()
		return supervise.Result{Message: supervise.StopRefused}
	}
	label := demoLabel(it)
	origin := it.originHost
	short := it.shortID
	d.forget(it)
	d.past[0].HandedBackTo = demoHandBackTo(origin)
	notify := d.notify
	d.mu.Unlock()

	d.log.Info(label, "stopped background copy "+short+via.Suffix())
	d.changed(notify)
	return supervise.Result{OK: true, Message: supervise.StopDone, ShortID: short}
}

// OpenTerminal answers as the real engine does on a Mac for a session with
// a background copy running, and opens nothing.
func (d *DemoEngine) OpenTerminal(id string) supervise.Result {
	d.mu.Lock()
	it, ok := d.items[id]
	if !ok || !it.running || it.host != claude.HostBackground {
		d.mu.Unlock()
		return supervise.Result{Message: "That session has no background copy running."}
	}
	label, short := demoLabel(it), it.shortID
	d.mu.Unlock()

	d.log.Info(label, "opened Terminal attached to background copy "+short)
	return supervise.Result{OK: true, Message: "Opened Terminal on " + label + ".", ShortID: short}
}

// forget takes an item out of the running world and puts it at the top of
// the Not running list. The caller holds the lock.
func (d *DemoEngine) forget(it *demoItem) {
	delete(d.items, it.id)
	kept := d.order[:0]
	for _, id := range d.order {
		if id != it.id {
			kept = append(kept, id)
		}
	}
	d.order = kept
	d.past = append([]supervise.PastView{{
		ID: it.id, ShortID: it.shortID, Name: it.name, Cwd: it.cwd,
		LastActivity: time.Now(), ResumeCmd: hosts.ResumeCommandIn(it.cwd, it.id),
	}}, d.past...)
}

// demoHandBackTo names the app a stopped session goes back to, as the
// real engine names it.
func demoHandBackTo(origin claude.Host) claude.Host {
	switch origin {
	case claude.HostDesktop, claude.HostVSCode:
		return origin
	default:
		return claude.HostTerminal
	}
}

// ResumeWatch clears a pause; the scripted copy then starts after a moment.
func (d *DemoEngine) ResumeWatch(id string, via supervise.Via) supervise.Result {
	d.mu.Lock()
	it, ok := d.items[id]
	if !ok || !it.watched {
		d.mu.Unlock()
		return supervise.Result{Message: "That session is not being babysat."}
	}
	it.paused = false
	it.pauseReason = ""
	if it.promiseState == "paused" {
		it.promiseState = "inplace"
	}
	if !it.running {
		it.startsAt = d.elapsed + demoStartTakes
	}
	label := demoLabel(it)
	notify := d.notify
	d.mu.Unlock()

	d.log.Info(label, "babysitting resumed"+via.Suffix())
	d.changed(notify)
	return supervise.Result{OK: true, Message: "Babysitting " + label + " again."}
}

// SetSettings stores the user's preferences, validating the same fields
// the real engine validates.
func (d *DemoEngine) SetSettings(next state.Settings, _ supervise.Via) supervise.Result {
	// The keep-awake rule is not a preference here either.
	next.KeepAwakeMode = state.KeepAwakeWhileBabysitting
	if !state.ValidTheme(next.Theme) {
		return supervise.Result{Message: "Theme must be dark, light or auto."}
	}
	d.mu.Lock()
	d.settings = next
	notify := d.notify
	d.mu.Unlock()

	d.changed(notify)
	return supervise.Result{OK: true, Message: "Settings saved."}
}

// demoNewID turns a small sequence number into a valid UUID distinct from
// every seed id, so a scripted conversation is just as real to
// claude.ValidID as the scripted sessions.
func demoNewID(seq int) string {
	return fmt.Sprintf("ffffffff-6666-4666-8666-%012d", seq)
}

// demoShortID makes the eight hex characters a session is known by out of
// its id, so an attach command in the demo reads exactly like one a person
// would type for a real session rather than announcing itself as a demo.
// It is a hash rather than the id's own first characters because the
// scripted ids are deliberately repetitive.
func demoShortID(id string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return fmt.Sprintf("%08x", h.Sum32())
}
