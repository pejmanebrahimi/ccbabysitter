//go:build !windows

package hosts

import (
	"os/exec"
	"syscall"
)

// detach puts cmd in its own process group so it survives this process
// exiting instead of receiving the same signals.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// noWindow does nothing outside Windows, where a console helper started
// from a background process opens no window.
func noWindow(*exec.Cmd) {}
