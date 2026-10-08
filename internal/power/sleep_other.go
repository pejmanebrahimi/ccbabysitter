//go:build !darwin

package power

import (
	"context"
	"time"
)

// SleepCause knows nothing here: this system keeps no log of why it slept
// that CC Babysitter reads.
func SleepCause(ctx context.Context, from, to time.Time) (cause string, at time.Time, known bool) {
	return "", time.Time{}, false
}

// LidClosed is false here: CC Babysitter cannot read the lid on this
// system.
func LidClosed() bool { return false }
