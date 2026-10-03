//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
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

// writeUnit writes the user unit that starts binPath, with the folder of
// the claude CLI found now put on its PATH.
func writeUnit(binPath string) (string, error) {
	path, err := systemdUnitPath()
	if err != nil {
		return "", err
	}
	text, err := unitFile(binPath, claude.FindCLI())
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

func runSystemctl(args ...string) error {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
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

// Install writes the unit and has systemd read it, then enables and
// starts it as Enable does.
func (sc systemdControl) Install(out io.Writer) bool {
	bin, err := resolvedExecutablePath()
	if err != nil {
		fmt.Fprintln(out, "could not resolve this program's own path:", err)
		return false
	}
	if _, err := writeUnit(bin); err != nil {
		fmt.Fprintln(out, "could not write the service file:", err)
		return false
	}
	if err := runSystemctl("daemon-reload"); err != nil {
		fmt.Fprintln(out, "systemctl --user daemon-reload failed:", err)
		return false
	}
	return sc.Enable(out)
}

// RefreshUnit writes the unit again when it is not the one this program
// would write now, naming this program where it is now and the claude CLI
// found now, and has systemd read it again.
func (systemdControl) RefreshUnit(out io.Writer) (rewritten, ok bool) {
	bin, err := resolvedExecutablePath()
	if err != nil {
		fmt.Fprintln(out, "could not resolve this program's own path:", err)
		return false, false
	}
	path, err := systemdUnitPath()
	if err != nil {
		fmt.Fprintln(out, "could not find the service file:", err)
		return false, false
	}
	want, err := unitFile(bin, claude.FindCLI())
	if err != nil {
		fmt.Fprintln(out, "could not write the service file:", err)
		return false, false
	}
	if unitUpToDate(path, want) {
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
	return true, true
}

// Restart stops the service and starts it again, as the unit says now.
func (systemdControl) Restart(out io.Writer) bool {
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
// not the machine has a display.
func runInstall(out io.Writer) int {
	return installService(out, newServiceControl(), state.DefaultDir(), waitForService)
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
		_, _ = systemdControl{}.RefreshUnit(io.Discard)
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
const foregroundHint = "To run CC Babysitter only while a terminal stays open, start it with: ccbabysitter --no-open"
