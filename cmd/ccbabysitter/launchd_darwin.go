//go:build darwin

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// On macOS the background copy is a user LaunchAgent, labelled
// com.ccbabysitter, in the gui/<uid> domain, loaded and started the way
// brew services start does it, with no admin rights. There is one plist,
// and where it lives decides start at login: in ~/Library/LaunchAgents,
// launchd loads it at every login; in the state folder, only the launcher
// loads it, for this login.

const launchdLabel = "com.ccbabysitter"

// runLaunchctl runs launchctl with args and returns what it printed. Tests
// replace it, so they never touch the real launchd.
var runLaunchctl = func(args ...string) (string, error) {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	return string(out), err
}

// agentPath is where the plist lives while start at login is on.
func agentPath() string {
	path, err := launchAgentPath()
	if err != nil {
		return ""
	}
	return path
}

// offPath is where the plist lives while start at login is off.
func offPath() string {
	return filepath.Join(state.DefaultDir(), launchdLabel+".plist")
}

// installedPath is the plist there is, the one in LaunchAgents first, or
// empty when there is none.
func installedPath() string {
	for _, path := range []string{agentPath(), offPath()} {
		if on, _ := pathPresent(path); on && path != "" {
			return path
		}
	}
	return ""
}

func launchdDomain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }
func launchdJob() string    { return launchdDomain() + "/" + launchdLabel }

// launchdPlist is the plist this program would write now.
func launchdPlist() (string, error) {
	bin, err := executablePath()
	if err != nil {
		return "", err
	}
	return launchAgentPlist(bin, claude.FindCLI(), os.Getenv("XDG_DATA_HOME")), nil
}

// launchdControl is serviceControl for launchd.
type launchdControl struct{}

// newServiceControl returns the service manager a plain run hands CC
// Babysitter to.
func newServiceControl() serviceControl { return launchdControl{} }

// Usable reports whether this login has a GUI domain to load the
// LaunchAgent into, which a Mac reached only over ssh does not.
func (launchdControl) Usable() bool {
	_, err := runLaunchctl("print", launchdDomain())
	return err == nil
}

func (launchdControl) Installed() bool { return installedPath() != "" }

// UnitKind is always a desktop's on macOS: it starts at login.
func (c launchdControl) UnitKind() (desktop, known bool) { return true, c.Installed() }

// Active reports whether the job is loaded and running now.
func (launchdControl) Active() bool {
	out, err := runLaunchctl("print", launchdJob())
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "state = running" {
			return true
		}
	}
	return false
}

// Write writes the plist in the state folder. The launcher turns start at
// login on afterwards through the settings, which moves it into
// LaunchAgents.
func (launchdControl) Write(out io.Writer, desktop bool) bool {
	text, err := launchdPlist()
	if err != nil {
		fmt.Fprintln(out, "could not resolve this program's own path:", err)
		return false
	}
	if err := os.MkdirAll(filepath.Dir(offPath()), 0o700); err != nil {
		fmt.Fprintln(out, "could not write the LaunchAgent:", err)
		return false
	}
	if err := writeDurable(offPath(), []byte(text), 0o644); err != nil {
		fmt.Fprintln(out, "could not write the LaunchAgent:", err)
		return false
	}
	return true
}

// RefreshUnit writes the plist again, where it is, when it is not the one
// this program would write now, such as one an earlier version wrote or
// one naming a program that has since moved.
func (launchdControl) RefreshUnit(out io.Writer, desktop bool) (rewritten, ok bool) {
	path := installedPath()
	want, err := launchdPlist()
	if err != nil {
		fmt.Fprintln(out, "could not resolve this program's own path:", err)
		return false, false
	}
	if unitUpToDate(path, want) {
		return false, true
	}
	if err := writeDurable(path, []byte(want), 0o644); err != nil {
		fmt.Fprintln(out, "could not write the LaunchAgent:", err)
		return false, false
	}
	return true, true
}

// launchdUnloadWait is how long a reload waits for launchd to finish
// unloading the job, and the old copy to finish quitting, before loading it
// again: a few seconds past the shutdown grace.
var launchdUnloadWait = 10 * time.Second

// loadFailedHint follows a failure to load the LaunchAgent: the usual cause
// is that it was switched off under Login Items.
const loadFailedHint = "If CC Babysitter is switched off in System Settings, General, Login Items, switch it on there, or run ccbabysitter --foreground."

// Start loads the LaunchAgent, which starts it. A job that is still loaded
// but not running, as after a quit, is reloaded instead, so launchd reads
// the plist as it is now rather than a definition it still holds.
func (c launchdControl) Start(out io.Writer, desktop bool) bool {
	if _, err := runLaunchctl("print", launchdJob()); err == nil {
		return c.Restart(out, desktop)
	}
	return bootstrapAgent(out)
}

// Restart unloads the LaunchAgent, waits until launchd has let it go and
// the old copy has released the state folder, and loads it again, so
// launchd reads a plist written since it was loaded. A job launchd still
// holds after the wait is not loaded over, since that load would fail and
// nothing would start the copy once the old one ended.
func (launchdControl) Restart(out io.Writer, desktop bool) bool {
	_, _ = runLaunchctl("bootout", launchdJob())
	stateDir := state.DefaultDir()
	deadline := time.Now().Add(launchdUnloadWait)
	for {
		_, err := runLaunchctl("print", launchdJob())
		gone := err != nil && !state.IsHeld(stateDir)
		if gone {
			break
		}
		if !time.Now().Before(deadline) {
			fmt.Fprintln(out, "CC Babysitter is still shutting down. Run ccbabysitter again in a moment.")
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
	return bootstrapAgent(out)
}

// bootstrapAgent loads the plist there is into this login's GUI domain.
func bootstrapAgent(out io.Writer) bool {
	if text, err := runLaunchctl("bootstrap", launchdDomain(), installedPath()); err != nil {
		fmt.Fprintf(out, "launchctl bootstrap failed: %s %s\n%s\n", err, strings.TrimSpace(text), loadFailedHint)
		return false
	}
	return true
}

// Enable puts the plist in LaunchAgents, so it loads at every login, and
// starts it.
func (c launchdControl) Enable(out io.Writer) bool {
	if _, err := installAutostartDarwin(true); err != nil {
		fmt.Fprintln(out, "could not turn start at login on:", err)
		return false
	}
	return c.Start(out, true)
}

// Lingering is a systemd idea; macOS has none, so it is always on and
// never changed.
func (launchdControl) Lingering(string) (bool, error)  { return true, nil }
func (launchdControl) SetLingering(string, bool) error { return nil }
