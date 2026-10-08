//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// uninstallSystem is what uninstall does on Windows: the install script
// puts the program in a folder of its own and adds that folder to the user
// Path, and a program cannot delete itself while it runs.
func uninstallSystem() uninstallSteps {
	return uninstallSteps{
		companions:  []string{backgroundExe},
		ownFolder:   "CCBabysitter",
		removeStart: removeStartAtLogin,
		removeFiles: removeFilesAfterExit,
		removePath:  removeFromUserPath,
	}
}

// powershell is the Windows PowerShell that comes with Windows.
func powershell() string {
	if root := os.Getenv("SystemRoot"); root != "" {
		return filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	}
	return "powershell.exe"
}

// removeFilesAfterExit leaves the program's removal to a PowerShell with
// no window, which waits for this process to exit. It runs out of the job
// of the terminal this command runs in where it may, so closing the
// terminal does not end it.
func removeFilesAfterExit(out io.Writer, plan removalPlan) error {
	args := []string{"-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(windowsRemoveScript(os.Getpid(), plan))}
	start := func(flags uint32) error {
		cmd := exec.Command(powershell(), args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags}
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	if err := start(createNoWindow | createNewProcessGroup | createBreakawayFromJob); err != nil {
		if err := start(createNoWindow | createNewProcessGroup); err != nil {
			return err
		}
	}
	what := plan.folder
	if !plan.whole {
		what = strings.Join(plan.files, ", ")
	}
	fmt.Fprintln(out, "Deleting", what, "once this command ends.")
	return nil
}

// removeFromUserPath takes folder out of the user Path, where the install
// script put it.
func removeFromUserPath(out io.Writer, folder string) error {
	cmd := exec.Command(powershell(), "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(windowsPathScript(folder)))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	said, err := cmd.Output()
	if err != nil {
		return err
	}
	if strings.Contains(string(said), "removed") {
		fmt.Fprintln(out, "Took", folder, "out of your user Path. Windows opened from now on no longer have it.")
	}
	return nil
}
