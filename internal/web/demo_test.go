package web

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/state"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
)

func newTestDemo(t *testing.T) (*DemoEngine, chan time.Time) {
	t.Helper()
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tickCh := make(chan time.Time)
	d := newDemoEngine(log, tickCh)
	t.Cleanup(d.Close)
	return d, tickCh
}

func advance(d *DemoEngine, tickCh chan time.Time, n int) {
	for i := 0; i < n; i++ {
		tickCh <- time.Time{}
		d.waitTick()
	}
}

func demoWatch(t *testing.T, d *DemoEngine, id string) supervise.WatchView {
	t.Helper()
	for _, w := range d.View().Watches {
		if w.SessionID == id {
			return w
		}
	}
	t.Fatalf("no watch for %s", id)
	return supervise.WatchView{}
}

// The scripted world shows every watch state, the sessions that are not
// babysat, and the Not running list, so the whole page can be looked at.
func TestDemoShowsEveryState(t *testing.T) {
	d, _ := newTestDemo(t)
	v := d.View()

	states := map[supervise.WatchState]bool{}
	for _, w := range v.Watches {
		states[w.State] = true
		if !claude.ValidID(w.SessionID) {
			t.Fatalf("demo id is not a valid uuid: %s", w.SessionID)
		}
	}
	for _, want := range []supervise.WatchState{supervise.StateWatching, supervise.StateInBackground, supervise.StateStarting, supervise.StateStuck} {
		if !states[want] {
			t.Errorf("no watch is %s: %+v", want, states)
		}
	}
	byID := map[string]supervise.SessionView{}
	for _, s := range v.Sessions {
		byID[s.ID] = s
	}
	if byID[demoDesktopID].Host != claude.HostDesktop || byID[demoBackgroundID].Host != claude.HostBackground {
		t.Fatalf("%+v", v.Sessions)
	}
	if byID[demoTerminalID].RemoteControl || byID[demoTerminalID].FallbackWarning != supervise.FallbackHome {
		t.Fatalf("the terminal session has Remote Control off and sits in the home folder: %+v", byID[demoTerminalID])
	}
	if byID[demoOtherID].Actionable {
		t.Fatal("the other program's session must not be actionable")
	}
	if len(v.NotRunning) != 4 {
		t.Fatalf("%+v", v.NotRunning)
	}
	attach := 0
	for _, p := range v.NotRunning {
		if !claude.ValidID(p.ID) || !strings.HasPrefix(p.Cwd, "~/") || p.ResumeCmd != hosts.ResumeCommandIn(p.Cwd, p.ID) || !strings.HasPrefix(p.ResumeCmd, "cd ") {
			t.Fatalf("%+v", p)
		}
		if p.AttachCmd != "" {
			attach++
			if p.AttachCmd != hosts.AttachCommand(p.ShortID) || p.HandedBackTo != "" || p.SSHAttachCmd != "" {
				t.Fatalf("a stopped background session is started again with its attach command: %+v", p)
			}
		}
	}
	if attach != 1 {
		t.Fatalf("one conversation is a stopped background session: %+v", v.NotRunning)
	}
	if !v.KeepAwake.Held || v.KeepAwake.ActiveWatches != 5 {
		t.Fatalf("five watches keep the computer awake, the stuck one does not: %+v", v.KeepAwake)
	}
}

func TestDemoStatsDriftEachTick(t *testing.T) {
	d, tickCh := newTestDemo(t)
	before := demoWatch(t, d, demoVSCodeID).Stats.InputTokens
	advance(d, tickCh, 1)
	if after := demoWatch(t, d, demoVSCodeID).Stats.InputTokens; after <= before {
		t.Fatalf("stats did not drift: before=%d after=%d", before, after)
	}
}

// The Starting watch has its copy running after a few ticks, and the VS
// Code session's app closes later on and goes the same way.
func TestDemoWalksThroughTheStates(t *testing.T) {
	d, tickCh := newTestDemo(t)
	advance(d, tickCh, demoStartTakes)
	if w := demoWatch(t, d, demoStartingID); w.State != supervise.StateInBackground || w.AttachCmd == "" {
		t.Fatalf("%+v", w)
	}

	advance(d, tickCh, demoAppClosesAfter-demoStartTakes)
	if w := demoWatch(t, d, demoVSCodeID); w.State != supervise.StateStarting {
		t.Fatalf("the app closed: %+v", w)
	}
	advance(d, tickCh, demoStartTakes)
	w := demoWatch(t, d, demoVSCodeID)
	if w.State != supervise.StateInBackground || w.OriginHost != claude.HostVSCode || !w.CanStop {
		t.Fatalf("%+v", w)
	}
	found := false
	for _, e := range d.log.Recent(20, "") {
		if e.Automatic && e.Reason == "host process exited" && e.Session == "shop-api" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected an automatic activity entry for the copy starting")
	}
}

func TestDemoBabysitAddsAWatch(t *testing.T) {
	d, _ := newTestDemo(t)
	if res := d.Babysit(demoTerminalID, false, supervise.ViaPage); !res.OK || !strings.HasSuffix(res.Message, supervise.FallbackHome) {
		t.Fatalf("%+v", res)
	}
	if w := demoWatch(t, d, demoTerminalID); w.State != supervise.StateWatching || w.FallbackWarning != supervise.FallbackHome {
		t.Fatalf("%+v", w)
	}
	if res := d.Babysit(demoOtherID, false, supervise.ViaPage); res.OK {
		t.Fatal("another program's session must be refused")
	}
}

// Unbabysit and Stop follow the same rules as the real engine.
func TestDemoActionsFollowTheState(t *testing.T) {
	d, _ := newTestDemo(t)
	if res := d.Unbabysit(demoCarriedID, supervise.ViaPage); res.OK {
		t.Fatalf("In background refuses unbabysit: %+v", res)
	}
	if res := d.Unbabysit(demoStartingID, supervise.ViaPage); res.OK {
		t.Fatalf("Starting refuses unbabysit: %+v", res)
	}
	if res := d.Stop(demoBackgroundID, supervise.ViaPage); res.OK || res.Message != supervise.StopRefused {
		t.Fatalf("a session that is not babysat is not stopped with a display: %+v", res)
	}
	res := d.Stop(demoCarriedID, supervise.ViaPage)
	if !res.OK || res.Message != "Stopped. Open it again any time from Not running." {
		t.Fatalf("%+v", res)
	}
	v := d.View()
	if len(v.NotRunning) != 5 || v.NotRunning[0].ID != demoCarriedID {
		t.Fatalf("a stopped session goes to the top of Not running: %+v", v.NotRunning)
	}
	if res := d.Unbabysit(demoStuckID, supervise.ViaPage); !res.OK {
		t.Fatalf("Stuck allows Stop watching: %+v", res)
	}
	if res := d.Unbabysit(demoVSCodeID, supervise.ViaPage); !res.OK {
		t.Fatalf("Watching allows unbabysit: %+v", res)
	}
}

// Unbabysit says where the session is from what it saw under the lock, so
// the ticking world may change that session the moment the lock is let go.
// Here the session is babysat again and its app closes while Unbabysit is
// still finishing, and the race detector watches the two goroutines.
func TestDemoUnbabysitSaysWhatItSawUnderTheLock(t *testing.T) {
	d, tickCh := newTestDemo(t)
	advance(d, tickCh, demoAppClosesAfter-1)

	unlocked := make(chan struct{}, 1)
	d.SetNotify(func() {
		select {
		case unlocked <- struct{}{}:
		default:
		}
	})
	done := make(chan supervise.Result, 1)
	go func() { done <- d.Unbabysit(demoVSCodeID, supervise.ViaPage) }()
	<-unlocked
	d.SetNotify(nil)

	if res := d.Babysit(demoVSCodeID, false, supervise.ViaPage); !res.OK {
		t.Fatalf("%+v", res)
	}
	advance(d, tickCh, 1)
	res := <-done
	if !res.OK || res.Message != "Stopped babysitting shop-api. It is now in VS Code." {
		t.Fatalf("%+v", res)
	}
	if w := demoWatch(t, d, demoVSCodeID); w.State != supervise.StateStarting {
		t.Fatalf("the app closed after the second babysit: %+v", w)
	}
}

func TestDemoSettingsAreStored(t *testing.T) {
	d, _ := newTestDemo(t)
	res := d.SetSettings(state.Settings{KeepAwakeMode: "off", Theme: "auto"}, supervise.ViaPage)
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	if d.View().Settings.Theme != "auto" {
		t.Fatal("settings were not stored")
	}
	if d.View().Settings.KeepAwakeMode != "babysitting" {
		t.Fatalf("the keep-awake rule is fixed: %q", d.View().Settings.KeepAwakeMode)
	}
}

// The demonstration offers the same three themes as the real engine, and
// its CLI is logged in, so it never shows the not logged in line.
func TestDemoThemesAndLogin(t *testing.T) {
	d, _ := newTestDemo(t)
	if !d.View().Settings.AutoBabysit {
		t.Fatalf("%+v", d.View().Settings)
	}
	for _, theme := range []string{"dark", "light", "auto"} {
		if res := d.SetSettings(state.Settings{Theme: theme}, supervise.ViaPage); !res.OK || d.View().Settings.Theme != theme {
			t.Fatalf("%s: %+v", theme, res)
		}
	}
	if res := d.SetSettings(state.Settings{Theme: "system"}, supervise.ViaPage); res.OK {
		t.Fatal("the old Follow system value is not one of the themes any more")
	}
	if d.View().Env.CLILoggedIn != hosts.LoginYes {
		t.Fatalf("%+v", d.View().Env)
	}
}

func TestDemoNotifyFiresAfterEveryChange(t *testing.T) {
	d, _ := newTestDemo(t)
	calls := 0
	d.SetNotify(func() { calls++ })
	d.Babysit(demoDesktopID, false, supervise.ViaPage)
	if calls != 1 {
		t.Fatalf("want 1 notify call, got %d", calls)
	}
	d.ResumeWatch(demoStuckID, supervise.ViaPage)
	if calls != 2 {
		t.Fatalf("want 2 notify calls, got %d", calls)
	}
}

// The demo follows the same rules for start at login and for attaching.
func TestDemoStartAtLoginAndAttach(t *testing.T) {
	d, _ := newTestDemo(t)
	res := d.Babysit(demoDesktopID, true, supervise.ViaPage)
	if !res.OK || !strings.HasSuffix(res.Message, " Start at login is on.") || !d.View().Settings.Autostart {
		t.Fatalf("%+v", res)
	}
	for _, s := range d.View().Sessions {
		if (s.Host == claude.HostBackground) != (s.AttachCmd != "") {
			t.Fatalf("attach is offered for background sessions only: %+v", s)
		}
	}
	for _, w := range d.View().Watches {
		if w.CanUnbabysit != (w.State == supervise.StateWatching || w.State == supervise.StateStuck) {
			t.Fatalf("%+v", w)
		}
	}
}

// The scripted world has one In background card for each app a session
// can come from, went to the background today, yesterday and before that,
// and one of them without a Remote Control address.
func TestDemoShowsOneBackgroundCardPerOrigin(t *testing.T) {
	d, _ := newTestDemo(t)
	now := time.Now()
	day := func(t time.Time) time.Time { y, m, dd := t.Date(); return time.Date(y, m, dd, 0, 0, 0, 0, time.Local) }
	today := day(now)
	byOrigin := map[claude.Host]supervise.WatchView{}
	for _, w := range d.View().Watches {
		if w.State == supervise.StateInBackground {
			if _, twice := byOrigin[w.OriginHost]; twice {
				t.Fatalf("two In background cards from %s", w.OriginHost)
			}
			byOrigin[w.OriginHost] = w
		}
	}
	whens := map[string]bool{}
	for _, origin := range []claude.Host{claude.HostDesktop, claude.HostVSCode, claude.HostTerminal} {
		w, ok := byOrigin[origin]
		if !ok {
			t.Fatalf("no In background card from %s", origin)
		}
		since, err := time.Parse(time.RFC3339, w.BackgroundSince)
		if err != nil || since.After(now) {
			t.Fatalf("%s: %q %v", origin, w.BackgroundSince, err)
		}
		switch at := day(since.Local()); {
		case at.Equal(today):
			whens["today"] = true
		case at.Equal(today.AddDate(0, 0, -1)):
			whens["yesterday"] = true
		default:
			whens["older"] = true
		}
		if w.ResumeCmd != hosts.ResumeCommandIn(w.Cwd, w.SessionID) || w.AttachCmd == "" {
			t.Fatalf("%+v", w)
		}
	}
	if len(whens) != 3 {
		t.Fatalf("the cards went to the background today, yesterday and before: %v", whens)
	}
	if !strings.HasPrefix(byOrigin[claude.HostDesktop].RemoteURL, "https://claude.ai/code/session_") || byOrigin[claude.HostVSCode].RemoteURL != "" {
		t.Fatalf("desktop has a Remote Control address and VS Code has none: %+v %+v", byOrigin[claude.HostDesktop], byOrigin[claude.HostVSCode])
	}
}

// Open in a terminal answers as the real engine would, and opens nothing.
func TestDemoOpenTerminal(t *testing.T) {
	d, _ := newTestDemo(t)
	if got := d.View().TerminalLabel; got != "Open in Terminal" {
		t.Fatalf("the button names what the message says opened: %q", got)
	}
	if res := d.OpenTerminal(demoCarriedID); !res.OK || res.Message != "Opened Terminal on api-gateway." {
		t.Fatalf("%+v", res)
	}
	if res := d.OpenTerminal(demoDesktopID); res.OK || res.Message != "That session has no background copy running." {
		t.Fatalf("%+v", res)
	}
}

// One running session is open in two apps at once, so the page shows the
// tag that says so, and it is one entry that the totals count twice.
func TestDemoHasASessionOpenInTwoApps(t *testing.T) {
	d, _ := newTestDemo(t)
	v := d.View()
	seen := map[string]bool{}
	var two []supervise.SessionView
	for _, s := range v.Sessions {
		if seen[s.ID] {
			t.Fatalf("%s is listed twice", s.ID)
		}
		seen[s.ID] = true
		if len(s.Live) == 0 || s.Live[0] != s.Host {
			t.Fatalf("a running session is live in its own host first: %+v", s)
		}
		if len(s.Live) > 1 {
			two = append(two, s)
		}
	}
	if len(two) != 1 || !reflect.DeepEqual(two[0].Live, []claude.Host{claude.HostTerminal, claude.HostVSCode}) {
		t.Fatalf("%+v", two)
	}
	processes := 0
	for _, w := range v.Watches {
		processes += len(w.Live)
	}
	for _, s := range v.Sessions {
		processes += len(s.Live)
	}
	if v.Totals.Sessions != processes || v.Totals.Processes != processes || v.Totals.ByHost[claude.HostVSCode] < 2 {
		t.Fatalf("the totals count every process, %d: %+v", processes, v.Totals)
	}
}

// One VS Code session in the scripted world has an AI title that differs
// from the automatic name in its record, so the tooltip can be seen.
func TestDemoShowsAnAITitleOverTheAutomaticName(t *testing.T) {
	d, _ := newTestDemo(t)
	found := false
	for _, s := range d.View().Sessions {
		if s.Host == claude.HostVSCode && s.Name != "" && s.AlsoCalled != "" && s.Name != s.AlsoCalled {
			found = true
		}
	}
	if !found {
		t.Fatalf("%+v", d.View().Sessions)
	}
}

// The way back from the background hands the session back, as the real
// engine does, and the scripted world starts with one handed back.
func TestDemoHandsBackAStoppedSession(t *testing.T) {
	d, _ := newTestDemo(t)
	v := d.View()
	if len(v.NotRunning) == 0 || v.NotRunning[0].HandedBackTo != claude.HostVSCode {
		t.Fatalf("one conversation starts handed back to VS Code: %+v", v.NotRunning)
	}
	if res := d.Stop(demoCarriedID, supervise.ViaPage); !res.OK {
		t.Fatal(res.Message)
	}
	v = d.View()
	if v.NotRunning[0].ID != demoCarriedID || v.NotRunning[0].HandedBackTo != claude.HostDesktop {
		t.Fatalf("%+v", v.NotRunning[0])
	}
}

// A request from the command line is named in Activity, and the demo's
// views carry the process start time a command line client checks.
func TestDemoBabysitFromTheCommandLineSaysSoAndCarriesProcStart(t *testing.T) {
	d, _ := newTestDemo(t)
	var sv supervise.SessionView
	for _, s := range d.View().Sessions {
		if s.ID == demoDesktopID {
			sv = s
		}
	}
	if sv.ProcStart == "" {
		t.Fatalf("an unbabysat running session carries its procStart: %+v", sv)
	}
	if res := d.Babysit(demoDesktopID, false, supervise.ViaCLI); !res.OK {
		t.Fatal(res.Message)
	}
	recent := d.log.Recent(1, "")
	if len(recent) != 1 || !strings.HasSuffix(recent[0].Message, ", from the command line") {
		t.Fatalf("newest entry: %+v", recent)
	}
	if w := demoWatch(t, d, demoDesktopID); w.ProcStart == "" {
		t.Fatalf("the watch carries its procStart: %+v", w)
	}
}

// The demonstration's scheduled task run is refused like a real one.
func TestDemoRefusesToBabysitAScheduledTaskRun(t *testing.T) {
	d, _ := newTestDemo(t)
	res := d.Babysit(demoScheduledID, false, supervise.ViaPage)
	if res.OK || !strings.Contains(res.Message, supervise.ScheduledRunReason) {
		t.Fatalf("%+v", res)
	}
}
