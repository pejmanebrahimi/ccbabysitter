//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// systemdUnitPath is where both the install subcommand and the autostart
// toggle write the user service.
func systemdUnitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", "ccbabysitter.service"), nil
}

// serviceInstalled reports whether the user unit is there. It only ever
// looks; writing it is install's job.
func serviceInstalled() bool {
	path, err := systemdUnitPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// writeUnit writes the user unit that starts binPath, for a desktop or a
// server, with the folder of the claude CLI found now put on its PATH.
func writeUnit(binPath string, desktop bool) (string, error) {
	path, err := systemdUnitPath()
	if err != nil {
		return "", err
	}
	text, err := unitFile(binPath, claude.FindCLI(), desktop, os.Getenv("XDG_DATA_HOME"))
	if err != nil {
		return "", err
	}
	return path, writeUnitAt(path, text)
}

// writeUnitAt writes text as the unit at path, making its folder first.
func writeUnitAt(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeDurable(path, []byte(text), 0o644)
}

func removeUnit() (string, error) {
	path, err := systemdUnitPath()
	if err != nil {
		return "", err
	}
	if err := removeDurable(path); err != nil {
		return "", err
	}
	return path, nil
}

// runSystemctl runs systemctl --user with args, its output discarded.
// Tests replace it, so they never touch the real service manager.
var runSystemctl = func(args ...string) error {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// executablePath is where this program is, for the unit to start. Tests
// replace it, since a test binary lives in a temporary folder that a unit
// must never name.
var executablePath = resolvedExecutablePath

// UnitKind reads whether the installed unit is a desktop's, wanted by the
// graphical session, or a server's, wanted at boot. known is false when
// there is no unit, or it names neither.
func (systemdControl) UnitKind() (desktop, known bool) {
	path, err := systemdUnitPath()
	if err != nil {
		return false, false
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	switch {
	case strings.Contains(string(text), "\nWantedBy=graphical-session.target\n"):
		return true, true
	case strings.Contains(string(text), "\nWantedBy=default.target\n"):
		return false, true
	}
	return false, false
}

// systemdControl is serviceControl for the real systemd user manager.
type systemdControl struct{}

// newServiceControl returns the service manager a plain run on a server
// hands CC Babysitter to.
func newServiceControl() serviceControl { return systemdControl{} }

// Usable asks the user's service manager for its environment, which
// answers whenever there is a user manager to talk to and fails when
// there is none, such as when there is no user bus.
func (systemdControl) Usable() bool { return runSystemctl("show-environment") == nil }

func (systemdControl) Installed() bool { return serviceInstalled() }

func (systemdControl) Active() bool {
	return runSystemctl("is-active", "--quiet", "ccbabysitter") == nil
}

// Write writes the unit, for a desktop or a server, and has systemd read
// it. It starts nothing.
func (systemdControl) Write(out io.Writer, desktop bool) bool {
	bin, err := executablePath()
	if err != nil {
		fmt.Fprintln(out, "could not resolve this program's own path:", err)
		return false
	}
	if _, err := writeUnit(bin, desktop); err != nil {
		fmt.Fprintln(out, "could not write the service file:", err)
		return false
	}
	if err := runSystemctl("daemon-reload"); err != nil {
		fmt.Fprintln(out, "systemctl --user daemon-reload failed:", err)
		return false
	}
	return true
}

// RefreshUnit writes the unit again when it is not the one this program
// would write now, for a desktop or a server, naming this program where it
// is now and the claude CLI found now, and has systemd read it again. A
// rewritten unit whose start at login was on is enabled again, so its
// enable link follows the target the unit names now.
func (systemdControl) RefreshUnit(out io.Writer, desktop bool) (rewritten, ok bool) {
	bin, err := executablePath()
	if err != nil {
		fmt.Fprintln(out, "could not resolve this program's own path:", err)
		return false, false
	}
	path, err := systemdUnitPath()
	if err != nil {
		fmt.Fprintln(out, "could not find the service file:", err)
		return false, false
	}
	want, err := unitFile(bin, claude.FindCLI(), desktop, os.Getenv("XDG_DATA_HOME"))
	if err != nil {
		fmt.Fprintln(out, "could not write the service file:", err)
		return false, false
	}
	if unitUpToDate(path, want) {
		// An enable link left in the other kind's wants folder, as after a
		// reenable that failed, is moved to the one this unit names.
		if on, _ := autostartInstalledLinux(); on && filepath.Base(filepath.Dir(enableLink(path))) != wantsFolderFor(desktop) {
			if err := runSystemctl("reenable", "ccbabysitter"); err != nil {
				fmt.Fprintln(out, "systemctl --user reenable ccbabysitter failed:", err)
				return false, false
			}
			flushStartLinks(path)
		}
		return false, true
	}
	if err := writeUnitAt(path, want); err != nil {
		fmt.Fprintln(out, "could not write the service file:", err)
		return false, false
	}
	if err := runSystemctl("daemon-reload"); err != nil {
		fmt.Fprintln(out, "systemctl --user daemon-reload failed:", err)
		return true, false
	}
	if on, _ := autostartInstalledLinux(); on {
		if err := runSystemctl("reenable", "ccbabysitter"); err != nil {
			fmt.Fprintln(out, "systemctl --user reenable ccbabysitter failed:", err)
			return true, false
		}
		flushStartLinks(path)
	}
	return true, true
}

// displayVariablesSet names the display variables set in this process,
// which a start from a desktop terminal hands to the user manager so the
// background copy sees the desktop, as a desktop session itself does.
func displayVariablesSet() []string {
	var names []string
	for _, name := range []string{"DISPLAY", "WAYLAND_DISPLAY", "XAUTHORITY"} {
		if os.Getenv(name) != "" {
			names = append(names, name)
		}
	}
	return names
}

// importDisplay hands the display variables set here to the user manager,
// for a desktop unit only, and never from an ssh session, whose forwarded
// display would outlive the connection and would make a server's copy act
// as a desktop's. Failing only means the background copy may not see the
// desktop until the next login, so it is reported and the start goes on.
func importDisplay(out io.Writer, desktop bool) {
	if !desktop || os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return
	}
	names := displayVariablesSet()
	if len(names) == 0 {
		return
	}
	if err := runSystemctl(append([]string{"import-environment"}, names...)...); err != nil {
		fmt.Fprintln(out, "systemctl --user import-environment failed:", err)
	}
}

// Start hands a desktop unit the display variables, then starts the unit,
// which leaves one already running alone. It does not enable it.
func (systemdControl) Start(out io.Writer, desktop bool) bool {
	importDisplay(out, desktop)
	if err := runSystemctl("start", "ccbabysitter"); err != nil {
		fmt.Fprintln(out, "systemctl --user start ccbabysitter failed:", err)
		return false
	}
	return true
}

// Restart hands a desktop unit the display variables, then stops the
// service and starts it again, as the unit says now.
func (systemdControl) Restart(out io.Writer, desktop bool) bool {
	importDisplay(out, desktop)
	if err := runSystemctl("restart", "ccbabysitter"); err != nil {
		fmt.Fprintln(out, "systemctl --user restart ccbabysitter failed:", err)
		return false
	}
	return true
}

// Enable enables and starts the unit, which leaves one already running
// alone.
func (systemdControl) Enable(out io.Writer) bool {
	if err := runSystemctl("enable", "--now", "ccbabysitter"); err != nil {
		fmt.Fprintln(out, "systemctl --user enable --now ccbabysitter failed:", err)
		return false
	}
	if unit, err := systemdUnitPath(); err == nil {
		flushStartLinks(unit)
	}
	return true
}

// Lingering asks loginctl whether lingering is on for user. It fails when
// loginctl is missing or does not know the user, and then nothing is
// assumed either way.
func (systemdControl) Lingering(user string) (bool, error) {
	text, err := exec.Command("loginctl", "show-user", user, "-p", "Linger").Output()
	if err != nil {
		return false, err
	}
	return lingerFromShowUser(string(text))
}

// SetLingering turns lingering on or off for user with loginctl.
func (systemdControl) SetLingering(user string, on bool) error {
	verb := "disable-linger"
	if on {
		verb = "enable-linger"
	}
	return exec.Command("loginctl", verb, user).Run()
}

// runInstall sets CC Babysitter up as a systemd user service that starts
// at boot and keeps running after the user logs out, or starts the one
// already set up, then says how to reach its page. It does this whether or
// not the machine has a display, and a service manager that does not
// answer shows up as the systemctl step that failed, never as a run in
// this terminal.
func runInstall(out io.Writer) int {
	o := launchOptions{Install: true, Headless: hosts.Headless(), NoOpen: true}
	return runLauncher(out, newServiceControl(), state.DefaultDir(), o, realLaunchDeps())
}

// runUninstall reverses install: it stops and disables the service,
// removes its unit file, and turns lingering back off when CC Babysitter
// turned it on.
func runUninstall(out io.Writer) int {
	// A unit written before it said KillMode=process would take Claude's
	// background sessions down with the service, so it is brought up to
	// date, and read again by systemd, before the service is stopped. This
	// is best effort: whatever fails here, uninstall goes on.
	if serviceInstalled() {
		_, _ = systemdControl{}.RefreshUnit(io.Discard, !hosts.Headless())
	}
	if err := runSystemctl("disable", "--now", "ccbabysitter"); err != nil {
		fmt.Fprintln(out, "systemctl --user disable --now ccbabysitter failed:", err)
	} else {
		fmt.Fprintln(out, "Stopped and disabled the ccbabysitter service.")
	}

	path, err := removeUnit()
	if err != nil {
		fmt.Fprintln(out, "could not remove the service file:", err)
		return 1
	}
	fmt.Fprintln(out, "Removed", path)

	if err := runSystemctl("daemon-reload"); err != nil {
		fmt.Fprintln(out, "systemctl --user daemon-reload failed:", err)
	}

	releaseLingering(out, systemdControl{}, state.DefaultDir(), currentUser())
	fmt.Fprintln(out, foregroundHint)
	return 0
}

// foregroundHint is how to run CC Babysitter after uninstall without a
// plain run setting the service up again.
const foregroundHint = "To run CC Babysitter only while a terminal stays open, start it with: ccbabysitter --foreground"
