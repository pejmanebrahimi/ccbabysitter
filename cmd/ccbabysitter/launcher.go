package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/buildinfo"
	"ccbabysitter.dev/ccbabysitter/internal/client"
	"ccbabysitter.dev/ccbabysitter/internal/state"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
)

// The launcher is what a person runs: a plain ccbabysitter. It never serves
// and never takes the state folder's lock. It makes sure the background
// copy is set up and running through the system's service manager, waits
// for its page, turns start at login on the first time, prints the start
// information and opens the page on a desktop, then gives the terminal
// back.

const (
	launchedReason  = "Started in the background by ccbabysitter."
	restartedReason = "Restarted in the background by ccbabysitter."
)

// launchOptions shape one launcher run.
type launchOptions struct {
	// Install is the install subcommand: a server unit that starts at
	// boot, lingering on, and start at login on whatever was chosen before.
	Install bool
	// Headless is whether this machine has no display, such as a server
	// reached over ssh.
	Headless bool
	// NoOpen keeps the launcher from opening the page.
	NoOpen bool
}

// launchDeps are what a launcher run needs from the running copy and the
// desktop. Tests replace them, so nothing real is touched.
type launchDeps struct {
	wait      waitFunc
	view      func(stateDir string) (supervise.View, error)
	loginOn   func(stateDir string) error
	launchURL func(stateDir string) (string, error)
	open      func(url string) error
}

// realLaunchDeps talks to the running copy through the same local API the
// command line uses, and opens the system's browser.
func realLaunchDeps() launchDeps {
	withClient := func(stateDir string, f func(context.Context, *client.Client) error) error {
		c, err := client.New(stateDir, "")
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return f(ctx, c)
	}
	return launchDeps{
		wait: waitForService,
		view: func(stateDir string) (supervise.View, error) {
			var v supervise.View
			err := withClient(stateDir, func(ctx context.Context, c *client.Client) error {
				var err error
				v, err = c.View(ctx)
				return err
			})
			return v, err
		},
		loginOn: func(stateDir string) error {
			return withClient(stateDir, func(ctx context.Context, c *client.Client) error {
				res, err := c.SetSettings(ctx, map[string]any{"autostart": true})
				if err == nil && !res.OK {
					err = fmt.Errorf("%s", res.Message)
				}
				return err
			})
		},
		launchURL: func(stateDir string) (string, error) {
			var u string
			err := withClient(stateDir, func(ctx context.Context, c *client.Client) error {
				var err error
				u, err = c.LaunchURL(ctx)
				return err
			})
			return u, err
		},
		open: openBrowser,
	}
}

// startInBackground is a launcher run, or, when there is no service manager
// to hand CC Babysitter to, says so and reports foreground true, and the
// caller serves in this terminal as before.
func startInBackground(out io.Writer, sc serviceControl, stateDir string, o launchOptions, d launchDeps) (rc int, foreground bool) {
	if !sc.Usable() {
		fmt.Fprintln(out, noServiceLine)
		return 0, true
	}
	return runLauncher(out, sc, stateDir, o, d), false
}

// runLauncher sets the background copy up and starts it, or finds it
// running, and tells the person where its page is. The address a person
// connects to is read first, which also saves the address of this ssh
// session for the copy, since it has no ssh connection of its own.
//
// A copy of CC Babysitter that holds the state folder's lock while the
// service is not running is one started in a terminal: nothing is changed
// while it runs, since the service could not start beside it.
//
// An installed unit that is not the one this program would write now is
// written again, and a copy running from the old one is restarted. A copy
// that answers with another version, such as one still running a program
// replaced since it started, is restarted once too. Otherwise a running
// copy is left running. Each start and restart leaves the copy a note of
// why, for its first Activity line.
//
// Start at login is turned on the first time, through the copy's own
// settings, and noted, so a person who turns it off later keeps it off.
// The install subcommand always turns it on, as it always has. A server,
// and anything install sets up, gets lingering too, so the copy keeps
// running after the person logs out.
func runLauncher(out io.Writer, sc serviceControl, stateDir string, o launchOptions, d launchDeps) int {
	user, address := currentUserAndAddress(stateDir)
	desktop := !o.Install && !o.Headless
	if !o.Install && sc.Installed() {
		desktop = keptKind(sc, stateDir, user, desktop)
	}
	server := !desktop

	active := sc.Active()
	if !active && anotherCopyRunning(stateDir) {
		fmt.Fprintln(out, otherCopyLine)
		return 1
	}

	restarted := false
	if !sc.Installed() {
		if !sc.Write(out, desktop) {
			return 1
		}
	} else {
		rewritten, ok := sc.RefreshUnit(out, desktop)
		if !ok {
			return 1
		}
		if rewritten && active {
			_ = state.WriteStartReason(stateDir, restartedReason)
			if !sc.Restart(out, desktop) {
				return 1
			}
			restarted = true
		}
	}
	if !active {
		_ = state.WriteStartReason(stateDir, launchedReason)
		if !sc.Start(out, desktop) {
			return 1
		}
	}
	if o.Install && !sc.Enable(out) {
		return 1
	}
	if server {
		turnLingeringOn(out, sc, stateDir, user)
	}

	page, ok := d.wait(stateDir)
	if ok && !restarted && page.Version != "" && page.Version != buildinfo.Version {
		_ = state.WriteStartReason(stateDir, restartedReason)
		if !sc.Restart(out, desktop) {
			return 1
		}
		page, ok = d.wait(stateDir)
	}
	if !ok {
		fmt.Fprintln(out, notStartedLine)
		return 1
	}

	// install has already enabled the unit; turning the copy's own setting
	// on too makes the banner, the page and the settings agree at once.
	if o.Install || !state.LoginStartOffered(stateDir) {
		if err := d.loginOn(stateDir); err != nil {
			fmt.Fprintln(out, "Could not turn start at login on:", err)
		} else {
			_ = state.MarkLoginStartOffered(stateDir)
		}
	}

	view, err := d.view(stateDir)
	if err != nil {
		view = supervise.View{}
	}
	version := page.Version
	if version == "" {
		version = buildinfo.Version
	}
	keyed := state.KeyedURL(page.URL, state.ReadPageKey(stateDir))
	fmt.Fprint(out, banner(version, keyed, o.Headless, user, address,
		view.Env.CLIVersion, view.Env.DesktopVersion, view.Env.VSCodeExtVersion,
		backgroundClosing(server, view.Settings.Autostart)))

	if !o.NoOpen && !o.Headless && view.Settings.AutoOpenBrowser {
		u, err := d.launchURL(stateDir)
		if err == nil {
			err = d.open(u)
		}
		if err != nil {
			fmt.Fprintf(out, "Could not open a browser: %s. Open %s yourself.\n", err, keyed)
		}
	}
	return 0
}

// keptKind is the kind of unit a run keeps, given the kind it would choose
// from where it runs. Once a unit exists its kind changes only for a good
// reason, so a plain run never undoes a setup or restarts the copy for
// nothing: a desktop reached over ssh keeps its desktop unit, and a unit
// set up to start at boot, with lingering, keeps that when a plain run
// comes from a desktop terminal. A unit an earlier version wrote for start
// at login on a desktop wants default.target without lingering, and
// becomes a desktop unit.
func keptKind(sc serviceControl, stateDir, user string, desktop bool) bool {
	existing, known := sc.UnitKind()
	if !known {
		return desktop
	}
	switch {
	case existing && !desktop:
		return true
	case !existing && desktop:
		lingering, err := sc.Lingering(user)
		if state.LingeringTurnedOn(stateDir) || (err == nil && lingering) {
			return false
		}
	}
	return desktop
}

// launches reports whether a run of the default command is the launcher:
// not the service itself, not the demo, not asked to stay in the
// foreground, and not started by systemd, which marks what it starts with
// INVOCATION_ID.
func launches(opts serveOptions, invocationID string) bool {
	return !opts.Service && !opts.Demo && !opts.Foreground && invocationID == ""
}
