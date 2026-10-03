//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
)

func init() {
	autostartInstaller = installAutostartWindows
	autostartInstalled = autostartInstalledWindows
}

func startupScriptPath() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", errors.New("APPDATA is not set")
	}
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "CCBabysitter.cmd"), nil
}

// installAutostartWindows writes or removes the Startup folder script that
// starts CC Babysitter at login.
func installAutostartWindows(enable bool) (string, error) {
	path, err := startupScriptPath()
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
	content, err := startupCmd(bin)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := writeDurable(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// autostartInstalledWindows reports whether the Startup folder script is
// there.
func autostartInstalledWindows() (bool, error) {
	path, err := startupScriptPath()
	if err != nil {
		return false, err
	}
	return pathPresent(path)
}
