package power

import (
	"strings"
	"testing"
	"time"
)

// A few lines in the shape macOS's power log writes them, with made-up
// times: a lid closed, the short maintenance wakes a Mac makes with its lid
// shut, and the wake when it is opened again.
const powerLog = `2026-10-05 08:40:02 +0300 Wake                	DarkWake to FullWake from Deep Idle [CDNVA] : due to HID Activity Using AC (Charge:80%)
2026-10-05 08:52:13 +0300 Sleep               	Entering Sleep state due to 'Clamshell Sleep':TCPKeepAlive=active Using AC (Charge:80%) 31 secs
2026-10-05 08:52:44 +0300 DarkWake            	DarkWake from Deep Idle [CDNP] : due to smc.sysState.Wake Using AC (Charge:80%)
2026-10-05 08:53:30 +0300 Sleep               	Entering Sleep state due to 'Maintenance Sleep':TCPKeepAlive=active Using AC (Charge:80%) 106 secs
2026-10-05 09:04:10 +0300 Wake                	DarkWake to FullWake from Deep Idle [CDNVA] : due to UserActivity Assertion Using AC (Charge:80%)
2026-10-05 13:00:00 +0300 Sleep               	Entering Sleep state due to 'Software Sleep pid=123':TCPKeepAlive=active Using AC (Charge:80%) 600 secs
2026-10-05 15:00:00 +0300 Sleep               	Entering Sleep state due to 'Low Power Sleep':TCPKeepAlive=active Using Batt (Charge:3%) 600 secs
2026-10-05 17:00:00 +0300 Sleep               	Entering Sleep state due to 'Idle Sleep':TCPKeepAlive=active Using AC (Charge:80%) 600 secs
`

// The first sleep the log has between a little before the computer was
// last seen awake and when it was seen again says why it slept: a closed
// lid, a request to sleep, or a battery running low. Anything else, or no
// such line, says nothing.
func TestSleepCause(t *testing.T) {
	zone := time.FixedZone("", 3*3600)
	at := func(h, m int) time.Time { return time.Date(2026, 10, 5, h, m, 0, 0, zone) }
	for _, c := range []struct {
		name     string
		from, to time.Time
		want     string
	}{
		{"lid, with maintenance wakes after it", at(8, 52), at(9, 4), "lid"},
		{"lid, last seen a little after the log says it slept", at(8, 53), at(9, 4), "lid"},
		{"put to sleep", at(12, 59), at(13, 10), "asked"},
		{"battery", at(14, 59), at(15, 10), "battery"},
		{"idle", at(16, 59), at(17, 10), ""},
		{"no sleep in the log then", at(10, 0), at(11, 0), ""},
		{"in another zone, the same moments", at(8, 52).UTC(), at(9, 4).UTC(), "lid"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := sleepCause(strings.NewReader(powerLog), c.from, c.to); got != c.want {
				t.Fatalf("sleepCause = %q, want %q", got, c.want)
			}
		})
	}
}
