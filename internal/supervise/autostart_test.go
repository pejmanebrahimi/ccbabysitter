package supervise

import (
	"errors"
	"sync"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/buildinfo"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// loginItem stands in for the real login item: installed is what a look
// at the machine finds, err is what that look fails with, and looks and
// writes count how often each was asked for.
type loginItem struct {
	mu        sync.Mutex
	installed bool
	err       error
	looks     int
	writes    int
}

func (l *loginItem) look() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.looks++
	return l.installed, l.err
}

func (l *loginItem) write(enable bool) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writes++
	l.installed = enable
	return "/home/dev/.config/autostart/ccbabysitter", nil
}

func (l *loginItem) counts() (int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.looks, l.writes
}

func (l *loginItem) set(installed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.installed = installed
}

// autostartFixture saves settings with start at login as saved, wires the
// login item in, and puts the clock in the test's hands. The loop is not
// started, so every look after the first is one the test drove.
func autostartFixture(t *testing.T, saved bool, item *loginItem) (*fixture, *time.Time) {
	t.Helper()
	f := newFixture(t, nil)
	settings := state.DefaultSettings()
	settings.Autostart = saved
	if err := f.d.Store.Save(state.State{Version: buildinfo.Version, Settings: settings}); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	f.d.Now = func() time.Time { return clock }
	if item != nil {
		f.d.AutostartInstalled = item.look
		f.d.Autostart = item.write
	}
	return f, &clock
}

func linesSaying(f *fixture, msg string) int {
	n := 0
	for _, e := range f.d.Log.Recent(500, "") {
		if e.Message == msg {
			n++
		}
	}
	return n
}

const (
	turnedOffOutside = "Start at login was turned off outside CC Babysitter."
	turnedOnOutside  = "Start at login was turned on outside CC Babysitter."
	serviceOn        = "Set up as a service that starts at boot."
	serviceOff       = "The service that starts CC Babysitter at boot was turned off outside CC Babysitter."
)

// On a server the login item is CC Babysitter's own systemd service, which
// installing it sets up, so finding it there is not an outside change.
func TestOnAServerTheServiceIsNotAnOutsideChange(t *testing.T) {
	item := &loginItem{installed: true}
	f, _ := autostartFixture(t, false, item)
	headless(f)
	s := New(*f.d)

	if !s.View().Settings.Autostart {
		t.Fatal("the setting must follow the machine")
	}
	if n := linesSaying(f, turnedOnOutside); n != 0 {
		t.Fatalf("a server must not say start at login was turned on outside, got %d lines", n)
	}
	if n := linesSaying(f, serviceOn); n != 1 {
		t.Fatalf("want one line saying the service is set up, got %d", n)
	}
}

func TestOnAServerAServiceTurnedOffIsSaidInServerWords(t *testing.T) {
	item := &loginItem{installed: false}
	f, _ := autostartFixture(t, true, item)
	headless(f)
	s := New(*f.d)

	if s.View().Settings.Autostart {
		t.Fatal("the setting must follow the machine")
	}
	if n := linesSaying(f, turnedOffOutside); n != 0 {
		t.Fatalf("a server does not speak of start at login, got %d lines", n)
	}
	if n := linesSaying(f, serviceOff); n != 1 {
		t.Fatalf("want one line saying the service was turned off, got %d", n)
	}
}

func TestALoginItemRemovedByHandTurnsTheSettingOff(t *testing.T) {
	item := &loginItem{installed: false}
	f, _ := autostartFixture(t, true, item)
	s := New(*f.d)

	if s.View().Settings.Autostart {
		t.Fatal("the setting must follow the machine")
	}
	if st, _ := f.d.Store.Load(); st.Settings.Autostart {
		t.Fatal("and be saved that way")
	}
	if n := linesSaying(f, turnedOffOutside); n != 1 {
		t.Fatalf("want one line saying so, got %d", n)
	}
	if looks, writes := item.counts(); looks != 1 || writes != 0 {
		t.Fatalf("it only looks: looks %d writes %d", looks, writes)
	}
}

func TestALoginItemAddedByHandTurnsTheSettingOn(t *testing.T) {
	item := &loginItem{installed: true}
	f, _ := autostartFixture(t, false, item)
	s := New(*f.d)

	if !s.View().Settings.Autostart {
		t.Fatal("the setting must follow the machine")
	}
	if st, _ := f.d.Store.Load(); !st.Settings.Autostart {
		t.Fatal("and be saved that way")
	}
	if n := linesSaying(f, turnedOnOutside); n != 1 {
		t.Fatalf("want one line saying so, got %d", n)
	}
	if _, writes := item.counts(); writes != 0 {
		t.Fatal("it only looks")
	}
}

func TestALookThatFailsChangesNothing(t *testing.T) {
	item := &loginItem{installed: false, err: errors.New("permission denied")}
	f, _ := autostartFixture(t, true, item)
	s := New(*f.d)

	if !s.View().Settings.Autostart {
		t.Fatal("a failed look must leave the setting alone")
	}
	if n := linesSaying(f, turnedOffOutside) + linesSaying(f, turnedOnOutside); n != 0 {
		t.Fatalf("nothing is said, got %d lines", n)
	}
}

func TestNoWayToLookChangesNothing(t *testing.T) {
	f, _ := autostartFixture(t, true, nil)
	s := New(*f.d)

	if !s.View().Settings.Autostart {
		t.Fatal("with no way to look, the setting is left alone")
	}
	if n := linesSaying(f, turnedOffOutside); n != 0 {
		t.Fatalf("nothing is said, got %d lines", n)
	}
}

func TestTheLoginItemIsLookedAtEveryThirtySeconds(t *testing.T) {
	item := &loginItem{installed: true}
	f, clock := autostartFixture(t, true, item)
	s := New(*f.d)
	if looks, _ := item.counts(); looks != 1 {
		t.Fatalf("one look at start, got %d", looks)
	}

	item.set(false)
	*clock = clock.Add(29 * time.Second)
	s.reconcileForTest(f.d.Obs.Current())
	if looks, _ := item.counts(); looks != 1 {
		t.Fatalf("no second look within 30 seconds, got %d", looks)
	}
	if !s.View().Settings.Autostart {
		t.Fatal("nothing has been looked at yet")
	}

	*clock = clock.Add(2 * time.Second)
	s.reconcileForTest(f.d.Obs.Current())
	if looks, _ := item.counts(); looks != 2 {
		t.Fatalf("a second look past 30 seconds, got %d", looks)
	}
	if s.View().Settings.Autostart {
		t.Fatal("the second look finds the item gone")
	}
	if st, _ := f.d.Store.Load(); st.Settings.Autostart {
		t.Fatal("and that is saved")
	}
	if n := linesSaying(f, turnedOffOutside); n != 1 {
		t.Fatalf("want one line, got %d", n)
	}
	if _, writes := item.counts(); writes != 0 {
		t.Fatal("it only looks")
	}
}

func TestTheSwitchStillWritesTheLoginItem(t *testing.T) {
	item := &loginItem{installed: false}
	f, clock := autostartFixture(t, false, item)
	s := New(*f.d)

	next := s.View().Settings
	next.Autostart = true
	if res := s.SetSettings(next, ViaPage); !res.OK {
		t.Fatal(res.Message)
	}
	if _, writes := item.counts(); writes != 1 {
		t.Fatalf("the switch writes the item once, got %d", writes)
	}
	*clock = clock.Add(31 * time.Second)
	s.reconcileForTest(f.d.Obs.Current())
	if !s.View().Settings.Autostart {
		t.Fatal("the item the switch wrote is there, so the setting stays on")
	}
	if n := linesSaying(f, turnedOnOutside) + linesSaying(f, turnedOffOutside); n != 0 {
		t.Fatalf("nothing changed outside, got %d lines", n)
	}
}
