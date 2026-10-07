package observe

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

const (
	// SweepInterval is how often the observer rebuilds a snapshot even when
	// no file system event has been seen.
	SweepInterval = 5 * time.Second
	// CrossCheckInterval is how often the observer compares its own view of
	// live sessions against the CLI's own list.
	CrossCheckInterval = time.Minute
	// silenceReportEvery is how often a CLI that will not answer is worth
	// saying so about. The check runs before every resume and on a timer,
	// so a CLI that is slow to start, or missing entirely, would otherwise
	// fill the activity feed with the same line and bury everything else.
	silenceReportEvery = time.Minute
	// debounce absorbs the burst of file system events a single session
	// write can produce before the observer rebuilds a snapshot.
	debounce = 300 * time.Millisecond
)

// Observer watches Claude Code's session files and turns them into
// snapshots. Every sweep is published on Swept; a sweep whose set of
// sessions differs from the previous one is also published on Changed.
// Both channels are buffered by one and always hold the newest snapshot,
// so a slow or absent reader never blocks the observer.
type Observer struct {
	Changed <-chan Snapshot
	Swept   <-chan Snapshot
	changed chan Snapshot
	swept   chan Snapshot

	readFiles  func() [][]byte
	procs      procs.Procs
	runner     claude.Runner
	log        *state.Log
	sweepEvery time.Duration

	// refreshMu serializes an entire RefreshNow call: two goroutines may
	// call it at the same time, and each call must build, compare and
	// publish as one step rather than interleave with another call.
	refreshMu sync.Mutex

	// mu guards current on its own, held only long enough to read or
	// replace it, so a slow file read or liveness check never holds up a
	// concurrent Current() call.
	mu      sync.Mutex
	current Snapshot

	kick chan struct{}

	// now reads the clock. It is a field so a test can move time forward
	// by hand instead of waiting for it.
	now func() time.Time

	// stampMu guards lastStamp, the time the newest snapshot was built
	// at, which stamp keeps every later one after.
	stampMu   sync.Mutex
	lastStamp time.Time

	// silenceMu guards lastSilenceLog, which is when the observer last
	// wrote down that the CLI would not answer. Agents can be called from
	// the supervisor loop and from a test at the same time.
	silenceMu      sync.Mutex
	lastSilenceLog time.Time
	// cliMissing is true once the CLI was found not to be installed, until
	// it next answers. silenceMu guards it too.
	cliMissing bool

	// watchReady receives once the watch goroutine has completed its
	// initial setup. Nothing in the observer itself waits on it; it exists
	// so a test can wait for the watcher to be armed instead of guessing
	// with a fixed sleep.
	watchReady chan struct{}
}

// New builds an Observer. readFiles supplies the raw contents of every
// session file on each refresh; ReadSessionFiles builds one such function
// for a real directory on disk.
func New(readFiles func() [][]byte, p procs.Procs, r claude.Runner, log *state.Log) *Observer {
	o := &Observer{
		readFiles:  readFiles,
		procs:      p,
		runner:     r,
		log:        log,
		sweepEvery: SweepInterval,
		changed:    make(chan Snapshot, 1),
		swept:      make(chan Snapshot, 1),
		kick:       make(chan struct{}, 1),
		watchReady: make(chan struct{}, 1),
		now:        time.Now,
	}
	o.Changed, o.Swept = o.changed, o.swept
	return o
}

// Current returns the most recently built snapshot.
func (o *Observer) Current() Snapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.current
}

// Peek reads the session files as they are right now and returns what it
// finds, without publishing anything and without replacing the snapshot
// Current reports. It is for a caller that is itself driven by the
// published snapshots and needs to look at the world in the middle of
// doing something: RefreshNow would wake that caller and hand it the very
// work it has not finished yet, over and over. Everyone else wants
// RefreshNow, whose behaviour this does not change.
func (o *Observer) Peek() Snapshot {
	return Build(o.readFiles(), o.procs.Alive, o.stamp())
}

// stamp returns the time a snapshot being built now carries, which is
// always later than the one before it carried. The supervisor tells one
// snapshot from the next by that time, and the clock can read the same for
// two snapshots built moments apart: on Windows it moves in steps of up to
// several milliseconds. Such a snapshot is moved on by a nanosecond.
func (o *Observer) stamp() time.Time {
	o.stampMu.Lock()
	defer o.stampMu.Unlock()
	t := o.now()
	if !t.After(o.lastStamp) {
		t = o.lastStamp.Add(time.Nanosecond)
	}
	o.lastStamp = t
	return t
}

// RefreshNow builds a fresh snapshot from the current file contents and
// process liveness, publishes it on Swept, and publishes it on Changed as
// well if the set of sessions differs from the previous snapshot. It is
// safe to call from more than one goroutine at once.
func (o *Observer) RefreshNow() {
	o.refreshMu.Lock()
	defer o.refreshMu.Unlock()

	next := Build(o.readFiles(), o.procs.Alive, o.stamp())

	o.mu.Lock()
	changed := !same(o.current, next)
	o.current = next
	o.mu.Unlock()

	publish(o.swept, next)
	if changed {
		publish(o.changed, next)
	}
}

// publish delivers s without ever blocking the caller. The channel holds at
// most one value, so when a value is already waiting it is dropped in favor
// of s: only the newest snapshot matters to a reader that is behind.
func publish(ch chan Snapshot, s Snapshot) {
	for i := 0; i < 4; i++ {
		select {
		case ch <- s:
			return
		default:
		}
		select {
		case <-ch:
		default:
		}
	}
}

// Start runs the file watcher (when watchDir is non-empty), the sweep timer
// and the periodic cross-check against the CLI until ctx is canceled. It
// takes one snapshot immediately before returning so callers see a current
// view right away. No goroutine started here outlives ctx.
func (o *Observer) Start(ctx context.Context, watchDir string) {
	o.RefreshNow()
	if watchDir != "" {
		go o.watch(ctx, watchDir)
	}
	go func() {
		t := time.NewTicker(o.sweepEvery)
		defer t.Stop()
		var pending <-chan time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				o.RefreshNow()
			case <-o.kick:
				pending = time.After(debounce)
			case <-pending:
				pending = nil
				o.RefreshNow()
			}
		}
	}()
	go func() {
		t := time.NewTicker(CrossCheckInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				o.crossCheck(ctx)
			}
		}
	}()
}

// watch reports file system activity under dir by nudging the sweep loop
// through kick; it never builds a snapshot itself. When dir does not exist
// yet, it watches the parent instead and switches the watch over to dir
// once dir appears, through watchSwitch.
func (o *Observer) watch(ctx context.Context, dir string) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		o.log.Error("", "file watcher unavailable, sweep only: "+err.Error())
		return
	}
	defer w.Close()

	target := dir
	switcher := &watchSwitch{parent: filepath.Dir(dir), dir: dir}
	if err := w.Add(dir); err != nil {
		target = switcher.parent
		if err := w.Add(target); err != nil {
			o.log.Error("", "file watcher unavailable, sweep only: "+err.Error())
			return
		}
	}
	select {
	case o.watchReady <- struct{}{}:
	default:
	}

	// pendingSwitch is set once dir is seen to have appeared. From then on
	// every event on the parent retries the switch-over, since the parent
	// watch may cover unrelated activity that has nothing to do with dir.
	pendingSwitch := false

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if target != dir {
				if ev.Name == dir && ev.Has(fsnotify.Create) {
					pendingSwitch = true
				}
				if pendingSwitch && switcher.try(w, func(msg string) { o.log.Error("", msg) }) {
					target = dir
					pendingSwitch = false
				}
			}
			select {
			case o.kick <- struct{}{}:
			default:
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			if err != nil {
				o.log.Error("", "file watcher: "+err.Error())
			}
		}
	}
}

// Agents runs `claude agents --json` and parses the result. answered is
// false when the CLI did not answer with a usable list, which callers must
// never treat as an empty one.
func (o *Observer) Agents(ctx context.Context) ([]claude.AgentEntry, bool) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, runErr := o.runner.Run(cctx, "", hosts.AgentsArgs()...)
	list, err := claude.ParseAgents(out)
	if err != nil {
		// A CLI that is not installed is said once, not every minute: it
		// stays missing until someone installs it.
		if errors.Is(runErr, exec.ErrNotFound) {
			if o.firstMissing() {
				o.log.Error("", "The Claude Code CLI is not installed, so babysat sessions cannot be brought back.")
			}
			return nil, false
		}
		if o.shouldReportSilence() {
			reason := firstLine(out)
			if reason == "" && runErr != nil {
				reason = runErr.Error()
			}
			o.log.Error("", "agents --json did not answer: "+reason)
		}
		return nil, false
	}
	o.setMissing(false)
	return list, true
}

// firstMissing records that the CLI is not installed and reports whether
// that is news: true the first time, and again only after the CLI has
// answered in between.
func (o *Observer) firstMissing() bool {
	o.silenceMu.Lock()
	defer o.silenceMu.Unlock()
	if o.cliMissing {
		return false
	}
	o.cliMissing = true
	return true
}

// setMissing records whether the CLI is known to be missing.
func (o *Observer) setMissing(missing bool) {
	o.silenceMu.Lock()
	defer o.silenceMu.Unlock()
	o.cliMissing = missing
}

// shouldReportSilence reports whether enough time has passed since the
// last "did not answer" line to write another one. The first one always
// goes through: it is the one that tells the user something is wrong.
func (o *Observer) shouldReportSilence() bool {
	o.silenceMu.Lock()
	defer o.silenceMu.Unlock()
	now := o.now()
	if !o.lastSilenceLog.IsZero() && now.Sub(o.lastSilenceLog) < silenceReportEvery {
		return false
	}
	o.lastSilenceLog = now
	return true
}

// crossCheck compares the CLI's own list of sessions against the current
// snapshot and logs a disagreement. It only logs: nothing here changes what
// the observer reports elsewhere.
func (o *Observer) crossCheck(ctx context.Context) {
	list, ok := o.Agents(ctx)
	if !ok {
		return
	}
	snap := o.Current()
	var onlyAgents, onlyFiles []string
	for _, a := range list {
		if _, found := snap.Find(a.SessionID); !found {
			onlyAgents = append(onlyAgents, short(a.SessionID))
		}
	}
	for _, s := range snap.Sessions {
		found := false
		for _, a := range list {
			if a.SessionID == s.ID {
				found = true
				break
			}
		}
		if !found {
			onlyFiles = append(onlyFiles, s.ShortID)
		}
	}
	if len(onlyAgents)+len(onlyFiles) > 0 {
		o.log.Info("", "cross-check disagreement: only in agents "+join(onlyAgents)+", only in files "+join(onlyFiles))
	}
}

// firstLine returns the text before the first newline in s, or s itself
// when it has none.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// short returns the first 8 characters of id, or id itself when shorter.
func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// join renders list as a comma separated string, or "none" when it is empty.
func join(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ", ")
}
