//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func init() {
	autostartInstaller = installAutostartWindows
	autostartInstalled = autostartInstalledWindows
}

// runKey is the per-user Run key: Windows starts what it names at every
// login, with no admin rights needed to write it.
const runKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

// runValue is the name of CC Babysitter's entry under runKey.
const runValue = "CCBabysitter"

// createNoWindow is the Windows CREATE_NO_WINDOW creation flag: a console
// program started with it gets no console window.
const createNoWindow = 0x08000000

// noWindow starts cmd without a console window, for console helpers the
// windowless background copy runs.
func noWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// runReg runs reg.exe with args and returns what it printed. Tests replace
// it, so they never touch the real registry.
var runReg = func(args ...string) (string, error) {
	cmd := exec.Command("reg", args...)
	noWindow(cmd)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// legacyScriptPath is the Startup folder script an earlier version wrote
// for start at login, which opened a console window at every login.
func legacyScriptPath() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", errors.New("APPDATA is not set")
	}
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "CCBabysitter.cmd"), nil
}

// removeLegacyScript removes that script when it is there.
func removeLegacyScript() error {
	path, err := legacyScriptPath()
	if err != nil {
		return nil
	}
	return removeDurable(path)
}

// runValueWanted is what the Run value says: the windowless program beside
// this one, run as the service. A path holding a double quote cannot be
// quoted for Windows to read back, so it is refused.
func runValueWanted() (string, error) {
	bin, err := executablePath()
	if err != nil {
		return "", err
	}
	background := filepath.Join(filepath.Dir(bin), backgroundExe)
	if strings.Contains(background, `"`) {
		return "", fmt.Errorf("the program's path cannot be quoted for the Run key: %q", background)
	}
	return `"` + background + `" --service`, nil
}

// setRunValue writes the Run value as runValueWanted says.
func setRunValue() error {
	data, err := runValueWanted()
	if err != nil {
		return err
	}
	if out, err := runReg("add", runKey, "/v", runValue, "/t", "REG_SZ", "/d", data, "/f"); err != nil {
		return fmt.Errorf("reg add failed: %v %s", err, strings.TrimSpace(out))
	}
	return nil
}

// installAutostartWindows turns start at login on or off: the Run value,
// which starts the windowless program at login without any window. The
// Startup folder script an earlier version wrote goes either way. It
// reports the Run value it set or deleted.
func installAutostartWindows(enable bool) (string, error) {
	name := runKey + `\` + runValue
	if err := removeLegacyScript(); err != nil {
		return "", err
	}
	if !enable {
		// A value that is not there is already off.
		_, _ = runReg("delete", runKey, "/v", runValue, "/f")
		return name, nil
	}
	if err := setRunValue(); err != nil {
		return "", err
	}
	return name, nil
}

// autostartInstalledWindows reports whether start at login is on: the Run
// value is there, or an earlier version's Startup script still is.
func autostartInstalledWindows() (bool, error) {
	if _, err := runReg("query", runKey, "/v", runValue); err == nil {
		return true, nil
	}
	path, err := legacyScriptPath()
	if err != nil {
		return false, nil
	}
	return pathPresent(path)
}
