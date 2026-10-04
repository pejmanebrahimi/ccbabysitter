//go:build darwin

package main

import (
	"os"
	"path/filepath"
)

func init() {
	autostartInstaller = installAutostartDarwin
	autostartInstalled = autostartInstalledDarwin
}

func launchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", "com.ccbabysitter.plist"), nil
}

// installAutostartDarwin turns start at login on or off by moving the
// LaunchAgent's plist: into ~/Library/LaunchAgents, which launchd loads at
// every login, or back to the state folder, which only the launcher loads.
// Moving it never unloads the running job, so CC Babysitter keeps running
// either way. With no plist anywhere yet, on writes one in LaunchAgents.
// It reports the LaunchAgent it wrote or removed, which is what changed.
func installAutostartDarwin(enable bool) (string, error) {
	from, to := offPath(), agentPath()
	if !enable {
		from, to = to, from
	}
	text, err := os.ReadFile(from)
	switch {
	case err == nil:
	case os.IsNotExist(err) && enable:
		plist, err := launchdPlist()
		if err != nil {
			return "", err
		}
		text = []byte(plist)
	case os.IsNotExist(err):
		return from, nil
	default:
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return "", err
	}
	if err := writeDurable(to, text, 0o644); err != nil {
		return "", err
	}
	if err := removeDurable(from); err != nil {
		return "", err
	}
	return agentPath(), nil
}

// autostartInstalledDarwin reports whether the LaunchAgent is there.
func autostartInstalledDarwin() (bool, error) {
	path, err := launchAgentPath()
	if err != nil {
		return false, err
	}
	return pathPresent(path)
}
