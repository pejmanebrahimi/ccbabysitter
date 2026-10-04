//go:build linux

package main

import (
	"path/filepath"

	"ccbabysitter.dev/ccbabysitter/internal/hosts"
)

func init() {
	autostartInstaller = installAutostartLinux
	autostartInstalled = autostartInstalledLinux
}

// installAutostartLinux turns start at login on or off for the systemd
// user unit the launcher and the install subcommand manage, without ever
// touching lingering. On writes the unit, for a desktop or a server as this
// machine is, only when there is none yet, and enables it. Off disables it
// and keeps the unit, so CC Babysitter keeps running until the next restart
// and a plain ccbabysitter can still start it; only uninstall removes the
// unit. It reports the enable link it made or removed, which is what
// changed.
func installAutostartLinux(enable bool) (string, error) {
	unit, err := systemdUnitPath()
	if err != nil {
		return "", err
	}
	if !enable {
		link := enableLink(unit)
		if err := runSystemctl("disable", "ccbabysitter"); err != nil {
			return "", err
		}
		flushStartLinks(unit)
		_ = runSystemctl("daemon-reload")
		return link, nil
	}
	if !serviceInstalled() {
		bin, err := resolvedExecutablePath()
		if err != nil {
			return "", err
		}
		if _, err := writeUnit(bin, !hosts.Headless()); err != nil {
			return "", err
		}
	}
	if err := runSystemctl("daemon-reload"); err != nil {
		return "", err
	}
	if err := runSystemctl("enable", "ccbabysitter"); err != nil {
		return "", err
	}
	flushStartLinks(unit)
	return enableLink(unit), nil
}

// wantsFolders are the targets' wants folders an enable link can be in: a
// desktop unit's and a server unit's.
var wantsFolders = []string{"graphical-session.target.wants", "default.target.wants"}

// wantsFolderFor is the wants folder a desktop's or a server's unit is
// enabled into.
func wantsFolderFor(desktop bool) string {
	if desktop {
		return wantsFolders[0]
	}
	return wantsFolders[1]
}

// enableLink is the enable link systemctl made for unit, in whichever wants
// folder it is, or the desktop one when there is none, which is where a
// desktop unit's link goes.
func enableLink(unit string) string {
	for _, folder := range wantsFolders {
		link := filepath.Join(filepath.Dir(unit), folder, filepath.Base(unit))
		if on, _ := pathPresent(link); on {
			return link
		}
	}
	return filepath.Join(filepath.Dir(unit), wantsFolders[0], filepath.Base(unit))
}

// flushStartLinks flushes the wants folders that exist, and the unit's own
// folder that holds them, after systemctl enabled or disabled the unit.
// systemctl flushes nothing, so without this a power cut right after
// turning start at login on could lose the new link, and CC Babysitter
// would not start at the very boot it was turned on for. A failed flush
// changes nothing that was done, so it is not an error.
func flushStartLinks(unit string) {
	for _, folder := range wantsFolders {
		dir := filepath.Join(filepath.Dir(unit), folder)
		if on, _ := pathPresent(dir); on {
			_ = syncDir(dir)
		}
	}
	_ = syncDir(filepath.Dir(unit))
}

// autostartInstalledLinux reports whether the user unit is enabled, judged
// by the link systemctl enable makes for it in a desktop's or a server's
// wants folder, next to the unit itself. It never runs systemctl, so a look
// is cheap and has no effect.
func autostartInstalledLinux() (bool, error) {
	unit, err := systemdUnitPath()
	if err != nil {
		return false, err
	}
	for _, folder := range wantsFolders {
		on, err := pathPresent(filepath.Join(filepath.Dir(unit), folder, filepath.Base(unit)))
		if err != nil || on {
			return on, err
		}
	}
	return false, nil
}
