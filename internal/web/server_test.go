package web

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/state"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
)

type fakeEngine struct {
	calls []string
	view  supervise.View
	// refuse names the one action this engine answers no to, so a test can
	// drive the refused path.
	refuse string
	// via is where the engine's latest action, settings change included,
	// was said to come from.
	via supervise.Via
}

func (f *fakeEngine) answer(action, id string, via supervise.Via) supervise.Result {
	f.via = via
	f.calls = append(f.calls, action+" "+id)
	if f.refuse == action {
		return supervise.Result{Message: "That session is not being babysat."}
	}
	return supervise.Result{OK: true, Message: "ok"}
}

func (f *fakeEngine) View() supervise.View { return f.view }
func (f *fakeEngine) Babysit(id string, startAtLogin bool, via supervise.Via) supervise.Result {
	if startAtLogin {
		return f.answer("babysit-and-start-at-login", id, via)
	}
	return f.answer("babysit", id, via)
}

func (f *fakeEngine) Stop(id string, via supervise.Via) supervise.Result {
	return f.answer("stop", id, via)
}
func (f *fakeEngine) Unbabysit(id string, via supervise.Via) supervise.Result {
	return f.answer("unbabysit", id, via)
}
func (f *fakeEngine) ResumeWatch(id string, via supervise.Via) supervise.Result {
	return f.answer("resume-watch", id, via)
}
func (f *fakeEngine) OpenTerminal(id string) supervise.Result {
	return f.answer("open-terminal", id, supervise.ViaPage)
}
func (f *fakeEngine) SetSettings(s state.Settings, via supervise.Via) supervise.Result {
	f.via = via
	f.calls = append(f.calls, "settings "+s.KeepAwakeMode)
	f.view.Settings = s
	return supervise.Result{OK: true}
}

const testID1 = "11111111-2222-4333-8444-555555555501"

// testKey is the access key every test server here is built with.
var testKey = strings.Repeat("ab", 32)

// keyTransport sends testKey on every request that does not already carry
// an Authorization header or a cookie of its own, the way the command line
// sends the key, so tests about something else get past the key check.
type keyTransport struct{}

func (keyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("Authorization") == "" && r.Header.Get("Cookie") == "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "token "+testKey)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// keyed is an HTTP client that carries testKey.
var keyed = &http.Client{Transport: keyTransport{}}

func newTS(t *testing.T) (*httptest.Server, *fakeEngine, *Server) {
	t.Helper()
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &fakeEngine{view: supervise.View{Version: "0.1.0"}}
	s := NewServer(e, log, "0.1.0", testKey)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, e, s
}

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	r, err := keyed.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestStateReturnsTheView(t *testing.T) {
	ts, _, _ := newTS(t)
	r, err := keyed.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if string(v["version"]) != `"0.1.0"` {
		t.Fatalf("%s", v["version"])
	}
	if _, found := v["costs"]; found {
		t.Fatal("the view carries no cost estimates")
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(v["settings"], &settings); err != nil {
		t.Fatal(err)
	}
	if _, found := settings["prices"]; found {
		t.Fatal("the settings carry no price table")
	}
}

func TestActionsCallTheEngine(t *testing.T) {
	ts, e, _ := newTS(t)

	r := postJSON(t, ts.URL+"/api/sessions/"+testID1+"/babysit", "")
	if r.StatusCode != 200 || e.calls[len(e.calls)-1] != "babysit "+testID1 {
		t.Fatal(r.StatusCode, e.calls)
	}

	// The babysit body is optional; when it is there it may ask for start at
	// login and for nothing else.
	r = postJSON(t, ts.URL+"/api/sessions/"+testID1+"/babysit", `{"startAtLogin":true}`)
	if r.StatusCode != 200 || e.calls[len(e.calls)-1] != "babysit-and-start-at-login "+testID1 {
		t.Fatal(r.StatusCode, e.calls)
	}
	r = postJSON(t, ts.URL+"/api/sessions/"+testID1+"/babysit", `{"startAtLogin":true,"moveBack":true}`)
	if r.StatusCode != 400 {
		t.Fatal("an unknown field is refused:", r.StatusCode)
	}

	e.refuse = "resume-watch"
	r = postJSON(t, ts.URL+"/api/sessions/"+testID1+"/resume-watch", "")
	if r.StatusCode != 409 {
		t.Fatal("non-OK result is 409:", r.StatusCode)
	}
	var res supervise.Result
	json.NewDecoder(r.Body).Decode(&res)
	if res.OK || res.Message == "" {
		t.Fatalf("%+v", res)
	}

	r = postJSON(t, ts.URL+"/api/sessions/"+testID1+"/unbabysit", "")
	if r.StatusCode != 200 || e.calls[len(e.calls)-1] != "unbabysit "+testID1 {
		t.Fatal(r.StatusCode, e.calls)
	}

	r = postJSON(t, ts.URL+"/api/sessions/"+testID1+"/stop", "")
	if r.StatusCode != 200 || e.calls[len(e.calls)-1] != "stop "+testID1 {
		t.Fatal(r.StatusCode, e.calls)
	}

	r = postJSON(t, ts.URL+"/api/sessions/"+testID1+"/open-terminal", "")
	if r.StatusCode != 200 || e.calls[len(e.calls)-1] != "open-terminal "+testID1 {
		t.Fatal(r.StatusCode, e.calls)
	}

	// A stop and opening a terminal are actions like any other: the guard
	// stands in front of them and an id of the wrong shape never reaches
	// the engine.
	for _, action := range []string{"stop", "open-terminal"} {
		r = postJSON(t, ts.URL+"/api/sessions/not-a-uuid/"+action, "")
		if r.StatusCode != 404 || e.calls[len(e.calls)-1] != "open-terminal "+testID1 {
			t.Fatal(action, r.StatusCode, e.calls)
		}
	}
}

// Stopping a background copy ends a running session and opening a terminal
// starts a program, so the guard in front of both is pinned on its own: a
// request from another site, or one that is not JSON, never reaches the
// engine.
func TestTheStopAndOpenTerminalRoutesAreGuarded(t *testing.T) {
	for _, action := range []string{"stop", "open-terminal"} {
		guardedRoute(t, "/api/sessions/"+testID1+"/"+action)
	}
}

func guardedRoute(t *testing.T, url string) {
	t.Helper()
	cases := map[string]map[string]string{
		"another origin":  {"Content-Type": "application/json", "Origin": "http://evil.example"},
		"another site":    {"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site"},
		"plain text":      {"Content-Type": "text/plain"},
		"no content type": {},
		"a form post":     {"Content-Type": "application/x-www-form-urlencoded"},
		"a foreign host":  {"Content-Type": "application/json", "Host": "evil.example"},
	}
	for name, headers := range cases {
		t.Run(url+" "+name, func(t *testing.T) {
			ts, e, _ := newTS(t)
			resp := doRequest(t, http.MethodPost, ts.URL+url, headers, "{}")
			assertForbiddenNoCall(t, e, resp)
		})
	}
}

// Moving a session between apps is not something this program does, so
// the routes that used to do it answer as if they had never existed.
func TestTheMoveRoutesAreGone(t *testing.T) {
	ts, e, _ := newTS(t)
	for _, path := range []string{"/api/sessions/" + testID1 + "/move", "/api/explainer-seen"} {
		r := postJSON(t, ts.URL+path, `{"target":"terminal"}`)
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			t.Fatalf("POST %s: status %d, want 404", path, r.StatusCode)
		}
	}
	if len(e.calls) != 0 {
		t.Fatalf("the engine must not be reached: %v", e.calls)
	}
}

// Starting a session is not something this program does either: the page
// no longer offers New session, and the route it used to call answers as
// if it had never existed.
func TestTheNewSessionRouteIsGone(t *testing.T) {
	ts, e, _ := newTS(t)
	r := postJSON(t, ts.URL+"/api/sessions", `{"cwd":"/w","name":"n"}`)
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("POST /api/sessions: status %d, want 404", r.StatusCode)
	}
	if len(e.calls) != 0 {
		t.Fatalf("the engine must not be reached: %v", e.calls)
	}
}

func TestUnknownIDIs404AndEngineNotCalled(t *testing.T) {
	ts, e, _ := newTS(t)
	r := postJSON(t, ts.URL+"/api/sessions/not-a-uuid/babysit", "")
	if r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
	var eb errorBody
	json.NewDecoder(r.Body).Decode(&eb)
	if eb.OK || eb.Message == "" {
		t.Fatalf("%+v", eb)
	}
	if len(e.calls) != 0 {
		t.Fatal("engine must not be called for an invalid id:", e.calls)
	}
}

func TestSettingsGetAndPut(t *testing.T) {
	ts, e, _ := newTS(t)
	r, _ := keyed.Get(ts.URL + "/api/settings")
	var got state.Settings
	json.NewDecoder(r.Body).Decode(&got)

	r = postJSONMethod(t, http.MethodPut, ts.URL+"/api/settings", `{"keepAwakeMode":"always","theme":"dark"}`)
	if r.StatusCode != 200 || e.calls[len(e.calls)-1] != "settings always" {
		t.Fatal(r.StatusCode, e.calls)
	}

	r = postJSONMethod(t, http.MethodPut, ts.URL+"/api/settings", "")
	if r.StatusCode != 400 {
		t.Fatal("empty body is required for PUT settings, so it is malformed:", r.StatusCode)
	}
}

// The page saves one setting at a time, so a body that names one setting
// changes that one and leaves every other as it was.
func TestSettingsPutChangesOnlyWhatItNames(t *testing.T) {
	ts, e, _ := newTS(t)
	e.view.Settings = state.Settings{Autostart: true, AutoBabysit: true, AutoOpenBrowser: true, Theme: "dark"}

	r := postJSONMethod(t, http.MethodPut, ts.URL+"/api/settings", `{"theme":"light"}`)
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	want := state.Settings{Autostart: true, AutoBabysit: true, AutoOpenBrowser: true, Theme: "light"}
	if e.view.Settings != want {
		t.Fatalf("got %+v, want %+v", e.view.Settings, want)
	}

	r = postJSONMethod(t, http.MethodPut, ts.URL+"/api/settings", `{"autoBabysit":false}`)
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	want.AutoBabysit = false
	if e.view.Settings != want {
		t.Fatalf("got %+v, want %+v", e.view.Settings, want)
	}

	r = postJSONMethod(t, http.MethodPut, ts.URL+"/api/settings", `{"serverAutoBabysit":true}`)
	if r.StatusCode != 400 {
		t.Fatal("a setting that no longer exists is refused:", r.StatusCode)
	}
}

func postJSONMethod(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := keyed.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestActivityEndpointFiltersBySessionAndOrdersNewestFirst(t *testing.T) {
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &fakeEngine{}
	ts := httptest.NewServer(NewServer(e, log, "0.1.0", testKey).Handler())
	defer ts.Close()

	log.Info("a", "one")
	log.Auto("b", "why", "two")
	log.Info("b", "three")

	r, err := keyed.Get(ts.URL + "/api/activity?session=b")
	if err != nil {
		t.Fatal(err)
	}
	var entries []state.Entry
	if err := json.NewDecoder(r.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("%+v", entries)
	}
	if entries[0].Message != "three" || entries[1].Message != "two" {
		t.Fatalf("expected newest first: %+v", entries)
	}
	if !entries[1].Automatic {
		t.Fatalf("%+v", entries)
	}
}

func TestRootServesThePage(t *testing.T) {
	ts, _, _ := newTS(t)
	r, err := keyed.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Fatal(ct)
	}
}

func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	ts, _, _ := newTS(t)
	r, err := keyed.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	if r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal(r.Header)
	}
	if r.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(r.Header)
	}
	if r.Header.Get("Content-Security-Policy") == "" {
		t.Fatal(r.Header)
	}
	for k := range r.Header {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Fatal("no Access-Control-* header must ever be sent:", k)
		}
	}
}

// A view that cannot be encoded must not answer 200 with half a document.
func TestStateAnswersAnErrorWhenTheViewCannotBeEncoded(t *testing.T) {
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &fakeEngine{view: unencodableView()}
	ts := httptest.NewServer(NewServer(e, log, "0.1.0", testKey).Handler())
	defer ts.Close()

	r, err := keyed.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", r.StatusCode)
	}
	var eb errorBody
	if err := json.NewDecoder(r.Body).Decode(&eb); err != nil {
		t.Fatal(err)
	}
	if eb.OK || !strings.HasPrefix(eb.Message, "the page view could not be encoded: ") {
		t.Fatalf("the 500 body must be the error shape with a sentence: %+v", eb)
	}
	said := 0
	for _, entry := range log.Recent(20, "") {
		if strings.Contains(entry.Message, "could not be encoded") {
			said++
			if strings.Contains(strings.ToLower(entry.Message), "cockpit") {
				t.Fatalf("the program calls it the page: %q", entry.Message)
			}
		}
	}
	if said != 1 {
		t.Fatalf("the reason must be written down once, got %d lines", said)
	}
}

// unencodableView builds a view carrying a number that has no JSON form.
func unencodableView() supervise.View {
	return supervise.View{
		Version:  "0.1.0",
		Sessions: []supervise.SessionView{},
		Watches:  []supervise.WatchView{},
		Totals:   supervise.Totals{CPUPercent: math.Inf(1)},
	}
}

// Live updates stopping for good, silently, is the worst way this can go
// wrong: the page keeps showing numbers that stopped being true and
// nothing says so.
func TestBroadcastKeepsGoingAndExplainsItselfOnceWhenAViewCannotBeEncoded(t *testing.T) {
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &fakeEngine{view: unencodableView()}
	s := NewServer(e, log, "0.1.0", testKey)

	for i := 0; i < 5; i++ {
		if _, ok := s.hub.viewMessage(); ok {
			t.Fatal("a view that cannot be encoded is not a message")
		}
	}
	said := 0
	for _, entry := range log.Recent(20, "") {
		if strings.Contains(entry.Message, "could not be encoded") {
			said++
		}
	}
	if said != 1 {
		t.Fatalf("the same failure is explained once, got %d lines", said)
	}

	// Once the numbers are usable again, the stream picks straight back up.
	e.view = supervise.View{Version: "0.1.0", Sessions: []supervise.SessionView{}, Watches: []supervise.WatchView{}}
	if _, ok := s.hub.viewMessage(); !ok {
		t.Fatal("a usable view must be sent again")
	}
}

// A page opened while the current view cannot be encoded is better off a
// little behind than blank with no explanation.
func TestStateServesTheLastGoodViewWhileTheCurrentOneCannotBeEncoded(t *testing.T) {
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &fakeEngine{view: supervise.View{
		Version: "0.1.0", Sessions: []supervise.SessionView{}, Watches: []supervise.WatchView{},
	}}
	s := NewServer(e, log, "0.1.0", testKey)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// One good read, so there is something to fall back to.
	r, err := keyed.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}

	e.view = unencodableView()
	r, err = keyed.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("status %d, want the last good view with 200", r.StatusCode)
	}
	var body struct {
		Version  string            `json:"version"`
		Sessions []json.RawMessage `json:"sessions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Version != "0.1.0" || body.Sessions == nil {
		t.Fatalf("the cached view must be a whole one: %+v", body)
	}

	// A new stream client is given the same thing rather than nothing.
	msg, ok := s.hub.openingMessage()
	if !ok || !strings.Contains(string(msg), `"version":"0.1.0"`) {
		t.Fatalf("a new client must be given the last good view: %q %v", msg, ok)
	}
}

// A failure that comes back after things had started working again is
// news, not a repeat of what was already said.
func TestAMarshalFailureIsReportedAgainAfterARecovery(t *testing.T) {
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	good := supervise.View{
		Version: "0.1.0", Sessions: []supervise.SessionView{}, Watches: []supervise.WatchView{},
	}
	e := &fakeEngine{view: unencodableView()}
	s := NewServer(e, log, "0.1.0", testKey)

	count := func() int {
		n := 0
		for _, entry := range log.Recent(50, "") {
			if strings.Contains(entry.Message, "could not be encoded") {
				n++
			}
		}
		return n
	}

	s.hub.viewMessage()
	s.hub.viewMessage()
	if n := count(); n != 1 {
		t.Fatalf("the same failure is said once, got %d", n)
	}

	e.view = good
	if _, ok := s.hub.viewMessage(); !ok {
		t.Fatal("a usable view must encode")
	}

	e.view = unencodableView()
	s.hub.viewMessage()
	if n := count(); n != 2 {
		t.Fatalf("a failure after a recovery must be said again, got %d", n)
	}
}

func postVia(t *testing.T, url string) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := keyed.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestViaCLIReachesTheEngine(t *testing.T) {
	ts, e, _ := newTS(t)
	for _, route := range []string{"babysit", "unbabysit", "stop", "resume-watch"} {
		e.via = ""
		postVia(t, ts.URL+"/api/sessions/"+testID1+"/"+route+"?via=cli")
		if e.via != supervise.ViaCLI {
			t.Errorf("%s: via = %q, want cli", route, e.via)
		}
	}
}

func TestUnknownViaIsThePage(t *testing.T) {
	ts, e, _ := newTS(t)
	e.via = supervise.ViaCLI
	postVia(t, ts.URL+"/api/sessions/"+testID1+"/babysit?via=evil")
	if e.via != supervise.ViaPage {
		t.Errorf("via = %q, want the page", e.via)
	}
}

func TestSettingsFromTheCommandLineAreLoggedOnce(t *testing.T) {
	ts, e, s := newTS(t)
	e.view.Settings = state.Settings{AutoBabysit: true, Theme: "auto"}
	req, _ := http.NewRequest("PUT", ts.URL+"/api/settings?via=cli", strings.NewReader(`{"autoBabysit":false,"autostart":true}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := keyed.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var msgs []string
	for _, en := range s.log.Recent(10, "") {
		msgs = append(msgs, en.Message)
	}
	if !slices.Contains(msgs, "auto-babysit off, from the command line") {
		t.Errorf("no auto-babysit entry: %q", msgs)
	}
	for _, m := range msgs {
		if strings.HasPrefix(m, "autostart") || strings.HasPrefix(m, "start at login") {
			t.Errorf("the server logged start at login, which the engine logs: %q", m)
		}
	}
	if e.via != supervise.ViaCLI {
		t.Errorf("SetSettings via = %q", e.via)
	}
}

func TestSettingsFromThePageAreNotLoggedByTheServer(t *testing.T) {
	ts, e, s := newTS(t)
	e.view.Settings = state.Settings{AutoBabysit: true, Theme: "auto"}
	req, _ := http.NewRequest("PUT", ts.URL+"/api/settings", strings.NewReader(`{"autoBabysit":false}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := keyed.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if n := len(s.log.Recent(10, "")); n != 0 {
		t.Errorf("the page's settings change wrote %d server entries", n)
	}
}

func TestQuitAnswersThenCallsTheHook(t *testing.T) {
	ts, _, s := newTS(t)
	called := make(chan struct{}, 2)
	s.OnQuit(func() { called <- struct{}{} })

	r := postJSON(t, ts.URL+"/api/quit?via=cli", "{}")
	var res supervise.Result
	decodeBody(t, r, &res)
	if r.StatusCode != http.StatusOK || !res.OK || !strings.Contains(res.Message, "nothing brings them back") {
		t.Fatalf("quit = %d %+v", r.StatusCode, res)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("the quit hook was not called")
	}
	entries := s.log.Recent(1, "")
	if len(entries) != 1 || entries[0].Message != "asked to quit, from the command line" {
		t.Fatalf("activity = %+v", entries)
	}

	// A second quit, as a double click would send, is answered too and
	// calls the hook again, which serve's cancel function allows.
	r = postJSON(t, ts.URL+"/api/quit", "{}")
	r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("second quit = %d", r.StatusCode)
	}
}

func TestQuitWithoutAHookIsRefused(t *testing.T) {
	ts, _, _ := newTS(t)
	r := postJSON(t, ts.URL+"/api/quit", "{}")
	var res supervise.Result
	decodeBody(t, r, &res)
	if r.StatusCode != http.StatusConflict || res.OK || res.Message != "Quitting is not available here." {
		t.Fatalf("quit without a hook = %d %+v", r.StatusCode, res)
	}
}

// The launcher asks the running copy for a one-time address to open the
// page with, so the key never reaches the program that opens a browser.
func TestLaunchHandsOutAOneTimeAddress(t *testing.T) {
	ts, e, s := newTS(t)
	e.view.URL = ts.URL
	r := postJSON(t, ts.URL+"/api/launch", "{}")
	var got struct {
		OK  bool   `json:"ok"`
		URL string `json:"url"`
	}
	decodeBody(t, r, &got)
	if r.StatusCode != http.StatusOK || !got.OK {
		t.Fatalf("launch = %d %+v", r.StatusCode, got)
	}
	launchToken(t, ts, got.URL)

	resp := doPlainRequest(t, http.MethodGet, got.URL, nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("first use: %d", resp.StatusCode)
	}
	if c := keyCookie(t, ts, resp); c == nil || c.Value != s.session {
		t.Fatalf("first use set %v, want this run's session", resp.Cookies())
	}
}
