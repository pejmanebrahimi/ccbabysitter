package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/buildinfo"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// fakeService records what was asked of it and answers from its fields.
type fakeService struct {
	usable, installed, active bool
	writeOK, enableOK         bool
	startOK                   bool
	// unitDesktop and unitKnown are the installed unit's kind, as
	// UnitKind reads it.
	unitDesktop, unitKnown bool
	onWrite                func()
	// staleUnit is whether the installed unit differs from the one this
	// program would write, which RefreshUnit then writes again.
	staleUnit bool
	refreshOK bool
	restartOK bool
	// lingerOn is whether lingering is on; lingerErr makes asking about
	// it fail, and setLingerErr makes changing it fail.
	lingerOn     bool
	lingerErr    error
	setLingerErr error

	calls []string
}

func (f *fakeService) Lingering(user string) (bool, error) {
	f.calls = append(f.calls, "lingering")
	return f.lingerOn, f.lingerErr
}

func (f *fakeService) SetLingering(user string, on bool) error {
	if on {
		f.calls = append(f.calls, "linger on")
	} else {
		f.calls = append(f.calls, "linger off")
	}
	if f.setLingerErr != nil {
		return f.setLingerErr
	}
	f.lingerOn = on
	return nil
}

func (f *fakeService) RefreshUnit(out io.Writer, desktop bool) (bool, bool) {
	f.calls = append(f.calls, "refresh "+kindOf(desktop))
	if !f.refreshOK {
		fmt.Fprintln(out, "could not write the service file: permission denied")
		return false, false
	}
	rewritten := f.staleUnit
	f.staleUnit = false
	return rewritten, true
}

func (f *fakeService) UnitKind() (desktop, known bool) { return f.unitDesktop, f.unitKnown }

func (f *fakeService) Restart(out io.Writer, desktop bool) bool {
	f.calls = append(f.calls, "restart")
	if !f.restartOK {
		fmt.Fprintln(out, "systemctl --user restart ccbabysitter failed: exit status 1")
	}
	return f.restartOK
}

// count is how many times call was made.
func (f *fakeService) count(call string) int {
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

func (f *fakeService) Usable() bool    { f.calls = append(f.calls, "usable"); return f.usable }
func (f *fakeService) Installed() bool { f.calls = append(f.calls, "installed"); return f.installed }
func (f *fakeService) Active() bool    { f.calls = append(f.calls, "active"); return f.active }
func (f *fakeService) Enable(out io.Writer) bool {
	f.calls = append(f.calls, "enable")
	if !f.enableOK {
		fmt.Fprintln(out, "systemctl --user enable --now ccbabysitter failed: exit status 1")
	}
	return f.enableOK
}

// kindOf names a unit's kind the way the fake records it.
func kindOf(desktop bool) string {
	if desktop {
		return "desktop"
	}
	return "server"
}

func (f *fakeService) Start(out io.Writer, desktop bool) bool {
	f.calls = append(f.calls, "start")
	if !f.startOK {
		fmt.Fprintln(out, "systemctl --user start ccbabysitter failed: exit status 1")
	}
	return f.startOK
}

func (f *fakeService) Write(out io.Writer, desktop bool) bool {
	f.calls = append(f.calls, "write "+kindOf(desktop))
	if f.onWrite != nil {
		f.onWrite()
	}
	if !f.writeOK {
		fmt.Fprintln(out, "systemctl --user daemon-reload failed: exit status 1")
	}
	return f.writeOK
}

func (f *fakeService) did(call string) bool {
	for _, c := range f.calls {
		if c == call {
			return true
		}
	}
	return false
}

// answeringAt is a service whose page answers at url with this program's
// own version.
func answeringAt(url string) waitFunc {
	return func(string) (servicePage, bool) { return servicePage{URL: url, Version: buildinfo.Version}, true }
}

// answeringWith is a service whose page answers at url with each version
// in turn, one per wait, and with the last one from then on.
func answeringWith(url string, versions ...string) (waitFunc, *int) {
	waits := 0
	return func(string) (servicePage, bool) {
		v := versions[min(waits, len(versions)-1)]
		waits++
		return servicePage{URL: url, Version: v}, true
	}, &waits
}

func neverAnswering(string) (servicePage, bool) { return servicePage{}, false }

// holdLock takes the state folder's lock the way a running copy does,
// naming this test process, which is alive.
func holdLock(t *testing.T, dir string) {
	t.Helper()
	createMs, ok := procs.NewReal().CreateTime(os.Getpid())
	if !ok {
		t.Skip("cannot read this process's start time here")
	}
	lock, err := state.Acquire(dir, createMs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lock.Release)
}

func TestWaitForPage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/state" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"version":"1.2.3","sessions":[]}`)
	}))
	defer ts.Close()

	dir := t.TempDir()
	if _, ok := waitForPage(dir, 100*time.Millisecond, 10*time.Millisecond, keyedPageState(dir)); ok {
		t.Fatal("no page address saved, so nothing answers")
	}

	if err := state.SavePageURL(dir, ts.URL); err != nil {
		t.Fatal(err)
	}
	if _, ok := waitForPage(dir, 100*time.Millisecond, 10*time.Millisecond, keyedPageState(dir)); ok {
		t.Fatal("an address no live copy holds the lock for is not asked")
	}
	holdLock(t, dir)
	got, ok := waitForPage(dir, time.Second, 10*time.Millisecond, keyedPageState(dir))
	if !ok || got.URL != ts.URL || got.Version != "1.2.3" {
		t.Fatalf("got %+v, %v", got, ok)
	}

	ts.Close()
	if _, ok := waitForPage(dir, 100*time.Millisecond, 10*time.Millisecond, keyedPageState(dir)); ok {
		t.Fatal("a page address nobody serves any more does not answer")
	}
}

// The address may be written only after the wait has begun, as it is
// when the service is still starting up.
func TestWaitForPageKeepsLooking(t *testing.T) {
	dir := t.TempDir()
	looks := 0
	answers := func(url string) (string, bool) { looks++; return "1.2.3", true }
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = state.SavePageURL(dir, "http://127.0.0.1:47391")
	}()
	holdLock(t, dir)
	got, ok := waitForPage(dir, 2*time.Second, 10*time.Millisecond, answers)
	if !ok || got.URL != "http://127.0.0.1:47391" || got.Version != "1.2.3" || looks != 1 {
		t.Fatalf("got %+v, %v, looks %d", got, ok, looks)
	}
}

// A page that answers without saying its version still answers.
func TestPageStateWithoutAVersion(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not json")
	}))
	defer ts.Close()
	if version, ok := pageState(ts.URL, ""); !ok || version != "" {
		t.Fatalf("got %q, %v", version, ok)
	}
}

func TestUnitUpToDate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ccbabysitter.service")
	want := mustUnitFile(t, "/home/dev/.local/bin/ccbabysitter", "/home/dev/.local/bin/claude")
	if unitUpToDate(path, want) {
		t.Fatal("a missing unit is not up to date")
	}
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	if !unitUpToDate(path, want) {
		t.Fatal("the same unit is up to date")
	}
	// One an older version wrote: another ExecStart and no PATH.
	old := strings.Replace(mustUnitFile(t, "/home/dev/.local/bin/ccbabysitter", ""), " --service", " --no-open", 1)
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if unitUpToDate(path, want) {
		t.Fatal("an older unit is not up to date")
	}
	// One written before KillMode=process was added, which would stop
	// Claude's background sessions along with the service.
	if err := os.WriteFile(path, []byte(strings.Replace(want, "KillMode=process\n", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if unitUpToDate(path, want) {
		t.Fatal("a unit without KillMode is not up to date")
	}
	// One naming the program where it used to be.
	if err := os.WriteFile(path, []byte(mustUnitFile(t, "/opt/old/ccbabysitter", "/home/dev/.local/bin/claude")), 0o644); err != nil {
		t.Fatal(err)
	}
	if unitUpToDate(path, want) {
		t.Fatal("a unit naming a moved program is not up to date")
	}
}

// keyedPage serves /api/state with this program's version only to a
// request that carries key, the way CC Babysitter's guard does.
func keyedPage(t *testing.T, key string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/state" || r.Header.Get("Authorization") != "token "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"version":"`+buildinfo.Version+`"}`)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// A page address left behind by a service that crashed may name a port
// another account's server listens on now: until a live copy holds the
// lock again, the wait asks nothing there and keeps waiting.
func TestWaitForPageSendsNoKeyToAStaleAddress(t *testing.T) {
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"version":"1.2.3"}`)
	}))
	defer ts.Close()
	dir := t.TempDir()
	if _, err := state.PageKey(dir); err != nil {
		t.Fatal(err)
	}
	if err := state.SavePageURL(dir, ts.URL); err != nil {
		t.Fatal(err)
	}
	if _, ok := waitForPage(dir, 100*time.Millisecond, 10*time.Millisecond, keyedPageState(dir)); ok || len(seen) != 0 {
		t.Fatalf("ok %v, seen %q", ok, seen)
	}
}
