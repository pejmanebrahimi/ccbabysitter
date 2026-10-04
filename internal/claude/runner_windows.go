//go:build windows

package claude

import (
	"os/exec"
	"syscall"
)

// createNoWindow is the Windows CREATE_NO_WINDOW creation flag: a console
// program started with it gets no console window.
const createNoWindow = 0x08000000

// noWindow starts cmd without a console window, since the background copy
// that runs the claude CLI has no console of its own to share.
func noWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
