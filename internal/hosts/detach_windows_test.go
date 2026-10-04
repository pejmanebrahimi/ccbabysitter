//go:build windows

package hosts

import (
	"os/exec"
	"testing"
)

// reg is a console program: started from the windowless background copy
// without this, it would flash a console window on every look.
func TestNoWindowHidesTheConsole(t *testing.T) {
	cmd := exec.Command("reg", "query", `HKCU\Software`)
	noWindow(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 || !cmd.SysProcAttr.HideWindow {
		t.Fatalf("SysProcAttr %+v", cmd.SysProcAttr)
	}
}
