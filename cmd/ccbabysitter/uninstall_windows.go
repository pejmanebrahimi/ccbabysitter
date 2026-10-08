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

	"ccbabysitter.dev/ccbabysitter/internal/procs"
)

// uninstallSystem is what uninstall does on Windows: the install script
// puts the program in a folder of its own and adds that folder to the user
// Path, and a program cannot delete itself while it runs.
func uninstallSystem() uninstallSteps {
	return uninstallSteps{
		companions:  []string{backgroundExe},
		ownFolder:   installerFolder(),
		removeStart: removeStartAtLogin,
		removeFiles: removeFilesAfterExit,
		removePath:  removeFromUserPath,
	}
}

// installerFolder is the folder the install script makes for the program
// by default, its links and junctions resolved as the program's own path
// is, or "" when it is not known.
func installerFolder() string {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return ""
	}
	dir := filepath.Join(local, "Programs", "CCBabysitter")
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return dir
}

// powershell is the Windows PowerShell that comes with Windows.
func powershell() string {
	if root := os.Getenv("SystemRoot"); root != "" {
		return filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	}
	return "powershell.exe"
}

// removeFilesAfterExit leaves the program's removal to a PowerShell with
// no window, which waits for this process to exit. It must outlive the
// terminal or ssh session this command runs in: it leaves their job where
// the job allows it, and is otherwise started by Windows' own process
// service. Where neither works, the program stays, and uninstall says to
// delete it by hand.
func removeFilesAfterExit(out io.Writer, plan removalPlan) (bool, error) {
	startMs, _ := procs.NewReal().CreateTime(os.Getpid())
	encoded := encodePowerShell(windowsRemoveScript(os.Getpid(), startMs, plan))
	dir := os.Getenv("SystemRoot")
	what := plan.folder
	if !plan.whole {
		what = strings.Join(plan.files, ", ")
	}
	// Out of the program's folder, which a process working in it would
	// keep from being deleted.
	helper := exec.Command(powershell(), "-NoProfile", "-NonInteractive", "-EncodedCommand", encoded)
	helper.Dir = dir
	helper.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow | createNewProcessGroup | createBreakawayFromJob}
	started := helper.Start() == nil
	if started {
		_ = helper.Process.Release()
	} else {
		commandLine := syscall.EscapeArg(powershell()) + " -NoProfile -NonInteractive -EncodedCommand " + encoded
		viaService := exec.Command(powershell(), "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(windowsStartOutsideJobScript(commandLine, dir)))
		viaService.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
		started = viaService.Run() == nil
	}
	if !started {
		return false, fmt.Errorf("it cannot be deleted while this command runs: delete %s once it has ended", what)
	}
	fmt.Fprintln(out, "Deleting", what, "once this command ends.")
	return true, nil
}

// removeFromUserPath takes folder out of the user Path, where the install
// script put it. The install script wrote the folder as it found it, and
// folder has its links and junctions resolved, so both ways of writing it
// are taken out.
func removeFromUserPath(out io.Writer, folder string) error {
	folders := []string{folder}
	if exe, err := os.Executable(); err == nil && !strings.EqualFold(filepath.Dir(exe), folder) {
		folders = append(folders, filepath.Dir(exe))
	}
	cmd := exec.Command(powershell(), "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(windowsPathScript(folders...)))
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
