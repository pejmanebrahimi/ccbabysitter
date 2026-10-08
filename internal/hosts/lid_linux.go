package hosts

import (
	"context"
	"os/exec"
	"path/filepath"
	"time"
)

// lidSleeps reports whether closing this computer's lid now puts it to
// sleep: it has a lid, and logind's lid action for how it is now does not
// leave it running. Where logind cannot be asked, its default, suspend,
// counts.
func lidSleeps() bool {
	if lids, _ := filepath.Glob("/proc/acpi/button/lid/*/state"); len(lids) == 0 {
		return false
	}
	ask := func(props ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		args := append([]string{"get-property", "org.freedesktop.login1", "/org/freedesktop/login1", "org.freedesktop.login1.Manager"}, props...)
		out, err := exec.CommandContext(ctx, "busctl", args...).Output()
		return string(out), err
	}
	if out, err := ask("Docked", "OnExternalPower", "HandleLidSwitch", "HandleLidSwitchExternalPower", "HandleLidSwitchDocked"); err == nil {
		return linuxLidSleeps(out)
	}
	if out, err := ask("HandleLidSwitch"); err == nil {
		return linuxLidSleeps(out)
	}
	return true
}
