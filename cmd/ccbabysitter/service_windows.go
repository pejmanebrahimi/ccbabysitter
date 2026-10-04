//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

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

// errStillOwned says the windowless program started, but inside the job
// of the terminal or app that ran the launcher, so closing that may end it.
var errStillOwned = errors.New("started inside this window's job")

// spawn starts the program at path as the service with these creation
// flags, and lets it go. Only a failed start is an error. Tests replace it.
var spawn = func(path string, flags uint32) error {
	cmd := exec.Command(path, "--service")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

// viaExplorer asks this user's Explorer to start the program at path.
// Explorer runs outside the job of the terminal or app that ran the
// launcher, and so does what it starts. It is started without arguments:
// the windowless program runs as the service by its name alone. Explorer's
// exit code says nothing about whether the program started, so only a
// failure to run explorer.exe at all is an error. Tests replace it.
var viaExplorer = func(path string) error {
	windir := os.Getenv("WINDIR")
	if windir == "" {
		windir = `C:\Windows`
	}
	err := exec.Command(filepath.Join(windir, "explorer.exe"), path).Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return nil
	}
	return err
}

// explorerWait is how long the launcher waits for the program Explorer was
// asked to start to appear.
const explorerWait = 10 * time.Second

// outsideJob reports whether a copy of the program at path that started at
// or after since is running outside this process's job, waiting up to
// explorerWait for one to appear. Tests replace it.
var outsideJob = func(path string, since time.Time) bool {
	deadline := time.Now().Add(explorerWait)
	for {
		if startedOutsideJob(filepath.Base(path), since) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procIsProcessInJob = kernel32.NewProc("IsProcessInJob")
)

// startedOutsideJob reports whether a process whose program is named name
// started at or after since, give or take a second for the clock's grain,
// and is not in this process's job.
func startedOutsideJob(name string, since time.Time) bool {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(snap)
	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = syscall.Process32First(snap, &entry); err == nil; err = syscall.Process32Next(snap, &entry) {
		if !strings.EqualFold(syscall.UTF16ToString(entry.ExeFile[:]), name) {
			continue
		}
		// PROCESS_QUERY_LIMITED_INFORMATION is enough for both questions.
		h, err := syscall.OpenProcess(0x1000, false, entry.ProcessID)
		if err != nil {
			continue
		}
		var created, exited, kernel, user syscall.Filetime
		timesErr := syscall.GetProcessTimes(h, &created, &exited, &kernel, &user)
		var inJob int32
		// A zero job handle asks about the job this process is in.
		ok, _, _ := procIsProcessInJob.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&inJob)))
		syscall.CloseHandle(h)
		if timesErr != nil || ok == 0 {
			continue
		}
		if time.Unix(0, created.Nanoseconds()).Before(since.Add(-time.Second)) {
			continue
		}
		if inJob == 0 {
			return true
		}
	}
	return false
}

// startBackground starts the windowless program at path as the service,
// out of the job of the terminal or app that ran the launcher, which would
// end it when that closes. A job that forbids breaking away refuses the
// first try, and refuses a start under another parent as well; the second
// asks Explorer, outside that job, to start it, and checks that it then
// runs outside the job; the last starts it anyway and reports
// errStillOwned.
var startBackground = func(path string) error {
	const flags = createNewProcessGroup | detachedProcess
	if spawn(path, flags|createBreakawayFromJob) == nil {
		return nil
	}
	since := time.Now()
	if viaExplorer(path) == nil && outsideJob(path, since) {
		return nil
	}
	if err := spawn(path, flags); err != nil {
		return err
	}
	return errStillOwned
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
// value, writing the value before removing the script, and writes the Run
// value again when it names a program that has since moved. A Run value
// that is not there is start at login turned off, and is left so; one that
// cannot be read is left alone too, so nothing restarts over it.
func (windowsControl) RefreshUnit(out io.Writer, desktop bool) (rewritten, ok bool) {
	if path, err := legacyScriptPath(); err == nil {
		if on, _ := pathPresent(path); on {
			if err := setRunValue(); err != nil {
				fmt.Fprintln(out, err)
				return false, false
			}
			if err := removeLegacyScript(); err != nil {
				fmt.Fprintln(out, "could not remove the old Startup script:", err)
			}
			return true, true
		}
	}
	data, found, err := queryRunValue()
	if err != nil || !found {
		return false, true
	}
	want, err := runValueWanted()
	if err != nil {
		fmt.Fprintln(out, err)
		return false, false
	}
	if strings.EqualFold(data, want) {
		return false, true
	}
	if err := setRunValue(); err != nil {
		fmt.Fprintln(out, err)
		return true, false
	}
	return true, true
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

// Start starts the windowless program as the service. Started inside this
// window's job, it says so: closing this window may then end it.
func (windowsControl) Start(out io.Writer, desktop bool) bool {
	err := startBackground(backgroundPath())
	switch {
	case errors.Is(err, errStillOwned):
		fmt.Fprintln(out, "Windows kept CC Babysitter inside this window's group of programs, so closing this window may stop it, whatever the lines below say. It starts on its own again at your next login.")
	case err != nil:
		fmt.Fprintln(out, "could not start", backgroundExe+":", err)
		return false
	}
	return true
}

// Restart asks the running copy to quit, waits until it has let the state
// folder go, and starts it again.
func (c windowsControl) Restart(out io.Writer, desktop bool) bool {
	stateDir := state.DefaultDir()
	state.SetPIDChecker(procs.NewReal().Exists)
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
