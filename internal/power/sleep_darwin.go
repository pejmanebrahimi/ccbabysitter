package power

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

// SleepCause reads why the computer went to sleep between from and to
// from the power log pmset keeps, as sleepCause says. Reading the whole log
// takes a second or two, so it is asked only once after a sleep.
func SleepCause(from, to time.Time) string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pmset", "-g", "log").Output()
	if err != nil {
		return ""
	}
	return sleepCause(bytes.NewReader(out), from, to)
}
