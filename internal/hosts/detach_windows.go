//go:build windows

package hosts

import (
	"os/exec"
	"syscall"
)

// createNewProcessGroup is the Windows CREATE_NEW_PROCESS_GROUP creation
// flag, which lets cmd survive this process exiting instead of receiving
// the same console events.
const createNewProcessGroup = 0x00000200

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// createNoWindow is the Windows CREATE_NO_WINDOW creation flag: a console
// program started with it gets no console window.
const createNoWindow = 0x08000000

// noWindow starts cmd without a console window, for the console helpers
// the windowless background copy runs, such as reg.
func noWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
