package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/state"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
	"ccbabysitter.dev/ccbabysitter/internal/web"
)

const id1 = "11111111-2222-4333-8444-555555555501"

type engine struct {
	view  supervise.View
	calls []string
}

func (e *engine) View() supervise.View { return e.view }
func (e *engine) act(name, id string, via supervise.Via) supervise.Result {
	e.calls = append(e.calls, name+" "+id+" "+string(via))
	if name == "stop" {
		return supervise.Result{Message: "That session has no background copy running."}
	}
	return supervise.Result{OK: true, Message: name + " done", ShortID: "11111111"}
}
func (e *engine) Babysit(id string, s bool, v supervise.Via) supervise.Result {
	return e.act("babysit", id, v)
}
func (e *engine) Unbabysit(id string, v supervise.Via) supervise.Result {
	return e.act("unbabysit", id, v)
}
func (e *engine) Stop(id string, v supervise.Via) supervise.Result { return e.act("stop", id, v) }
func (e *engine) ResumeWatch(id string, v supervise.Via) supervise.Result {
	return e.act("resume", id, v)
}
func (e *engine) OpenTerminal(string) supervise.Result { return supervise.Result{} }
func (e *engine) Start(dir string, trust bool, v supervise.Via) supervise.Result {
	return supervise.Result{}
}
func (e *engine) SetSettings(s state.Settings, v supervise.Via) supervise.Result {
	e.view.Settings = s
	return supervise.Result{OK: true}
}

func serve(t *testing.T) (*httptest.Server, *engine, string) {
	t.Helper()
	dir := t.TempDir()
	log, err := state.NewLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	key, err := state.PageKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := &engine{view: supervise.View{Version: "0.4.0", Settings: state.DefaultSettings()}}
	ts := httptest.NewServer(web.NewServer(e, log, "0.4.0", key).Handler())
	t.Cleanup(ts.Close)
	if err := state.SavePageURL(dir, ts.URL); err != nil {
		t.Fatal(err)
	}
	holdLock(t, dir)
	return ts, e, dir
}

// holdLock takes dir's single-instance lock the way a running copy does.
// This package's tests never wire a real process check, so any lock that
// is there counts as held by a live copy.
func holdLock(t *testing.T, dir string) {
	t.Helper()
	lock, err := state.Acquire(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lock.Release)
}

func TestFindsTheRunningCopyFromPageURL(t *testing.T) {
	ts, _, dir := serve(t)
	c, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.URL() != ts.URL {
		t.Fatalf("URL = %q, want %q", c.URL(), ts.URL)
	}
	v, err := c.View(context.Background())
	if err != nil || v.Version != "0.4.0" {
		t.Fatalf("View = %+v, %v", v, err)
	}
}

func TestNoPageURLIsNotRunning(t *testing.T) {
	if _, err := New(t.TempDir(), ""); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

func TestFindStaleAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := "http://" + ln.Addr().String()
	ln.Close()
	dir := t.TempDir()
	if err := state.SavePageURL(dir, addr); err != nil {
		t.Fatal(err)
	}
	holdLock(t, dir)
	c, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.View(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

// A page address left behind by a copy that is no longer running may name
// a port another account's server now listens on. Without a live copy
// holding the folder's lock, the saved address is not used at all, so the
// folder's key is never sent there.
func TestStalePageURLSendsNothing(t *testing.T) {
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+" "+r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"version":"0.4.0"}`))
	}))
	t.Cleanup(ts.Close)

	for name, setup := range map[string]func(t *testing.T, dir string){
		"no lock": func(*testing.T, string) {},
		"a lock whose process is gone": func(t *testing.T, dir string) {
			holdLock(t, dir)
			state.SetPIDChecker(func(int, int64) bool { return false })
			t.Cleanup(func() { state.SetPIDChecker(func(int, int64) bool { return true }) })
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := state.PageKey(dir); err != nil {
				t.Fatal(err)
			}
			if err := state.SavePageURL(dir, ts.URL); err != nil {
				t.Fatal(err)
			}
			setup(t, dir)
			c, err := New(dir, "")
			if err == nil {
				_, err = c.View(context.Background())
			}
			if !errors.Is(err, ErrNotRunning) {
				t.Fatalf("err = %v, want ErrNotRunning", err)
			}
			if len(seen) != 0 {
				t.Fatalf("the saved address was asked: %q", seen)
			}
		})
	}

	// An address the person gives stands, but without a live copy holding
	// the folder's lock the folder's key does not go with it.
	dir := t.TempDir()
	if _, err := state.PageKey(dir); err != nil {
		t.Fatal(err)
	}
	if err := state.SavePageURL(dir, ts.URL); err != nil {
		t.Fatal(err)
	}
	c, err := New(dir, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	c.View(context.Background())
	if len(seen) != 1 || seen[0] != "/api/state " {
		t.Fatalf("seen %q", seen)
	}
}

// A --url without a key gets the folder's key only when it is the address
// the live copy saved: any other address may be another account's server,
// or another copy such as a demo, and the key never goes there. What
// arrives is asserted where it arrives, on the server.
func TestBareURLGetsTheKeyOnlyAtTheSavedAddress(t *testing.T) {
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"version":"0.4.0"}`))
	}))
	t.Cleanup(ts.Close)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := "http://" + ln.Addr().String()
	ln.Close()

	for _, tc := range []struct {
		name  string
		saved string
		held  bool
		want  bool
	}{
		{"another address, lock held", elsewhere, true, false},
		{"no saved address, lock held", "", true, false},
		{"the saved address, lock held", ts.URL, true, true},
		{"the saved address, lock not held", ts.URL, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen = nil
			dir := t.TempDir()
			key, err := state.PageKey(dir)
			if err != nil {
				t.Fatal(err)
			}
			if tc.saved != "" {
				if err := state.SavePageURL(dir, tc.saved); err != nil {
					t.Fatal(err)
				}
			}
			if tc.held {
				holdLock(t, dir)
			}
			c, err := New(dir, ts.URL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.View(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := ""
			if tc.want {
				want = "token " + key
			}
			if len(seen) != 1 || seen[0] != want {
				t.Fatalf("Authorization %q, want %q", seen, want)
			}
			if !tc.want && c.KeyedURL() != ts.URL {
				t.Fatalf("KeyedURL %q gives the folder's key away", c.KeyedURL())
			}
		})
	}

	// A key in the address is the one sent, whatever the folder holds.
	seen = nil
	dir := t.TempDir()
	if _, err := state.PageKey(dir); err != nil {
		t.Fatal(err)
	}
	holdLock(t, dir)
	given := strings.Repeat("ef", 32)
	c, err := New(dir, ts.URL+"/?token="+given)
	if err != nil {
		t.Fatal(err)
	}
	c.View(context.Background())
	if len(seen) != 1 || seen[0] != "token "+given {
		t.Fatalf("Authorization %q, want the given key", seen)
	}
}

func otherProgram(t *testing.T, status int, body string) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestNotCCBabysitterHTML(t *testing.T) {
	c, err := New(t.TempDir(), otherProgram(t, 200, "<html>some other program</html>"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.View(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

func TestNotCCBabysitterJSON404(t *testing.T) {
	c, err := New(t.TempDir(), otherProgram(t, 404, `{"message":"not found"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.View(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

func TestURLMustBeLoopbackPageAddress(t *testing.T) {
	key := strings.Repeat("ab", 32)
	for _, bad := range []string{
		"http://example.com:80", "https://127.0.0.1:1", "http://127.0.0.1:1/x", "http://localhost:1", "127.0.0.1:1",
		"http://127.0.0.1:1/?token=short", "http://127.0.0.1:1/?token=" + strings.Repeat("z", 64),
		"http://127.0.0.1:1/x?token=" + key, "http://127.0.0.1:1/?token=" + key + "&x=1",
		"http://example.com:1/?token=" + key, "http://127.0.0.1:1/?token=" + key + "#f",
		"http://127.0.0.1:1/?token=" + key + "&token=" + key,
		"http://user@127.0.0.1:1/?token=" + key, "http://user:pw@127.0.0.1:1/?token=" + key,
	} {
		_, err := New(t.TempDir(), bad)
		if err == nil || errors.Is(err, ErrNotRunning) {
			t.Errorf("New(%q) = %v, want a bad address error", bad, err)
			continue
		}
		// The error is shown to the person, and may end up anywhere: it
		// never repeats what was given as a key.
		if strings.Contains(err.Error(), "token=") && !strings.Contains(err.Error(), "token=...") {
			t.Errorf("New(%q) error repeats the key: %v", bad, err)
		}
		if strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "short") {
			t.Errorf("New(%q) error repeats the key: %v", bad, err)
		}
	}
}

// The address with the key, as the banner prints it, works from a state
// folder that has no key, and that key wins over the folder's own.
func TestURLWithTheKey(t *testing.T) {
	ts, _, dir := serve(t)
	key := state.ReadPageKey(dir)
	for _, raw := range []string{ts.URL + "/?token=" + key, ts.URL + "?token=" + key} {
		other := t.TempDir()
		if err := os.WriteFile(filepath.Join(other, "page-key"), []byte(strings.Repeat("cd", 32)), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, d := range []string{t.TempDir(), other} {
			c, err := New(d, raw)
			if err != nil {
				t.Fatal(err)
			}
			if c.URL() != ts.URL || c.KeyedURL() != ts.URL+"/?token="+key {
				t.Fatalf("URL %q, KeyedURL %q", c.URL(), c.KeyedURL())
			}
			if v, err := c.View(context.Background()); err != nil || v.Version != "0.4.0" {
				t.Fatalf("View = %+v, %v", v, err)
			}
		}
	}
}

func TestKeyedURL(t *testing.T) {
	ts, _, dir := serve(t)
	c, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := ts.URL + "/?token=" + state.ReadPageKey(dir); c.KeyedURL() != want {
		t.Fatalf("KeyedURL %q, want %q", c.KeyedURL(), want)
	}
	c, _ = New(t.TempDir(), "http://127.0.0.1:1")
	if c.KeyedURL() != "http://127.0.0.1:1" {
		t.Fatalf("with no key, KeyedURL %q", c.KeyedURL())
	}
}

// A copy that answers without the right key is running: every call says
// it needs the key, not that nothing is there.
func TestWrongOrMissingKeyIsErrNoKey(t *testing.T) {
	ts, _, dir := serve(t)
	if err := os.WriteFile(filepath.Join(dir, "page-key"), []byte(strings.Repeat("cd", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	wrong, _ := New(dir, "")
	missing, _ := New(t.TempDir(), ts.URL)
	ctx := context.Background()
	for name, c := range map[string]*Client{"wrong": wrong, "missing": missing} {
		if _, err := c.View(ctx); !errors.Is(err, ErrNoKey) {
			t.Errorf("%s View err = %v, want ErrNoKey", name, err)
		}
		if _, err := c.Babysit(ctx, id1, false); !errors.Is(err, ErrNoKey) {
			t.Errorf("%s Babysit err = %v, want ErrNoKey", name, err)
		}
		if _, err := c.Activity(ctx, 5); !errors.Is(err, ErrNoKey) {
			t.Errorf("%s Activity err = %v, want ErrNoKey", name, err)
		}
		if _, err := c.SetSettings(ctx, map[string]any{"theme": "dark"}); !errors.Is(err, ErrNoKey) {
			t.Errorf("%s SetSettings err = %v, want ErrNoKey", name, err)
		}
	}
	if ErrNoKey.Error() != web.KeyMessage {
		t.Fatalf("the client and the page word the missing key differently:\n%q\n%q", ErrNoKey, web.KeyMessage)
	}
}

// A 401 from some other program, without CC Babysitter's error shape, is
// not CC Babysitter asking for its key.
func TestOtherProgramsUnauthorizedIsNotRunning(t *testing.T) {
	c, err := New(t.TempDir(), otherProgram(t, 401, "<html>log in</html>"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.View(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

// Every request carries the key as an Authorization header, and never in
// the address.
func TestRequestsCarryTheKeyInAHeader(t *testing.T) {
	dir := t.TempDir()
	key, err := state.PageKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.Header.Get("Authorization")+" "+r.URL.RawQuery)
		w.WriteHeader(500)
	}))
	t.Cleanup(ts.Close)
	if err := state.SavePageURL(dir, ts.URL); err != nil {
		t.Fatal(err)
	}
	holdLock(t, dir)
	c, _ := New(dir, "")
	ctx := context.Background()
	c.View(ctx)
	c.Babysit(ctx, id1, false)
	for _, s := range seen {
		if !strings.Contains(s, " token "+key+" ") || strings.Contains(s, "token="+key) {
			t.Errorf("request %q", s)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("seen %q", seen)
	}
}

func TestActionsCarryViaCLI(t *testing.T) {
	_, e, dir := serve(t)
	c, _ := New(dir, "")
	ctx := context.Background()
	if r, err := c.Babysit(ctx, id1, false); err != nil || !r.OK {
		t.Fatalf("babysit: %+v %v", r, err)
	}
	c.Unbabysit(ctx, id1)
	c.Retry(ctx, id1)
	want := []string{"babysit " + id1 + " cli", "unbabysit " + id1 + " cli", "resume " + id1 + " cli"}
	if strings.Join(e.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %q, want %q", e.calls, want)
	}
}

func TestRefusedIsAResultNotAnError(t *testing.T) {
	_, _, dir := serve(t)
	c, _ := New(dir, "")
	r, err := c.Stop(context.Background(), id1)
	if err != nil || r.OK || r.Message == "" {
		t.Fatalf("stop = %+v, %v; want a refused result", r, err)
	}
}

func TestSetSettingsChangesOneField(t *testing.T) {
	_, e, dir := serve(t)
	c, _ := New(dir, "")
	if r, err := c.SetSettings(context.Background(), map[string]any{"theme": "dark"}); err != nil || !r.OK {
		t.Fatalf("%+v %v", r, err)
	}
	if e.view.Settings.Theme != "dark" || e.view.Settings.AutoBabysit != state.DefaultSettings().AutoBabysit {
		t.Fatalf("settings = %+v", e.view.Settings)
	}
}

func TestBadRequestIsAnError(t *testing.T) {
	_, _, dir := serve(t)
	c, _ := New(dir, "")
	_, err := c.SetSettings(context.Background(), map[string]any{"nope": true})
	var ce *Error
	if !errors.As(err, &ce) || ce.Status != 400 {
		t.Fatalf("err = %v, want a 400 Error", err)
	}
}

func TestActivity(t *testing.T) {
	_, _, dir := serve(t)
	c, _ := New(dir, "")
	if entries, err := c.Activity(context.Background(), 5); err != nil || entries == nil {
		t.Fatalf("%v %v", entries, err)
	}
}

// slowServer accepts requests and holds them until the test ends.
func slowServer(t *testing.T) string {
	t.Helper()
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(ts.Close)
	t.Cleanup(func() { close(release) })
	return ts.URL
}

func TestSlowAnswerIsNoAnswerNotNotRunning(t *testing.T) {
	c, err := New(t.TempDir(), slowServer(t))
	if err != nil {
		t.Fatal(err)
	}
	c.viewTimeout, c.actionTimeout = 50*time.Millisecond, 50*time.Millisecond
	ctx := context.Background()
	if _, err := c.View(ctx); !errors.Is(err, ErrNoAnswer) || errors.Is(err, ErrNotRunning) {
		t.Fatalf("View err = %v, want ErrNoAnswer", err)
	}
	if _, err := c.Babysit(ctx, id1, false); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("Babysit err = %v, want ErrNoAnswer", err)
	}
	if _, err := c.SetSettings(ctx, map[string]any{"theme": "dark"}); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("SetSettings err = %v, want ErrNoAnswer", err)
	}
}

func TestDroppedConnectionIsNoAnswer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			conn.Close()
		}
	}))
	t.Cleanup(ts.Close)
	c, _ := New(t.TempDir(), ts.URL)
	if _, err := c.Stop(context.Background(), id1); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("err = %v, want ErrNoAnswer", err)
	}
}

// Actions wait longer than a view does, since they queue behind claude
// commands that run for up to a minute.
func TestActionsWaitLongerThanViews(t *testing.T) {
	c, _ := New(t.TempDir(), "http://127.0.0.1:1")
	if c.viewTimeout != 10*time.Second || c.actionTimeout != 90*time.Second {
		t.Fatalf("timeouts = %v, %v", c.viewTimeout, c.actionTimeout)
	}
}

func TestRefusedConnectionStaysNotRunning(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := "http://" + ln.Addr().String()
	ln.Close()
	c, _ := New(t.TempDir(), addr)
	if _, err := c.Babysit(context.Background(), id1, false); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	t.Cleanup(target.Close)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/state", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(ts.Close)
	c, _ := New(t.TempDir(), ts.URL)
	if _, err := c.View(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("View err = %v, want ErrNotRunning", err)
	}
	if _, err := c.Babysit(context.Background(), id1, false); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Babysit err = %v, want ErrNotRunning", err)
	}
	if hit {
		t.Fatal("the redirect was followed")
	}
}
