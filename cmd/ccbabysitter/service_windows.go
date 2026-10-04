//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/client"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// On Windows the background copy is the windowless program,
// ccbabysitter-background.exe, beside ccbabysitter.exe. There is no
// service manager in user mode: the launcher starts it itself, detached so
// that closing the terminal or the app that ran the launcher never ends
// it, and Windows starts it at login from the per-user Run key.

// Process creation flags for the background program: a process group of
// its own, no console, and out of any job object the launcher's terminal
// or app put it in, which would end it with them.
const (
	createNewProcessGroup  = 0x00000200
	detachedProcess        = 0x00000008
	createBreakawayFromJob = 0x01000000
)

// backgroundPath is the windowless program beside this one.
func backgroundPath() string {
	bin, err := executablePath()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(bin), backgroundExe)
}

// startBackground starts the program at path as the service and lets it
// go. A job object that forbids breaking away refuses the first start, so
// it is tried again without that flag. Tests replace it.
var startBackground = func(path string) error {
	start := func(flags uint32) error {
		cmd := exec.Command(path, "--service")
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	if err := start(createNewProcessGroup | detachedProcess | createBreakawayFromJob); err == nil {
		return nil
	}
	return start(createNewProcessGroup | detachedProcess)
}

// windowsControl is serviceControl for Windows.
type windowsControl struct{}

// newServiceControl returns what a plain run hands CC Babysitter to.
func newServiceControl() serviceControl { return windowsControl{} }

// Usable reports whether the windowless program is there. A copy installed
// some other way, such as with go install, has none, and then a plain run
// serves in this terminal.
func (windowsControl) Usable() bool {
	path := backgroundPath()
	if path == "" {
		return false
	}
	on, _ := pathPresent(path)
	return on
}

// Installed is always true: there is nothing to install beyond the
// windowless program, which Usable checks for.
func (windowsControl) Installed() bool { return true }

// UnitKind is always a desktop's on Windows: it starts at login.
func (windowsControl) UnitKind() (desktop, known bool) { return true, true }

// Write has nothing to write; the Run value is start at login's business.
func (windowsControl) Write(io.Writer, bool) bool { return true }

// RefreshUnit replaces an earlier version's Startup script with the Run
// value, and writes the Run value again when it names a program that has
// since moved. A Run value that is not there is start at login turned off,
// and is left so.
func (windowsControl) RefreshUnit(out io.Writer, desktop bool) (rewritten, ok bool) {
	if path, err := legacyScriptPath(); err == nil {
		if on, _ := pathPresent(path); on {
			if err := removeLegacyScript(); err != nil {
				fmt.Fprintln(out, "could not remove the old Startup script:", err)
				return false, false
			}
			if err := setRunValue(); err != nil {
				fmt.Fprintln(out, err)
				return true, false
			}
			return true, true
		}
	}
	text, err := runReg("query", runKey, "/v", runValue)
	if err != nil {
		return false, true
	}
	want, err := runValueWanted()
	if err != nil {
		fmt.Fprintln(out, err)
		return false, false
	}
	if runValueData(text) == want {
		return false, true
	}
	if err := setRunValue(); err != nil {
		fmt.Fprintln(out, err)
		return true, false
	}
	return true, true
}

// runValueData is the data reg query printed for the Run value: what
// follows REG_SZ on its line.
func runValueData(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if _, data, found := strings.Cut(line, "REG_SZ"); found {
			return strings.TrimSpace(data)
		}
	}
	return ""
}

// Active reports whether the background copy runs: the state lock is held
// by a live process whose program is the windowless one. A copy serving in
// a terminal holds the lock as ccbabysitter.exe instead.
func (windowsControl) Active() bool {
	pid, createMs, ok := state.Holder(state.DefaultDir())
	if !ok {
		return false
	}
	exe, ok := procs.NewReal().Exe(pid, createMs)
	return ok && strings.EqualFold(filepath.Base(exe), backgroundExe)
}

// Start starts the windowless program as the service.
func (windowsControl) Start(out io.Writer, desktop bool) bool {
	if err := startBackground(backgroundPath()); err != nil {
		fmt.Fprintln(out, "could not start", backgroundExe+":", err)
		return false
	}
	return true
}

// Restart asks the running copy to quit, waits until it has let the state
// folder go, and starts it again.
func (c windowsControl) Restart(out io.Writer, desktop bool) bool {
	stateDir := state.DefaultDir()
	if cl, err := client.New(stateDir, ""); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, _ = cl.Quit(ctx)
		cancel()
	}
	waitForRelease(stateDir, 10*time.Second)
	return c.Start(out, desktop)
}

// waitForRelease waits up to timeout for stateDir's lock to be let go.
func waitForRelease(stateDir string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for state.IsHeld(stateDir) && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
}

// Enable turns start at login on and starts the copy.
func (c windowsControl) Enable(out io.Writer) bool {
	if _, err := installAutostartWindows(true); err != nil {
		fmt.Fprintln(out, "could not turn start at login on:", err)
		return false
	}
	return c.Start(out, true)
}

// Lingering is a systemd idea; Windows has none, so it is always on and
// never changed.
func (windowsControl) Lingering(string) (bool, error)  { return true, nil }
func (windowsControl) SetLingering(string, bool) error { return nil }
