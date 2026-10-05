package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/buildinfo"
	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/power"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
	"ccbabysitter.dev/ccbabysitter/internal/web"
)

// defaultPort is the port CC Babysitter tries to serve the page on
// before falling back to a random free one.
const defaultPort = 47391

// envRefreshInterval is how often the cached environment snapshot is
// rebuilt. Detecting the CLI's version runs "claude --version", which is
// slow enough that doing it on every page load would be felt.
const envRefreshInterval = 60 * time.Second

// shutdownGrace is how long the HTTP server is given to finish requests
// already in flight once a shutdown begins.
const shutdownGrace = 2 * time.Second

// serveOptions are the flags that shape the default command.
type serveOptions struct {
	NoOpen bool
	Demo   bool
	Port   int
	// Foreground keeps a plain run serving in this terminal instead of
	// handing CC Babysitter to the system's service manager.
	Foreground bool
	// PortSet is whether --port was given, which only a run that serves in
	// this terminal can use.
	PortSet bool
	// Service is set when the systemd unit runs this copy, or anything else
	// whose output goes to the systemd journal. Its output goes
	// to the journal, which other accounts may be able to read and which
	// keeps every restart's output, so it prints the page's plain address
	// and never the key. The person gets the address with the key from
	// the run that set the service up, and from ccbabysitter status.
	Service bool

	// onListen, when set, is called with the listener serve just opened,
	// before anything is served on it. Production wiring never sets this;
	// it exists so a test can reach in and close the listener out from
	// under a running server, to exercise the path where the HTTP server
	// stops on its own instead of being asked to.
	onListen func(net.Listener)

	// onStateDir, when set, is told which state folder serve uses. Like
	// onListen, only tests set it, to find a demo run's temporary folder.
	onStateDir func(string)
}

// listenPreferred opens a TCP listener on 127.0.0.1 at port, or, when that
// port cannot be bound, on a random free loopback port instead. Nothing in
// this program can bind anywhere but loopback: there is no flag for the
// address.
func listenPreferred(port int) (net.Listener, error) {
	if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
		return ln, nil
	}
	return net.Listen("tcp", "127.0.0.1:0")
}

// envCache holds the most recently detected environment snapshot behind a
// mutex, so a slow refresh never blocks a page read.
type envCache struct {
	mu  sync.Mutex
	env hosts.Env
}

func (c *envCache) get() hosts.Env {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.env
}

func (c *envCache) set(e hosts.Env) {
	c.mu.Lock()
	c.env = e
	c.mu.Unlock()
}

// targetCache holds the user@host a person on another computer would ssh
// to, the same way envCache holds the environment: the refresh sets it and
// the supervisor reads it, each from its own goroutine.
type targetCache struct {
	mu     sync.Mutex
	target string
}

func (c *targetCache) get() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.target
}

func (c *targetCache) set(target string) {
	c.mu.Lock()
	c.target = target
	c.mu.Unlock()
}

// sshTargetFor is the ssh destination, user@address as hosts.SSHTarget
// renders it, on a machine with no display, where the person is always on
// another computer, and empty anywhere else.
func sshTargetFor(headless bool, user, address string) string {
	if !headless {
		return ""
	}
	return hosts.SSHTarget(user, address)
}

// envRefresher is the refresh serve runs in the background: it detects
// the environment in full, login question included, and reads the ssh
// address again, which a later run from another ssh connection may have
// saved in stateDir, so the commands for reaching this machine from
// elsewhere follow it.
func envRefresher(cache *envCache, target *targetCache, stateDir string, detect func() hosts.Env) func() {
	return func() {
		env := detect()
		cache.set(env)
		user, address := currentUserAndAddress(stateDir)
		target.set(sshTargetFor(env.Headless, user, address))
	}
}

// startEnvRefresh runs refresh in the background once right away and then
// every interval, until ctx ends, counting itself in wg. The first run is
// not held back by the interval: the environment serve starts with leaves
// out the slow login question, and this is what fills it in.
func startEnvRefresh(ctx context.Context, wg *sync.WaitGroup, interval time.Duration, refresh func()) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		if ctx.Err() != nil {
			return
		}
		refresh()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				refresh()
			}
		}
	}()
}

// snapshotHasDesktop reports whether any session in snap is currently
// running in the desktop app, which counts as the app being open even
// when its own process cannot be found directly.
func snapshotHasDesktop(snap observe.Snapshot) bool {
	for _, s := range snap.Sessions {
		if s.Host == claude.HostDesktop {
			return true
		}
	}
	return false
}

// alreadyRunningLine is what a run that finds another copy holding the
// state folder says: where that copy's page is, with its key when withKey
// is set, when what it needs can be read from stateDir and a live copy
// still holds the lock, and only that it is running otherwise. Without a
// live holder the saved address may be an old one that names another
// account's server by now, which must not be handed the key.
func alreadyRunningLine(stateDir string, withKey bool) string {
	u, key := state.PageURL(stateDir), state.ReadPageKey(stateDir)
	if u == "" || key == "" || !state.IsHeld(stateDir) {
		return "CC Babysitter is already running."
	}
	if !withKey {
		return "CC Babysitter is already running: " + u
	}
	return "CC Babysitter is already running: " + state.KeyedURL(u, key)
}

// openInBrowser opens the page in a browser through open, with a one-time
// launch token in place of the key: the program that opens a browser, and
// often the browser itself, shows its command line to every account on
// the machine.
func openInBrowser(srv *web.Server, pageURL string, open func(string) error) error {
	return open(srv.LaunchURL(pageURL))
}

// claimStateDir takes stateDir's single-instance lock and, the moment it
// has it, removes the page address a copy before it may have left
// behind. The lock is what tells the command line a live copy's address
// can be trusted with the key; between taking it and saving this copy's
// own address, slower start-up steps run, and an old address must not be
// on disk then, since it may name another account's server by now. A copy
// that does not get the lock leaves everything as it is.
//
// When the old address cannot be removed, the lock is given back and the
// error is a *clearError: this copy did get the folder, so it must not say
// another copy is running, nor print the address it could not remove.
func claimStateDir(stateDir string, createMs int64) (*state.Lock, error) {
	lock, err := state.Acquire(stateDir, createMs)
	if err != nil {
		return nil, err
	}
	if err := clearPageURL(stateDir); err != nil {
		lock.Release()
		return nil, &clearError{err}
	}
	return lock, nil
}

// bootTime is procs.BootTime, a variable so tests can replace it.
var bootTime = procs.BootTime

// clearPageURL is state.ClearPageURL, a variable only so a test can make
// it fail.
var clearPageURL = state.ClearPageURL

// clearError is claimStateDir failing to remove the old page address.
type clearError struct{ err error }

func (e *clearError) Error() string { return e.err.Error() }
func (e *clearError) Unwrap() error { return e.err }

// claimFailureLine is what a start that could not claim stateDir says:
// that it could not remove the old page address, when that was it, and
// otherwise that another copy is running, with its address as
// alreadyRunningLine gives it.
func claimFailureLine(stateDir string, err error, withKey bool) string {
	var ce *clearError
	if errors.As(err, &ce) {
		return fmt.Sprintf("Could not clear the old page address in %s: %v", stateDir, ce.err)
	}
	return alreadyRunningLine(stateDir, withKey)
}

// shouldOpenBrowser decides whether serve should open a browser window on
// the page once it is ready.
func shouldOpenBrowser(noOpen, headless, autoOpen bool) bool {
	return !noOpen && !headless && autoOpen
}

// serve wires every internal package into a running program: it opens the
// state folder and the listener, builds the engine (the real supervisor,
// or a scripted demo that touches nothing real), starts the HTTP server,
// prints the console banner, opens a browser unless told not to, and shuts
// everything down cleanly once ctx is canceled. It returns the process
// exit code.
func serve(ctx context.Context, opts serveOptions, stdout io.Writer) int {
	// A quit from the page or the command line cancels this context, which
	// shuts everything down exactly as Ctrl+C does.
	ctx, quitServe := context.WithCancel(ctx)
	defer quitServe()

	version := buildinfo.Version

	stateDir := state.DefaultDir()
	if opts.Demo {
		dir, err := os.MkdirTemp("", "ccbabysitter-demo-*")
		if err != nil {
			fmt.Fprintln(stdout, "could not create a temporary state folder:", err)
			return 1
		}
		defer os.RemoveAll(dir)
		stateDir = dir
	}

	if opts.onStateDir != nil {
		opts.onStateDir(stateDir)
	}

	realProcs := procs.NewReal()
	state.SetPIDChecker(realProcs.Exists)

	// release is idempotent, since it is both called explicitly at the
	// normal end of this function (so the lock is gone before the closing
	// "quit" log line is written) and deferred right after a successful
	// Acquire, as a safety net against a panic leaving ccbabysitter.lock
	// behind forever.
	var lock *state.Lock
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			if lock != nil {
				lock.Release()
			}
		})
	}

	createMs, _ := realProcs.CreateTime(os.Getpid())
	if !opts.Demo {
		l, err := claimStateDir(stateDir, createMs)
		if err != nil {
			fmt.Fprintln(stdout, claimFailureLine(stateDir, err, !opts.Service))
			return 1
		}
		lock = l
		defer release()
	}

	log, err := state.NewLog(stateDir)
	if err != nil {
		fmt.Fprintln(stdout, "could not open the log:", err)
		release()
		return 1
	}
	log.Info("", fmt.Sprintf("%s %s start, pid %d, state %s", buildinfo.Name, version, os.Getpid(), stateDir))
	// A login item the launcher or the Windows install script rewrote is
	// noted for the next copy, whose first look at it would otherwise call
	// it someone else's. Every copy that is not the demo takes the note, so
	// it is never left behind to explain a later change.
	autostartNote := ""
	if !opts.Demo {
		autostartNote = state.TakeLoginItemNote(stateDir)
	}
	if opts.Service {
		// Why this background copy started: the launcher's note when it
		// started it; otherwise the system, again after the copy before
		// it ended without cleaning up, or at login or boot.
		prevMs, wasThere := state.TakeServiceRunning(stateDir)
		_ = state.MarkServiceRunning(stateDir, createMs)
		crashed := restartedAfterCrash(runtime.GOOS, prevMs, wasThere, bootTime())
		log.Info("", startedReason(state.TakeStartReason(stateDir), hosts.Headless(), crashed))
	}

	// The page's access key, which every request must carry. A demo has its
	// own temporary folder, and so its own key.
	key, err := state.PageKey(stateDir)
	if err != nil {
		msg := "could not create the page's key: " + err.Error()
		fmt.Fprintln(stdout, msg)
		log.Error("", msg)
		release()
		return 1
	}

	ln, err := listenPreferred(opts.Port)
	if err != nil {
		fmt.Fprintln(stdout, "could not listen on a port:", err)
		release()
		return 1
	}
	if opts.onListen != nil {
		opts.onListen(ln)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	pageURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	// keyed is the address a person opens: the page's address with its
	// key. It is printed, except by the service, but never saved in
	// page-url, logged, or handed to the program that opens a browser,
	// which gets a one-time launch token instead; the page-url file and
	// the page's own view keep the plain address.
	keyed := state.KeyedURL(pageURL, key)
	// Another run of this program, such as the one that started the
	// systemd service, reads the port back from here. Failing to save it
	// only means that run cannot say where the page is.
	if err := state.SavePageURL(stateDir, pageURL); err != nil {
		log.Error("", "could not save the page address: "+err.Error())
	}
	// The account and address a person on another computer would connect
	// to, for the commands the banner and the page print. Reading them also
	// remembers the address of an ssh connection this was started from.
	sshUser, sshAddress := currentUserAndAddress(stateDir)

	// innerCtx is what every goroutine this function starts actually runs
	// on. It is canceled whenever ctx is (an ordinary shutdown), and it is
	// also canceled by hand if the HTTP server stops on its own while ctx
	// is still live: without that second trigger, the supervisor loop and
	// the environment refresh goroutine would wait for a cancellation that
	// would otherwise never come, and this function would hang instead of
	// reporting the failure and returning.
	innerCtx, cancelInner := context.WithCancel(ctx)
	defer cancelInner()

	var wg sync.WaitGroup
	var srv *web.Server
	var runLoop func(context.Context)
	var envAt func() hosts.Env
	var settingsAt func() state.Settings
	var stopDemo func()

	if opts.Demo {
		demoEngine := web.NewDemoEngine(log)
		srv = web.NewServer(demoEngine, log, version, key)
		if d, ok := demoEngine.(*web.DemoEngine); ok {
			d.SetNotify(srv.Notify)
			stopDemo = d.Close
		}
		envAt = func() hosts.Env { return demoEngine.View().Env }
		settingsAt = func() state.Settings { return demoEngine.View().Settings }
	} else {
		store := &state.Store{Dir: stateDir}
		runner := claude.ExecRunner{}
		obs := observe.New(observe.ReadSessionFiles(claude.SessionsDir()), realProcs, runner, log)

		// A fresh Observer has never swept the session folder, so its
		// Current() is empty until something asks it to look. Refreshing
		// once here, synchronously, means the very first environment
		// detection already knows whether a desktop session exists,
		// instead of always assuming there is none until the first
		// scheduled sweep happens to land.
		obs.RefreshNow()

		probes := hosts.RealProbes(realProcs.ExeContains)
		cache := &envCache{}
		target := &targetCache{}
		// The page has to answer quickly, since a run that just started
		// the service waits for it, so the environment it starts with
		// leaves out the login question, which can take as long as the
		// version one again. The refresh below asks it straight away.
		cache.set(hosts.DetectWithoutLogin(innerCtx, probes, runner, snapshotHasDesktop(obs.Current())))
		target.set(sshTargetFor(cache.get().Headless, sshUser, sshAddress))
		startEnvRefresh(innerCtx, &wg, envRefreshInterval, envRefresher(cache, target, stateDir, func() hosts.Env {
			return hosts.Detect(innerCtx, probes, runner, snapshotHasDesktop(obs.Current()))
		}))
		envAt = cache.get

		terminal := hosts.NewTerminalLauncher(stateDir)
		deps := supervise.Deps{
			Store:  store,
			Log:    log,
			Obs:    obs,
			Procs:  realProcs,
			Runner: runner,
			Power:  power.New(),
			Env:    cache.get,

			ProjectsDir:        claude.ProjectsDir(),
			URL:                pageURL,
			SSHTarget:          target.get,
			Autostart:          autostartInstaller,
			AutostartInstalled: autostartInstalled,
			AutostartNote:      autostartNote,
			OpenTerminal:       terminal.OpenAttach,
			TerminalName:       terminal.Name,
		}
		deps.Notify = func() {
			if srv != nil {
				srv.Notify()
			}
		}

		supervisor := supervise.New(deps)
		srv = web.NewServer(supervisor, log, version, key)
		runLoop = supervisor.Run
		settingsAt = func() state.Settings { return supervisor.View().Settings }

		obs.Start(innerCtx, claude.SessionsDir())
	}
	if stopDemo != nil {
		defer stopDemo()
	}

	srv.OnQuit(quitServe)
	httpServer := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	// An event stream holds its connection open indefinitely and has
	// nothing in the request to notice a shutdown through, so it is told
	// directly; without this a shutdown would sit out its whole grace
	// period waiting for a page that is simply doing its job.
	httpServer.RegisterOnShutdown(srv.Shutdown)
	serveErr := make(chan error, 1)

	if runLoop != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runLoop(innerCtx)
		}()
	}
	go func() {
		err := httpServer.Serve(ln)
		if err == http.ErrServerClosed {
			err = nil
		}
		serveErr <- err
	}()

	env := envAt()
	settings := settingsAt()
	shown := keyed
	if opts.Service {
		shown = pageURL
	}
	closing := foregroundClosing
	if opts.Service {
		closing = serviceClosing
	}
	fmt.Fprint(stdout, banner(version, shown, env.Headless, sshUser, sshAddress, env.CLIVersion, env.DesktopVersion, env.VSCodeExtVersion, closing))

	if shouldOpenBrowser(opts.NoOpen, env.Headless, settings.AutoOpenBrowser) {
		if err := openInBrowser(srv, pageURL, openBrowser); err != nil {
			// The page is serving either way, so this is not a failure
			// to start; it is something the person needs to know before
			// they sit waiting for a window that is never coming. The
			// person is given the address with the key, except from the
			// service; the log, which is kept, gets the plain one.
			fmt.Fprintf(stdout, "Could not open a browser: %s. Open %s yourself.\n", err, shown)
			log.Error("", fmt.Sprintf("Could not open a browser: %s. Open %s yourself.", err, pageURL))
		}
	}

	rc := 0
	served := false
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		served = true
		// The HTTP server stopped on its own while the caller never asked
		// for a shutdown. ctx is not canceled, so nothing else here would
		// ever stop on its own either: innerCtx is canceled by hand so the
		// supervisor loop and the environment refresh goroutine return
		// instead of waiting forever, and the failure is reported the way
		// an operator watching this process would need to see it, on
		// stderr as well as in the log.
		cancelInner()
		if err != nil {
			msg := "the web server stopped unexpectedly: " + err.Error()
			fmt.Fprintln(os.Stderr, msg)
			log.Error("", msg)
			rc = 1
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	_ = httpServer.Shutdown(shutdownCtx)
	cancel()
	if !served {
		<-serveErr
	}

	wg.Wait()
	_ = state.RemovePageURL(stateDir, pageURL)
	if opts.Service {
		state.ClearServiceRunning(stateDir)
	}
	release()
	log.Info("", "quit")
	return rc
}
