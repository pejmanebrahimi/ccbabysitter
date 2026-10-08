package hosts

import (
	"context"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

// systemPowerStatus is Windows' SYSTEM_POWER_STATUS.
type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

var getSystemPowerStatus = syscall.NewLazyDLL("kernel32.dll").NewProc("GetSystemPowerStatus")

// lidSleeps reports whether closing this computer's lid now puts it to
// sleep: it has a battery, as a laptop does, and the power plan's lid
// action for the power it runs on now is not to do nothing.
func lidSleeps() bool {
	var st systemPowerStatus
	if r, _, _ := getSystemPowerStatus.Call(uintptr(unsafe.Pointer(&st))); r == 0 {
		return false
	}
	// 128 is no system battery and 255 unknown status.
	if st.BatteryFlag == 128 || st.BatteryFlag == 255 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powercfg", "/query", "SCHEME_CURRENT", "SUB_BUTTONS", "LIDACTION")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := cmd.Output()
	return err == nil && windowsLidSleeps(string(out), st.ACLineStatus == 1)
}
