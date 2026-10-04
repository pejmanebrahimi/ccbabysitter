package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// The launcher (launcher.go) hands CC Babysitter to the system's service
// manager. This file holds what it needs from that manager, the waiting for
// the started copy's page, and the lines it prints, which are the same on
// every platform; the systemd side lives in install_linux.go behind
// serviceControl.

const (
	// pageWaitTimeout is how long a run that started the service waits
	// for its page to answer before saying it did not start.
	pageWaitTimeout = 15 * time.Second
	// pageWaitInterval is how often it looks while it waits.
	pageWaitInterval = 250 * time.Millisecond
)

const (
	noServiceLine  = "Could not set CC Babysitter up as a service here, so it runs only while this terminal stays open."
	notStartedLine = "CC Babysitter did not start. See: journalctl --user -u ccbabysitter"
	otherCopyLine  = "CC Babysitter is already running in another terminal. Quit it there with Ctrl+C, then run ccbabysitter again."
)

// serviceControl is the systemd user unit, and the user's lingering, as
// far as the launcher needs them. The real one runs systemctl and loginctl;
// tests use a fake so they never touch the real service manager, lingering
// or home folder.
type serviceControl interface {
	lingering
	// Usable reports whether the user's service manager answers at all.
	Usable() bool
	// Installed reports whether the unit file is there.
	Installed() bool
	// UnitKind reads whether the installed unit is a desktop's or a
	// server's; known is false when there is none or it names neither.
	UnitKind() (desktop, known bool)
	// Write writes the unit, for a desktop or a server, and has the
	// service manager read it, telling out about any step that failed. It
	// reports whether everything worked. It starts nothing.
	Write(out io.Writer, desktop bool) bool
	// RefreshUnit writes the installed unit again when it is not the one
	// this program would write now, for a desktop or a server, such as one
	// an older version wrote or one naming a program that has since moved,
	// and has the service manager read it again, telling out about any
	// step that failed. A rewritten unit whose start at login is on stays
	// on. It reports whether the unit was written, and whether everything
	// it did worked; a unit that is already the right one is left alone.
	RefreshUnit(out io.Writer, desktop bool) (rewritten, ok bool)
	// Start starts the service if it is not running, which leaves a
	// running one alone, telling out about any step that failed. A desktop
	// unit is handed the display variables first. It does not turn start
	// at login on.
	Start(out io.Writer, desktop bool) bool
	// Enable turns start at boot on and starts the service if it is not
	// running, telling out about any step that failed. Only the install
	// subcommand uses it; lingering is not its business.
	Enable(out io.Writer) bool
	// Restart stops the running service and starts it again, telling out
	// when that failed, and reports whether it worked. A desktop unit is
	// handed the display variables first, as Start does.
	Restart(out io.Writer, desktop bool) bool
	// Active reports whether the service is running now.
	Active() bool
}

// servicePage is the page a started service answered on, and the version
// of CC Babysitter that answered, empty when it did not say.
type servicePage struct {
	URL     string
	Version string
}

// waitFunc waits for the service's page to answer, reading its address
// from stateDir, and reports whether it did.
type waitFunc func(stateDir string) (servicePage, bool)

// unitUpToDate reports whether the unit file at path says exactly want.
// A file that cannot be read is not up to date, so it is written again.
func unitUpToDate(path, want string) bool {
	have, err := os.ReadFile(path)
	return err == nil && string(have) == want
}

// anotherCopyRunning reports whether a copy of CC Babysitter is serving
// from stateDir now: its lock is there and names a process that is still
// alive.
func anotherCopyRunning(stateDir string) bool {
	state.SetPIDChecker(procs.NewReal().Exists)
	return state.IsHeld(stateDir)
}

// waitForService waits the usual time for the service's page to answer.
func waitForService(stateDir string) (servicePage, bool) {
	state.SetPIDChecker(procs.NewReal().Exists)
	return waitForPage(stateDir, pageWaitTimeout, pageWaitInterval, keyedPageState(stateDir))
}

// keyedPageState is pageState with the page's key read from stateDir on
// every look: a service started for the first time writes its key only as
// it starts, after the wait for it has begun.
func keyedPageState(stateDir string) func(pageURL string) (string, bool) {
	return func(pageURL string) (string, bool) {
		return pageState(pageURL, state.ReadPageKey(stateDir))
	}
}

// waitForPage looks every interval, for up to timeout, for a page address
// saved in stateDir that answers, and returns the first one that does
// with the version it answered with. A saved address is asked only while
// a live copy holds stateDir's lock: one left behind by a copy that
// crashed may name a port another account's server listens on now, and
// the folder's key must never be sent there.
func waitForPage(stateDir string, timeout, interval time.Duration, answers func(pageURL string) (version string, ok bool)) (servicePage, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if u := state.PageURL(stateDir); u != "" && state.IsHeld(stateDir) {
			if version, ok := answers(u); ok {
				return servicePage{URL: u, Version: version}, true
			}
		}
		if !time.Now().Add(interval).Before(deadline) {
			return servicePage{}, false
		}
		time.Sleep(interval)
	}
}

// pageState asks the page at pageURL for its state, sending key the way
// the command line does, which is a request the page's own guard lets
// through from a program on this machine, and reports whether it answered
// and the version of CC Babysitter it says it is, empty when the answer
// does not say. An empty key is not sent, and the page then refuses.
func pageState(pageURL, key string) (version string, ok bool) {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodGet, pageURL+"/api/state", nil)
	if err != nil {
		return "", false
	}
	if key != "" {
		req.Header.Set("Authorization", "token "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", false
	}
	var view struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&view); err != nil {
		return "", true
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return view.Version, true
}
