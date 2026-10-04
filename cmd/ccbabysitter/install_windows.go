//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/client"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// runInstall explains that install, which sets up a systemd service, is a
// Linux affair; on Windows a plain ccbabysitter starts the background copy.
func runInstall(out io.Writer) int {
	fmt.Fprintln(out, "install sets up a systemd user service and is for Linux. On Windows, run ccbabysitter: it starts CC Babysitter in the background.")
	return 2
}

// runUninstall quits the running copy, turns start at login off and
// removes an earlier version's Startup script. The state folder stays.
func runUninstall(out io.Writer) int {
	stateDir := state.DefaultDir()
	// The page's key goes only to a copy whose lock names a live process,
	// never to a stale address another account may listen on by now.
	state.SetPIDChecker(procs.NewReal().Exists)
	rc := 0
	if cl, err := client.New(stateDir, ""); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		res, err := cl.Quit(ctx)
		cancel()
		switch {
		case err != nil:
			fmt.Fprintln(out, "could not ask CC Babysitter to quit:", err)
			rc = 1
		case !res.OK:
			fmt.Fprintln(out, res.Message)
			rc = 1
		default:
			fmt.Fprintln(out, "Quit CC Babysitter.")
			waitForRelease(stateDir, 10*time.Second)
		}
	}
	name, err := installAutostartWindows(false)
	if err != nil {
		fmt.Fprintln(out, "could not turn start at login off:", err)
		return 1
	}
	fmt.Fprintln(out, "Removed", name)
	fmt.Fprintln(out, foregroundHint)
	return rc
}
