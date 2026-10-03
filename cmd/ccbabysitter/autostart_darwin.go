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

// installAutostartDarwin writes or removes the LaunchAgent that starts CC
// Babysitter at login. It never calls launchctl: a LaunchAgent written
// here takes effect the next time the user logs in, without needing this
// process to have permission to talk to the running launchd.
func installAutostartDarwin(enable bool) (string, error) {
	path, err := launchAgentPath()
	if err != nil {
		return "", err
	}
	if !enable {
		if err := removeDurable(path); err != nil {
			return "", err
		}
		return path, nil
	}
	bin, err := resolvedExecutablePath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := writeDurable(path, []byte(launchAgentPlist(bin)), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// autostartInstalledDarwin reports whether the LaunchAgent is there.
func autostartInstalledDarwin() (bool, error) {
	path, err := launchAgentPath()
	if err != nil {
		return false, err
	}
	return pathPresent(path)
}
