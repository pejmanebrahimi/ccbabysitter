//go:build !darwin

package power

import "time"

// SleepCause is "" here: this system keeps no log of why it slept that
// CC Babysitter reads.
func SleepCause(from, to time.Time) string { return "" }
