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
2026-10-05 08:52:15 +0300 Wake Requests       	[process=dasd request=SleepService deltaSecs=911 wakeAt=2026-10-05 09:07:26]
2026-10-05 08:52:44 +0300 WakeDetails         	DriverReason:smc.sysState.Wake - DriverDetails:
2026-10-05 08:52:44 +0300 DarkWake            	DarkWake from Deep Idle [CDNP] : due to smc.sysState.Wake Using AC (Charge:80%)
2026-10-05 08:53:30 +0300 Sleep               	Entering Sleep state due to 'Maintenance Sleep':TCPKeepAlive=active Using AC (Charge:80%) 106 secs
2026-10-05 09:04:10 +0300 Wake                	DarkWake to FullWake from Deep Idle [CDNVA] : due to UserActivity Assertion Using AC (Charge:80%)
2026-10-05 13:00:00 +0300 Sleep               	Entering Sleep state due to 'Software Sleep pid=123':TCPKeepAlive=active Using AC (Charge:80%) 600 secs
2026-10-05 13:10:00 +0300 Wake                	Wake from Deep Idle [CDNVA] : due to HID Activity Using AC (Charge:80%)
2026-10-05 15:00:00 +0300 Sleep               	Entering Sleep state due to 'Low Power Sleep':TCPKeepAlive=active Using Batt (Charge:3%) 600 secs
2026-10-05 15:10:00 +0300 Wake                	Wake from Deep Idle [CDNVA] : due to HID Activity Using AC (Charge:80%)
2026-10-05 17:00:00 +0300 Sleep               	Entering Sleep state due to 'Idle Sleep':TCPKeepAlive=active Using AC (Charge:80%) 600 secs
`

// A night with the lid shut as the power log writes it: the lid closes,
// the Mac makes a short maintenance wake, then an hour-long one, goes back
// to sleep, and wakes when the lid opens.
const nightLog = `2026-10-01 21:00:00 +0300 Wake                	DarkWake to FullWake from Deep Idle [CDNVA] : due to HID Activity Using AC (Charge:80%)
2026-10-01 22:30:00 +0300 Sleep               	Entering Sleep state due to 'Clamshell Sleep':TCPKeepAlive=active Using AC (Charge:80%) 31 secs
2026-10-01 22:30:02 +0300 Wake Requests       	[process=dasd request=SleepService deltaSecs=900 wakeAt=2026-10-01 22:45:00]
2026-10-01 22:30:31 +0300 DarkWake            	DarkWake from Deep Idle [CDNP] : due to smc.sysState.Wake Using AC (Charge:80%) 49 secs
2026-10-01 22:31:20 +0300 Sleep               	Entering Sleep state due to 'Maintenance Sleep':TCPKeepAlive=active Using AC (Charge:80%) 820 secs
2026-10-01 22:45:00 +0300 DarkWake            	DarkWake from Deep Idle [CDNP] : due to rtc/SleepService Using AC (Charge:80%) 3622 secs
2026-10-01 23:46:02 +0300 Sleep               	Entering Sleep state due to 'Sleep Service Back to Sleep':TCPKeepAlive=active Using AC (Charge:80%) 26000 secs
2026-10-02 07:00:00 +0300 Wake                	DarkWake to FullWake from Deep Idle [CDNVA] : due to UserActivity Assertion Using AC (Charge:80%)
`

// The sleep that counts is the first one after the computer was last fully
// awake before it was last seen: its reason says why it slept, a closed
// lid, a request to sleep or a battery running low, and its time is when it
// went to sleep. Anything else says no reason, and no sleep line gives no
// time.
func TestSleepCause(t *testing.T) {
	zone := time.FixedZone("", 3*3600)
	at := func(h, m, sec int) time.Time { return time.Date(2026, 10, 5, h, m, sec, 0, zone) }
	night := func(d, h, m, sec int) time.Time { return time.Date(2026, 10, d, h, m, sec, 0, zone) }
	for _, c := range []struct {
		name     string
		log      string
		from, to time.Time
		want     string
		start    time.Time
	}{
		{"lid, with maintenance wakes after it", powerLog, at(8, 53, 29), at(9, 4, 20), "lid", at(8, 52, 13)},
		{"a whole night with the lid shut", nightLog, night(1, 22, 31, 19), night(2, 7, 0, 30), "lid", night(1, 22, 30, 0)},
		{"put to sleep", powerLog, at(12, 59, 0), at(13, 10, 0), "asked", at(13, 0, 0)},
		{"battery", powerLog, at(14, 59, 0), at(15, 10, 0), "battery", at(15, 0, 0)},
		{"idle", powerLog, at(16, 59, 0), at(17, 10, 0), "", at(17, 0, 0)},
		{"a full wake in between leaves the earlier sleep out", powerLog, at(9, 30, 0), at(11, 0, 0), "", time.Time{}},
		{"in another zone, the same moments", powerLog, at(8, 53, 29).UTC(), at(9, 4, 20).UTC(), "lid", at(8, 52, 13)},
	} {
		t.Run(c.name, func(t *testing.T) {
			cause, start := sleepCause(strings.NewReader(c.log), c.from, c.to)
			if cause != c.want || !start.Equal(c.start) {
				t.Fatalf("sleepCause = %q at %v, want %q at %v", cause, start, c.want, c.start)
			}
		})
	}
}

// The lid counts as closed only when closing it puts the computer to
// sleep: a Mac on an external display keeps running with its lid shut.
func TestLidClosed(t *testing.T) {
	for _, c := range []struct {
		out  string
		want bool
	}{
		{"      \"AppleClamshellCausesSleep\" = Yes\n      \"AppleClamshellState\" = Yes\n", true},
		{"      \"AppleClamshellCausesSleep\" = No\n      \"AppleClamshellState\" = Yes\n", false},
		{"      \"AppleClamshellCausesSleep\" = Yes\n      \"AppleClamshellState\" = No\n", false},
		{"", false},
	} {
		if got := lidClosed(c.out); got != c.want {
			t.Errorf("lidClosed(%q) = %v, want %v", c.out, got, c.want)
		}
	}
}
