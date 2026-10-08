package power

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

// SleepCause reads, from the power log pmset keeps, why the computer went to
// sleep between from and to and when, as sleepCause says. known is false
// when the log could not be read. Reading the whole log takes a second or
// two, so it is asked once after a sleep.
func SleepCause(ctx context.Context, from, to time.Time) (cause string, at time.Time, known bool) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pmset", "-g", "log")
	// A pmset ended by the timeout is not waited on for long.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", time.Time{}, false
	}
	cause, at = sleepCause(bytes.NewReader(out), from, to)
	return cause, at, true
}

// LidClosed reports whether the lid is closed with closing it putting the
// computer to sleep, as ioreg says.
func LidClosed() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ioreg", "-r", "-k", "AppleClamshellState", "-d", "1").Output()
	return err == nil && lidClosed(string(out))
}
