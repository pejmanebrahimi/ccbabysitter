//go:build windows

package claude

import (
	"os/exec"
	"testing"
)

// The background copy has no console, so every claude command it starts
// would otherwise open a console window of its own.
func TestNoWindowHidesTheConsole(t *testing.T) {
	cmd := exec.Command("claude", "--version")
	noWindow(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 || !cmd.SysProcAttr.HideWindow {
		t.Fatalf("SysProcAttr %+v", cmd.SysProcAttr)
	}
}
