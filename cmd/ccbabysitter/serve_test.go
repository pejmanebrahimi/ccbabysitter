package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/buildinfo"
	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
	"ccbabysitter.dev/ccbabysitter/internal/web"
)

func TestListenPreferredFallsBackWhenPortTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	port := taken.Addr().(*net.TCPAddr).Port

	ln, err := listenPreferred(port)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	if addr.Port == port {
		t.Fatalf("expected a different port than the taken one %d", port)
	}
	if addr.IP.String() != "127.0.0.1" {
		t.Fatalf("listener is not on loopback: %v", ln.Addr())
	}
}

// syncWriter lets the serve goroutine and the test goroutine share the
// captured banner output safely.
type syncWriter struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

var pageURLRe = regexp.MustCompile(`Page: (\S+)`)

// waitForPageURL waits for the page URL to appear in the output and returns
// it as the banner prints it, with the page's key.
func waitForPageURL(t *testing.T, out *syncWriter) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := pageURLRe.FindStringSubmatch(out.String()); m != nil {
			return m[1]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("did not see a page URL in the banner:\n%s", out.String())
	return "" // unreachable
}

func TestServeDemoSmoke(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out := &syncWriter{}
	done := make(chan int, 1)
	dirCh := make(chan string, 1)
	go func() {
		done <- serve(ctx, serveOptions{Demo: true, NoOpen: true, Port: 0, onStateDir: func(d string) { dirCh <- d }}, out)
	}()
	stateDir := <-dirCh

	keyed := waitForPageURL(t, out)
	url, _, _ := strings.Cut(keyed, "/?")

	// The page address is saved for another run to find, and it answers
	// the check that run makes, past the page's own guard, with the key
	// saved beside it. The banner prints the address with that key, and the
	// saved address stays the plain one.
	if got := state.PageURL(stateDir); got != url {
		t.Fatalf("saved page address %q, want %q", got, url)
	}
	key := state.ReadPageKey(stateDir)
	if key == "" || keyed != state.KeyedURL(url, key) {
		t.Fatalf("the banner's address %q does not carry the saved key", keyed)
	}
	if !strings.Contains(out.String(), "Page: "+url+"/?token="+key+"\n") {
		t.Fatalf("the banner's Page line has no key:\n%s", out.String())
	}
	if version, ok := pageState(url, key); !ok || version != buildinfo.Version {
		t.Fatalf("the page does not answer the check a later run makes, or not with its version: %q, %v", version, ok)
	}
	if _, ok := pageState(url, ""); ok {
		t.Fatal("the page answered a check without the key")
	}

	resp, err := http.Get(url + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/state without the key = %d, want 401", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, url+"/api/state", nil)
	req.Header.Set("Authorization", "token "+key)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/state = %d", resp.StatusCode)
	}
	var body struct {
		Sessions []any `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Sessions == nil {
		t.Fatal("expected a sessions array")
	}

	cancel()
	select {
	case rc := <-done:
		if rc != 0 {
			t.Fatalf("serve returned %d, want 0", rc)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return within 5s of cancellation")
	}
}

// TestServeReturnsErrorWhenListenerFails exercises the path where the HTTP
// server stops on its own while the caller's context is still live: serve
// must notice, cancel its own inner work, and return a non-zero exit code
// instead of waiting forever on goroutines that would never be told to
// stop. onListen gives the test a way to close the listener out from under
// the server the instant it exists, well before ctx is ever canceled.
func TestServeReturnsErrorWhenListenerFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out := &syncWriter{}
	done := make(chan int, 1)
	go func() {
		done <- serve(ctx, serveOptions{
			Demo:   true,
			NoOpen: true,
			Port:   0,
			onListen: func(ln net.Listener) {
				_ = ln.Close()
			},
		}, out)
	}()

	select {
	case rc := <-done:
		if rc == 0 {
			t.Fatal("expected a non-zero exit code when the listener fails out from under serve")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return within 5s of the listener failing")
	}
}

// linuxServerProbes are the answers of a Linux server reached over ssh
// with the claude CLI on PATH and nothing else installed.
func linuxServerProbes() hosts.Probes {
	return hosts.Probes{
		Platform: "linux",
		LookPath: func(name string) (string, error) {
			if name == "claude" {
				return "/usr/local/bin/claude", nil
			}
			return "", errors.New("not found")
		},
		Getenv: func(key string) string {
			if key == "SSH_CONNECTION" {
				return "198.51.100.4 50000 203.0.113.7 22"
			}
			return ""
		},
		RegistryHandler: func(string) (string, bool) { return "", false },
		FileExists:      func(string) bool { return false },
		ReadFile:        func(string) ([]byte, error) { return nil, errors.New("not found") },
		ListDir:         func(string) []string { return nil },
	}
}

// The page starts with the environment detected without the login
// question, so it answers without waiting on it, and the refresh asks the
// question straight away rather than one whole interval later.
func TestTheFirstEnvironmentLeavesTheLoginToTheRefresh(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "")
	release := make(chan struct{})
	runner := claude.NewFakeRunner(func(args []string) (string, error) {
		if len(args) == 2 && args[0] == "auth" && args[1] == "status" {
			<-release
			return `{"loggedIn": true}`, nil
		}
		return "2.1.275 (Claude Code)\n", nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()

	cache, target := &envCache{}, &targetCache{}
	cache.set(hosts.DetectWithoutLogin(ctx, linuxServerProbes(), runner, false))
	if got := cache.get().CLILoggedIn; got != hosts.LoginUnknown {
		t.Fatalf("the first environment does not know the login yet: %q", got)
	}

	startEnvRefresh(ctx, &wg, time.Hour, envRefresher(cache, target, t.TempDir(), func() hosts.Env {
		return hosts.Detect(ctx, linuxServerProbes(), runner, false)
	}))
	if got := cache.get().CLILoggedIn; got != hosts.LoginUnknown {
		t.Fatalf("still unknown while the CLI has not answered: %q", got)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for cache.get().CLILoggedIn != hosts.LoginYes {
		if time.Now().After(deadline) {
			t.Fatalf("the refresh never filled the login in: %+v", cache.get())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The running service reads the saved ssh address again on each refresh,
// so a later run from another ssh connection moves the commands for
// reaching this machine from elsewhere along with it.
func TestTheRefreshReadsTheSavedAddressAgain(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "")
	dir := t.TempDir()
	if err := state.SaveServerAddress(dir, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	cache, target := &envCache{}, &targetCache{}
	refresh := envRefresher(cache, target, dir, func() hosts.Env { return hosts.Env{Headless: true} })

	refresh()
	if got, want := target.get(), currentUser()+"@203.0.113.7"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if err := state.SaveServerAddress(dir, "203.0.113.9"); err != nil {
		t.Fatal(err)
	}
	refresh()
	if got, want := target.get(), currentUser()+"@203.0.113.9"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// A machine with a display has nobody connecting from elsewhere.
	withDisplay := envRefresher(cache, target, dir, func() hosts.Env { return hosts.Env{} })
	withDisplay()
	if got := target.get(); got != "" {
		t.Fatalf("got %q", got)
	}
}

// The refresh stops when its context ends, and a context already ended
// never runs it.
func TestStartEnvRefreshStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var wg sync.WaitGroup
	ran := false
	startEnvRefresh(ctx, &wg, time.Millisecond, func() { ran = true })
	wg.Wait()
	if ran {
		t.Fatal("an ended context runs nothing")
	}
}

// A second copy that finds the first one running says where its page is,
// with the key, when both files can be read and a live copy holds the
// lock, and only that it is running otherwise.
func TestAlreadyRunningLine(t *testing.T) {
	state.SetPIDChecker(procs.NewReal().Exists)
	dir := t.TempDir()
	if got := alreadyRunningLine(dir, true); got != "CC Babysitter is already running." {
		t.Fatalf("no files: %q", got)
	}
	if err := state.SavePageURL(dir, "http://127.0.0.1:47391"); err != nil {
		t.Fatal(err)
	}
	if got := alreadyRunningLine(dir, true); got != "CC Babysitter is already running." {
		t.Fatalf("no key: %q", got)
	}
	key, err := state.PageKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	// With no live copy holding the lock, the saved address may be an old
	// one that names another account's server by now: neither it nor the
	// key is printed.
	for _, withKey := range []bool{true, false} {
		if got := alreadyRunningLine(dir, withKey); got != "CC Babysitter is already running." {
			t.Fatalf("no lock, withKey %v: %q", withKey, got)
		}
	}
	holdLock(t, dir)
	if got, want := alreadyRunningLine(dir, true), "CC Babysitter is already running: http://127.0.0.1:47391/?token="+key; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// The service's output goes to the journal, which is no place for the
	// key: it gets the plain address.
	if got, want := alreadyRunningLine(dir, false), "CC Babysitter is already running: http://127.0.0.1:47391"; got != want {
		t.Fatalf("without the key: got %q, want %q", got, want)
	}
}

// useStateDir points state.DefaultDir at a fresh temporary folder for this
// test and returns it.
func useStateDir(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", base)
	t.Setenv("LOCALAPPDATA", base)
	return state.DefaultDir()
}

// The service's output goes to the systemd journal, which other accounts
// may be able to read, and is repeated every time it restarts. It never
// carries the key: not in the banner, and not when it finds another copy
// already running. A run in a terminal does give the address with it.
func TestServiceOutputNeverCarriesTheKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncWriter{}
	done := make(chan int, 1)
	go func() {
		done <- serve(ctx, serveOptions{Demo: true, NoOpen: true, Service: true, Port: 0}, out)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "Ctrl+C") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if !strings.Contains(out.String(), "Page: http://127.0.0.1:") || strings.Contains(out.String(), "token=") {
		t.Fatalf("the service's banner:\n%s", out.String())
	}

	// A second copy, started while this test holds the lock of a state
	// folder of its own, stops at once without touching anything else.
	dir := useStateDir(t)
	if err := state.SavePageURL(dir, "http://127.0.0.1:47391"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.PageKey(dir); err != nil {
		t.Fatal(err)
	}
	holdLock(t, dir)
	// Never go on to a real start: the lock must be seen as held by a live
	// copy with the same check serve itself uses.
	state.SetPIDChecker(procs.NewReal().Exists)
	if !state.IsHeld(dir) {
		t.Fatal("the lock this test holds is not seen as held")
	}
	for _, service := range []bool{true, false} {
		var b strings.Builder
		if rc := serve(context.Background(), serveOptions{NoOpen: true, Service: service}, &b); rc != 1 {
			t.Fatalf("service %v: rc %d, out %q", service, rc, b.String())
		}
		if strings.Contains(b.String(), "token=") == service || !strings.HasPrefix(b.String(), "CC Babysitter is already running: http://127.0.0.1:47391") {
			t.Fatalf("service %v: %q", service, b.String())
		}
	}
}

// The browser is opened with a one-time launch token, never the key: the
// opener's command line can be read by every account on the machine. The
// launch address works once, and the cookie it sets is not the key.
func TestBrowserIsOpenedWithALaunchToken(t *testing.T) {
	key := strings.Repeat("ab", 32)
	srv := web.NewServer(&ctlEngine{view: ctlView()}, nil, "0.4.0", key)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	var opened []string
	if err := openInBrowser(srv, ts.URL, func(u string) error { opened = append(opened, u); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || !strings.HasPrefix(opened[0], ts.URL+"/?token=") || strings.Contains(opened[0], key) {
		t.Fatalf("opened %q", opened)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for i, want := range []int{http.StatusSeeOther, http.StatusUnauthorized} {
		resp, err := c.Get(opened[0])
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("use %d: %d, want %d", i+1, resp.StatusCode, want)
		}
		if i == 0 && (resp.Header.Get("Set-Cookie") == "" || strings.Contains(resp.Header.Get("Set-Cookie"), key)) {
			t.Fatalf("the launch address set %q, want a cookie that is not the key", resp.Header.Get("Set-Cookie"))
		}
	}

	boom := errors.New("no opener")
	if err := openInBrowser(srv, ts.URL, func(string) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

// The moment a copy takes the state folder's lock, the page address left
// by one before it is removed, before anything slower happens: otherwise
// the lock would be held, by a live copy, while the old address, which
// may name another account's server by now, is still on disk for the
// command line to send the key to.
func TestClaimingTheFolderClearsAnOldPageAddress(t *testing.T) {
	dir := t.TempDir()
	if err := state.SavePageURL(dir, "http://127.0.0.1:47391"); err != nil {
		t.Fatal(err)
	}
	createMs := thisProcessStart(t)
	lock, err := claimStateDir(dir, createMs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lock.Release)
	if !state.IsHeld(dir) || state.PageURL(dir) != "" {
		t.Fatalf("held %v, page address %q", state.IsHeld(dir), state.PageURL(dir))
	}

	// A copy that finds the folder taken leaves the running copy's address
	// alone.
	if err := state.SavePageURL(dir, "http://127.0.0.1:47392"); err != nil {
		t.Fatal(err)
	}
	if _, err := claimStateDir(dir, createMs); err == nil {
		t.Fatal("the folder was claimed twice")
	}
	if state.PageURL(dir) != "http://127.0.0.1:47392" {
		t.Fatal("a copy that did not get the lock removed the running copy's address")
	}
}

// thisProcessStart is this test process's start time, with the real
// process check wired, for a lock this process takes.
func thisProcessStart(t *testing.T) int64 {
	t.Helper()
	createMs, ok := procs.NewReal().CreateTime(os.Getpid())
	if !ok {
		t.Skip("cannot read this process's start time here")
	}
	state.SetPIDChecker(procs.NewReal().Exists)
	return createMs
}

// When the old page address cannot be removed once the lock is taken, the
// lock is given back and the start fails with a plain sentence: not "already
// running", and never an address or the key.
func TestClaimFailsPlainlyWhenTheOldAddressStays(t *testing.T) {
	dir := t.TempDir()
	key, err := state.PageKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SavePageURL(dir, "http://127.0.0.1:47391"); err != nil {
		t.Fatal(err)
	}
	createMs := thisProcessStart(t)
	boom := errors.New("permission denied")
	clearPageURL = func(string) error { return boom }
	t.Cleanup(func() { clearPageURL = state.ClearPageURL })

	_, err = claimStateDir(dir, createMs)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if state.IsHeld(dir) {
		t.Fatal("the lock was kept after the claim failed")
	}
	for _, service := range []bool{true, false} {
		line := claimFailureLine(dir, err, !service)
		if want := "Could not clear the old page address in " + dir + ": permission denied"; line != want {
			t.Fatalf("got %q, want %q", line, want)
		}
		if strings.Contains(line, "already running") || strings.Contains(line, key) || strings.Contains(line, "127.0.0.1") {
			t.Fatalf("%q", line)
		}
	}
	// A folder some other copy holds is still said to be running.
	if got := claimFailureLine(dir, errors.New("CC Babysitter is already running"), true); !strings.HasPrefix(got, "CC Babysitter is already running") {
		t.Fatalf("held: %q", got)
	}
}

// A quit through the API shuts serve down as Ctrl+C does, even with an
// event stream open: serve returns 0, and well within the shutdown grace
// period, so the open stream did not hold the shutdown up. (The demo
// deletes its own state folder as serve returns, so the saved address is
// not checked here; removing it is the same code as after Ctrl+C.)
func TestServeQuitsThroughTheAPI(t *testing.T) {
	out := &syncWriter{}
	done := make(chan int, 1)
	dirCh := make(chan string, 1)
	go func() {
		done <- serve(context.Background(), serveOptions{Demo: true, NoOpen: true, Port: 0, onStateDir: func(d string) { dirCh <- d }}, out)
	}()
	stateDir := <-dirCh
	keyed := waitForPageURL(t, out)
	url, _, _ := strings.Cut(keyed, "/?")
	key := state.ReadPageKey(stateDir)

	stream, err := http.NewRequest(http.MethodGet, url+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	stream.Header.Set("Authorization", "token "+key)
	streamResp, err := http.DefaultClient.Do(stream)
	if err != nil {
		t.Fatal(err)
	}
	defer streamResp.Body.Close()

	req, err := http.NewRequest(http.MethodPost, url+"/api/quit", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "token "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("quit = %d", resp.StatusCode)
	}
	answered := time.Now()

	select {
	case rc := <-done:
		if rc != 0 {
			t.Fatalf("serve returned %d after a quit", rc)
		}
		if took := time.Since(answered); took >= shutdownGrace/2 {
			t.Fatalf("serve took %v to return after a quit; an open stream held the shutdown up", took)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after a quit")
	}
}
