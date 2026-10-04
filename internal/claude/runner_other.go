//go:build !windows

package claude

import "os/exec"

// noWindow does nothing outside Windows, where a console program started
// from a background process opens no window.
func noWindow(*exec.Cmd) {}
