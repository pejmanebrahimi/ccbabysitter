package supervise

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/buildinfo"
	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/power"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// statsInterval is how often a single session's transcript and process
// tree are re-read. Reading either costs real work, and neither changes
// fast enough to be worth reading on every snapshot.
const statsInterval = 5 * time.Second

// phase is how far the supervisor's own goroutine has got.
type phase int

const (
	// phaseNew means Run has not been called yet: an action runs inline so
	// wiring code and tests can use the supervisor before it is started.
	phaseNew phase = iota
	// phaseRunning means the loop owns the state and actions are posted to
	// it.
	phaseRunning
	// phaseStopped means the loop has exited and nothing more will be done.
	phaseStopped
)

// statsQueueDepth is how many transcript reads can be waiting for the
// stats worker at once. The loop never waits to add one: a queue this deep
// already means the worker is behind, and the sessions it has not reached
// will be asked for again on the next pass anyway.
const statsQueueDepth = 32

// shuttingDown is the honest answer to an action that arrives after the
// loop has gone.
const shuttingDown = "CC Babysitter is shutting down, so nothing was changed."

// statsRequest asks the stats worker to read one session's transcript, or,
// when forget is set, to drop what it remembers about a session that is
// gone, or, when past is set, to list the conversations written to since
// that are not in skip, or, when trust is set, to read the CLI's trust
// flags as they stand at.
type statsRequest struct {
	id     string
	path   string
	forget bool

	past  bool
	since time.Time
	skip  map[string]bool

	trust bool
	at    time.Time
}

// statsResult is one finished transcript read, one finished list of past
// conversations, or one reading of the CLI's trust flags, on its way back
// to the loop.
type statsResult struct {
	id    string
	stats claude.Stats
	ok    bool

	isPast bool
	past   []claude.PastSession

	isTrust bool
	trust   claude.Trust
}

// request is one action waiting to run on the loop goroutine.
type request struct {
	fn    func(ctx context.Context) Result
	reply chan Result
}

// Supervisor keeps every babysat session where it was promised to be. One
// goroutine owns all the mutable state below: reconcile passes and user
// actions take turns on it, so neither can ever see the other half done.
// The only thing published outside that goroutine is an immutable View,
// which callers read without waiting for anything.
type Supervisor struct {
	deps Deps

	reqs chan request
	done chan struct{}
	view atomic.Pointer[View]

	// The stats worker's two channels. Reading a long transcript for the
	// first time is the one thing a pass does that can take noticeably
	// longer than the pass itself, so it happens on a goroutine of its own:
	// the loop posts a request without waiting and picks the answer up
	// whenever it arrives. Everything else about a session, its process
	// tree included, stays on the loop, where it is cheap.
	statsReqs    chan statsRequest
	statsResults chan statsResult

	// mu guards phase, and is held for the whole of an action that runs
	// inline before Run has started, so such an action can never overlap
	// the loop.
	mu    sync.Mutex
	phase phase

	// Everything below belongs to the loop goroutine (or to an inline
	// action holding mu).
	st   state.State
	snap observe.Snapshot
	env  hosts.Env
	// terminal is what Open in a terminal would open, read alongside env.
	terminal     string
	absent       map[string]int
	backoffUntil map[string]time.Time
	seen         map[string]bool
	stats        map[string]claude.Stats
	statsAt      map[string]time.Time
	// statsInFlight names the sessions the worker has been asked about and
	// has not answered for yet, so the same read is never queued twice.
	statsInFlight map[string]bool
	// statsForget holds sessions whose reader should be dropped but whose
	// request did not fit in the queue, to be posted on a later pass.
	statsForget []string
	trees       map[int]procs.TreeStats
	treesAt     map[int]time.Time
	// past is the newest list of conversations that were running recently,
	// pastAt when it was asked for, pastLive the live sessions it was asked
	// for against, and pastInFlight whether an answer is still to come.
	past         []claude.PastSession
	pastAt       time.Time
	pastLive     string
	pastInFlight bool
	// pastWanted says a new list was wanted while one was being read.
	pastWanted bool
	// trust is the newest reading of whether the CLI trusts a folder, for
	// the warning that a background copy will not start there. The stats
	// worker reads the file; the loop only ever looks at this value.
	// trustAt is when it was last asked for, and trustInFlight whether an
	// answer is still to come.
	trust         claude.Trust
	trustAt       time.Time
	trustInFlight bool
	lastCounted   time.Time
	// autostartAt is when the login item was last looked at.
	autostartAt time.Time
	firstPass   bool
	// startedAt is when this supervisor was built, which is when the
	// start-up grace begins.
	startedAt time.Time
	// trouble is what the loop remembers about background copies it could
	// not deal with.
	trouble *copyTrouble
	// handedBack is every session handed back from the background to its
	// app in the last day, by session id. It is only ever kept in memory.
	handedBack map[string]handBack
	// stopped is every background session whose copy Stop ended without
	// handing it back to an app, by session id, so its Not running row can
	// offer the command that starts it again. It is only kept in memory.
	stopped map[string]stoppedCopy
	release func()
	held    bool
	// heldFor is how many babysat sessions the request was last said to be
	// held for, so a change in that number is said as well.
	heldFor int
	// The two keep-awake warnings are separate: a platform that cannot hold
	// the request at all and a platform that refused this particular
	// request are different problems, and neither should silence the other.
	unsupportedWarned bool
	refusedWarned     bool
}

// New builds a supervisor from saved state. It publishes a view straight
// away, so a caller can read one before Run is ever started.
func New(d Deps) *Supervisor {
	d.defaults()
	s := &Supervisor{
		deps:          d,
		reqs:          make(chan request, 16),
		done:          make(chan struct{}),
		statsReqs:     make(chan statsRequest, statsQueueDepth),
		statsResults:  make(chan statsResult, statsQueueDepth),
		absent:        map[string]int{},
		backoffUntil:  map[string]time.Time{},
		seen:          map[string]bool{},
		stats:         map[string]claude.Stats{},
		statsAt:       map[string]time.Time{},
		statsInFlight: map[string]bool{},
		trees:         map[int]procs.TreeStats{},
		treesAt:       map[int]time.Time{},
		trouble:       newCopyTrouble(),
		handedBack:    map[string]handBack{},
		stopped:       map[string]stoppedCopy{},
		startedAt:     d.Now(),
	}
	st, report, err := d.Store.LoadReport()
	if err != nil {
		s.logError("", "could not read saved state, starting from defaults: "+err.Error())
		st = state.State{Version: buildinfo.Version, Settings: state.DefaultSettings()}
	}
	if report.Healed {
		// Starting from nothing looks exactly like every babysat session
		// having been forgotten on purpose, so it is never done quietly.
		// The reason is already the whole sentence, and which of the two
		// reasons it was is the first thing it says; wrapping it in another
		// sentence would either repeat it or put the wrong one in front.
		s.logError("", report.Reason)
	}
	s.st = st
	if s.deps.Obs != nil {
		s.snap = s.deps.Obs.Current()
	}
	s.env = s.readEnv()
	s.terminal = s.readTerminal()
	s.checkAutostart()
	// The initial view is published without telling anyone: a notifier that
	// captures this supervisor would otherwise run before New has returned
	// it.
	s.store()
	return s
}

// Run owns the supervisor's state until ctx is canceled: it reconciles
// every snapshot the observer publishes and runs every action posted to
// it, one at a time. The keep-awake request is released on the way out.
func (s *Supervisor) Run(ctx context.Context) {
	s.mu.Lock()
	if s.phase != phaseNew {
		s.mu.Unlock()
		return
	}
	s.phase = phaseRunning
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.phase = phaseStopped
		s.mu.Unlock()
		s.dropKeepAwake("no longer keeping the computer awake, " + buildinfo.Name + " is stopping")
		s.publish()
		close(s.done)
	}()

	if ctx.Err() != nil {
		return
	}

	var worker sync.WaitGroup
	worker.Add(1)
	go func() {
		defer worker.Done()
		s.runStatsWorker(ctx)
	}()
	defer worker.Wait()

	s.reconcile(ctx, s.deps.Obs.Current())
	s.after()
	for {
		select {
		case snap := <-s.deps.Obs.Swept:
			s.reconcile(ctx, snap)
			s.after()
		case snap := <-s.deps.Obs.Changed:
			s.reconcile(ctx, snap)
			s.after()
		case res := <-s.statsResults:
			s.applyStats(res)
			s.after()
		case r := <-s.reqs:
			res := r.fn(ctx)
			s.after()
			r.reply <- res
		case <-ctx.Done():
			return
		}
	}
}

// runStatsWorker reads transcripts away from the loop. It owns every
// StatsReader this program has: nothing else may touch one, which is what
// makes reading a very long transcript for the first time cost the loop
// nothing at all. It stops with ctx.
func (s *Supervisor) runStatsWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-s.statsReqs:
			if req.forget {
				s.deps.forgetStats(req.id)
				continue
			}
			if req.trust {
				tr := s.deps.readTrust(req.at)
				select {
				case s.statsResults <- statsResult{isTrust: true, trust: tr}:
				case <-ctx.Done():
					return
				}
				continue
			}
			if req.past {
				list := s.deps.listPast(req.since, req.skip)
				select {
				case s.statsResults <- statsResult{isPast: true, past: list}:
				case <-ctx.Done():
					return
				}
				continue
			}
			st, err := s.deps.readStats(req.id, req.path)
			select {
			case s.statsResults <- statsResult{id: req.id, stats: st, ok: err == nil}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// applyStats folds one finished transcript read back into the state the
// loop owns. A session forgotten while its read was in flight is dropped
// rather than resurrected.
func (s *Supervisor) applyStats(res statsResult) {
	if res.isTrust {
		s.trustInFlight = false
		s.trust = res.trust
		return
	}
	if res.isPast {
		s.pastInFlight = false
		s.past = res.past
		if s.pastWanted {
			s.pastWanted = false
			s.pastAt = time.Time{}
			s.askForPast(s.snap)
		}
		return
	}
	delete(s.statsInFlight, res.id)
	if _, wanted := s.statsAt[res.id]; !wanted {
		return
	}
	if res.ok {
		s.stats[res.id] = res.stats
	}
}

// postStats hands the worker a request without ever waiting for it, and
// reports whether it fit. A request that did not fit is simply made again
// on a later pass.
func (s *Supervisor) postStats(req statsRequest) bool {
	select {
	case s.statsReqs <- req:
		return true
	default:
		return false
	}
}

// ask runs fn where the supervisor's state is owned: on the loop goroutine
// once Run has started, or inline under mu before that. It must never be
// called from the loop goroutine itself, which is why everything inside
// the package calls the unexported action bodies directly.
func (s *Supervisor) ask(fn func(ctx context.Context) Result) Result {
	s.mu.Lock()
	switch s.phase {
	case phaseNew:
		defer s.mu.Unlock()
		res := fn(context.Background())
		s.after()
		return res
	case phaseStopped:
		s.mu.Unlock()
		return Result{Message: shuttingDown}
	}
	s.mu.Unlock()

	r := request{fn: fn, reply: make(chan Result, 1)}
	select {
	case s.reqs <- r:
	case <-s.done:
		return Result{Message: shuttingDown}
	}
	select {
	case res := <-r.reply:
		return res
	case <-s.done:
		// The loop may have answered and exited before this select ran, so
		// look once more before reporting a failure that did not happen.
		select {
		case res := <-r.reply:
			return res
		default:
			return Result{Message: shuttingDown}
		}
	}
}

// after is what every reconcile pass and every action ends with: the
// keep-awake request is brought in line with the state that now exists,
// and a fresh view is published.
func (s *Supervisor) after() {
	s.applyKeepAwake()
	s.publish()
}

// store builds an immutable view and publishes it without telling anyone.
func (s *Supervisor) store() {
	v := s.buildView()
	s.view.Store(&v)
}

// publish stores an immutable view and tells the caller who asked to be
// told. Notify is called with nothing held, so a notifier is free to read
// the view straight back.
func (s *Supervisor) publish() {
	s.store()
	if s.deps.Notify != nil {
		s.deps.Notify()
	}
}

// View returns the most recently published view. It never waits for the
// loop, so a page stays readable even while a resume that takes several
// seconds is running.
func (s *Supervisor) View() View {
	v := s.view.Load()
	if v == nil {
		return View{Sessions: []SessionView{}, Watches: []WatchView{}, NotRunning: []PastView{}}
	}
	return *v
}

// find returns the watch for id, or nil. The pointer is into the live
// state and is only valid inside the call that asked for it.
func (s *Supervisor) find(id string) *state.Watch {
	return s.st.Find(id)
}

// persist writes the whole state out. A failure is reported and the
// in-memory state is kept, since losing it as well would help no one.
func (s *Supervisor) persist() {
	if s.deps.Store == nil {
		return
	}
	if err := s.deps.Store.Save(s.st); err != nil {
		s.logError("", "could not save state: "+err.Error())
	}
}

// readEnv asks what this machine can offer, tolerating wiring that has not
// supplied an answer.
// readTerminal asks what Open in a terminal would open, which can change
// when Windows Terminal is installed or removed.
func (s *Supervisor) readTerminal() string {
	if s.deps.TerminalName == nil {
		return ""
	}
	return s.deps.TerminalName()
}

func (s *Supervisor) readEnv() hosts.Env {
	if s.deps.Env == nil {
		return hosts.Env{}
	}
	return s.deps.Env()
}

func (s *Supervisor) logInfo(session, msg string) {
	if s.deps.Log != nil {
		s.deps.Log.Info(session, msg)
	}
}

func (s *Supervisor) logError(session, msg string) {
	if s.deps.Log != nil {
		s.deps.Log.Error(session, msg)
	}
}

func (s *Supervisor) logAuto(session, reason, msg string) {
	if s.deps.Log != nil {
		s.deps.Log.Auto(session, reason, msg)
	}
}

// applyKeepAwake holds the keep-awake request while at least one watch is
// Watching, In background or Starting, and drops it otherwise. A Stuck
// watch is not keeping anything alive, so it does not count.
func (s *Supervisor) applyKeepAwake() {
	n := activeWatches(s.st.Watches)
	s.setKeepAwake(power.Wanted(power.ModeBabysitting, n), n)
}

// setKeepAwake acquires or releases the request, and says so in the
// activity log when the answer changed or when the number of babysat
// sessions it is held for did. A platform that cannot hold the request at
// all is reported once rather than on every pass.
func (s *Supervisor) setKeepAwake(want bool, n int) {
	if want && s.held {
		if n != s.heldFor {
			s.heldFor = n
			s.logInfo("", keepingAwake(n))
		}
		return
	}
	if want == s.held {
		return
	}
	if !want {
		s.dropKeepAwake("no longer keeping the computer awake, no babysat session is active")
		return
	}
	if s.deps.Power == nil || !s.deps.Power.Supported() {
		if !s.unsupportedWarned {
			s.unsupportedWarned = true
			s.logInfo("", "keeping the computer awake is not supported on this system")
		}
		return
	}
	release, ok := s.deps.Power.Acquire()
	if !ok {
		if !s.refusedWarned {
			s.refusedWarned = true
			s.logError("", "the system refused the keep-awake request")
		}
		return
	}
	s.release = release
	s.held = true
	s.heldFor = n
	s.logInfo("", keepingAwake(n))
}

// keepingAwake is the sentence the activity log carries while the request
// is held, with every babysat session that holds it counted.
func keepingAwake(n int) string {
	return "keeping the computer awake for " + plural(n, "babysat session", "babysat sessions")
}

// dropKeepAwake releases the request, if it is held at all, and says in the
// activity log why it went. The sentence is the caller's, since a request
// that went because nothing is left to keep awake and one that went because
// this program is stopping are different things to read.
func (s *Supervisor) dropKeepAwake(msg string) {
	if !s.held {
		return
	}
	if s.release != nil {
		s.release()
		s.release = nil
	}
	s.held = false
	s.heldFor = 0
	s.logInfo("", msg)
}

// graceUntil is when the start-up grace ends, or the zero time when there
// is none. Only a machine with a display has one: apps there, Claude
// Desktop in particular, restore their own sessions at login, and nothing
// on a server does.
func (s *Supervisor) graceUntil() time.Time {
	if s.env.Headless || s.deps.StartupGrace <= 0 {
		return time.Time{}
	}
	return s.startedAt.Add(s.deps.StartupGrace)
}

// inGrace reports whether the start-up grace is still running.
func (s *Supervisor) inGrace() bool {
	until := s.graceUntil()
	return !until.IsZero() && s.deps.Now().Before(until)
}

// autostartInterval is how often the login item is looked at, so a saved
// setting follows one someone added or removed by hand.
const autostartInterval = 30 * time.Second

// maybeCheckAutostart looks at the login item when the last look is at
// least autostartInterval old.
func (s *Supervisor) maybeCheckAutostart() {
	if s.deps.Now().Sub(s.autostartAt) < autostartInterval {
		return
	}
	s.checkAutostart()
}

// checkAutostart brings the saved start at login setting in line with the
// login item that is really there. It only ever looks: the item itself is
// never written or removed from here. A look that fails, or no way to look
// at all, leaves the setting as it is.
func (s *Supervisor) checkAutostart() {
	s.autostartAt = s.deps.Now()
	if s.deps.AutostartInstalled == nil {
		return
	}
	// The launcher's note speaks only for the login item it found as this
	// copy started.
	note := s.deps.AutostartNote
	s.deps.AutostartNote = ""
	installed, err := s.deps.AutostartInstalled()
	if err != nil || installed == s.st.Settings.Autostart {
		return
	}
	s.st.Settings.Autostart = installed
	s.persist()
	// On a Linux server the login item is CC Babysitter's own systemd
	// service, which installing CC Babysitter sets up, and the page does
	// not speak of start at login there. A Mac or Windows machine reached
	// over SSH has no display either, but its login item still runs at
	// login.
	server := s.env.Headless && s.env.Platform == "linux"
	switch {
	case installed && server:
		s.logInfo("", "Set up as a service that starts at boot.")
	case installed && note != "":
		s.logInfo("", note)
	case installed:
		s.logInfo("", "Start at login was turned on outside CC Babysitter.")
	case server:
		s.logInfo("", "The service that starts CC Babysitter at boot was turned off outside CC Babysitter.")
	default:
		s.logInfo("", "Start at login was turned off outside CC Babysitter.")
	}
}
