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
	"unsafe"
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

// runSubkey is runKey below HKEY_CURRENT_USER, for the registry API.
const runSubkey = `Software\Microsoft\Windows\CurrentVersion\Run`

// queryRunValue reads the Run value's data through the registry API, as
// the UTF-16 Windows stores, so a path with letters outside ASCII reads
// back exactly as it was written. found is false when there is no such
// value; any other failure is an error, never "off". Tests replace it.
var queryRunValue = func() (data string, found bool, err error) {
	sub, err := syscall.UTF16PtrFromString(runSubkey)
	if err != nil {
		return "", false, err
	}
	var key syscall.Handle
	if err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, sub, 0, syscall.KEY_READ, &key); err != nil {
		if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
			return "", false, nil
		}
		return "", false, err
	}
	defer syscall.RegCloseKey(key)
	name, err := syscall.UTF16PtrFromString(runValue)
	if err != nil {
		return "", false, err
	}
	var kind, size uint32
	if err := syscall.RegQueryValueEx(key, name, nil, &kind, nil, &size); err != nil {
		if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
			return "", false, nil
		}
		return "", false, err
	}
	buf := make([]uint16, size/2+1)
	size = uint32(len(buf) * 2)
	if err := syscall.RegQueryValueEx(key, name, nil, &kind, (*byte)(unsafe.Pointer(&buf[0])), &size); err != nil {
		return "", false, err
	}
	return syscall.UTF16ToString(buf), true, nil
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
// Startup folder script an earlier version wrote goes too, once the Run
// value is written. Start at login is refused without the windowless
// program, as after go install, where a Run value would name a missing
// file. It reports the Run value it set or deleted.
func installAutostartWindows(enable bool) (string, error) {
	name := runKey + `\` + runValue
	if !enable {
		_, found, err := queryRunValue()
		if err != nil {
			return "", err
		}
		if found {
			if out, err := runReg("delete", runKey, "/v", runValue, "/f"); err != nil {
				return "", fmt.Errorf("reg delete failed: %v %s", err, strings.TrimSpace(out))
			}
		}
		return name, removeLegacyScript()
	}
	if !(windowsControl{}).Usable() {
		return "", fmt.Errorf("start at login needs %s beside ccbabysitter.exe, which the install script installs", backgroundExe)
	}
	if err := setRunValue(); err != nil {
		return "", err
	}
	return name, removeLegacyScript()
}

// autostartInstalledWindows reports whether start at login is on: the Run
// value is there, or an earlier version's Startup script still is. A Run
// value that cannot be read is an error, so the saved setting stays.
func autostartInstalledWindows() (bool, error) {
	_, found, err := queryRunValue()
	if err != nil || found {
		return found, err
	}
	path, err := legacyScriptPath()
	if err != nil {
		return false, nil
	}
	return pathPresent(path)
}
