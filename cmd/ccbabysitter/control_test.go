package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/client"
	"ccbabysitter.dev/ccbabysitter/internal/state"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
	"ccbabysitter.dev/ccbabysitter/internal/web"
)

const (
	cID1 = "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa" // live, api
	cID2 = "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb" // live, web
	cID3 = "cccccccc-3333-4333-8333-cccccccccccc" // babysat, stuck, nightly
	cID4 = "dddddddd-4444-4444-8444-dddddddddddd" // babysat, in background, worker
)

type ctlEngine struct {
	view   supervise.View
	calls  []string
	refuse string
	log    *state.Log
}

func (e *ctlEngine) View() supervise.View { return e.view }
func (e *ctlEngine) act(name, id string, via supervise.Via) supervise.Result {
	e.calls = append(e.calls, name+" "+id+" "+string(via))
	if e.refuse == name {
		return supervise.Result{Message: "That session is not being babysat."}
	}
	return supervise.Result{OK: true, Message: name + " done.", ShortID: id[:8]}
}
func (e *ctlEngine) Babysit(id string, s bool, v supervise.Via) supervise.Result {
	return e.act("babysit", id, v)
}
func (e *ctlEngine) Unbabysit(id string, v supervise.Via) supervise.Result {
	return e.act("unbabysit", id, v)
}
func (e *ctlEngine) Stop(id string, v supervise.Via) supervise.Result { return e.act("stop", id, v) }
func (e *ctlEngine) ResumeWatch(id string, v supervise.Via) supervise.Result {
	return e.act("retry", id, v)
}
func (e *ctlEngine) OpenTerminal(string) supervise.Result { return supervise.Result{} }
func (e *ctlEngine) SetSettings(s state.Settings, v supervise.Via) supervise.Result {
	e.view.Settings = s
	e.calls = append(e.calls, "settings "+string(v))
	return supervise.Result{OK: true}
}

func ctlView() supervise.View {
	return supervise.View{
		Version: "0.4.0", URL: "http://127.0.0.1:47391",
		Settings:  state.Settings{AutoBabysit: true, Theme: "auto"},
		KeepAwake: supervise.KeepAwakeView{Held: true, Supported: true},
		Sessions: []supervise.SessionView{
			{ID: cID1, ShortID: "aaaaaaaa", Name: "api", Cwd: "/w/api", PID: 101, Host: claude.HostTerminal, Live: []claude.Host{claude.HostTerminal}, Tree: supervise.TreeView{UptimeSeconds: 11100}},
			{ID: cID2, ShortID: "bbbbbbbb", Name: "web", Cwd: "/w/web", PID: 102, Host: claude.HostVSCode, Live: []claude.Host{claude.HostVSCode}},
		},
		Watches: []supervise.WatchView{
			{Watch: state.Watch{SessionID: cID3, ShortID: "cccccccc", Name: "nightly"}, State: supervise.StateStuck, Live: []claude.Host{}},
			{Watch: state.Watch{SessionID: cID4, ShortID: "dddddddd", Name: "worker", Cwd: "/w/worker"}, State: supervise.StateInBackground,
				Host: claude.HostBackground, Live: []claude.Host{claude.HostBackground}, RCOn: true, PID: 104, CanStop: true,
				Stats: claude.Stats{InputTokens: 12300}},
		},
	}
}

// testEnv serves ctlView through the real web.Server and guard, saves its
// address and key in a temp state folder, and returns an env aimed at it.
func testEnv(t *testing.T) (controlEnv, *ctlEngine, *httptest.Server) {
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
	e := &ctlEngine{view: ctlView(), log: log}
	ts := httptest.NewServer(web.NewServer(e, log, "0.4.0", key).Handler())
	t.Cleanup(ts.Close)
	if err := state.SavePageURL(dir, ts.URL); err != nil {
		t.Fatal(err)
	}
	holdLock(t, dir)
	return controlEnv{stateDir: dir, self: func(supervise.View) (int, bool) { return 0, false }}, e, ts
}

func runCmd(t *testing.T, env controlEnv, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	env.stdout, env.stderr = &out, &errb
	code := runControl(args[0], args[1:], env)
	return code, out.String(), errb.String()
}

func oneJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(out))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("not JSON: %q", out)
	}
	if dec.More() {
		t.Fatalf("more than one document: %q", out)
	}
	if doc["schema"] != float64(1) {
		t.Fatalf("schema = %v", doc["schema"])
	}
	return doc
}

func TestListShowsBabysatSessions(t *testing.T) {
	env, _, _ := testEnv(t)
	code, out, _ := runCmd(t, env, "list")
	if code != 0 {
		t.Fatalf("list = %d", code)
	}
	for _, want := range []string{"api", "web", "nightly", "stuck", "worker", "in background", "12k", "3h 5m"} {
		if !strings.Contains(out, want) {
			t.Errorf("list has no %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "worker") && !strings.Contains(line, " on ") {
			t.Errorf("worker has Remote Control on: %q", line)
		}
	}
}

func TestListJSON(t *testing.T) {
	env, _, _ := testEnv(t)
	code, out, _ := runCmd(t, env, "list", "--json")
	var doc struct {
		Schema   int              `json:"schema"`
		Sessions []client.Session `json:"sessions"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil || doc.Schema != 1 || len(doc.Sessions) != 4 {
		t.Fatalf("list --json = %d\n%s", code, out)
	}
}

func TestShowPrintsZerosAndState(t *testing.T) {
	env, _, _ := testEnv(t)
	code, out, _ := runCmd(t, env, "show", "web")
	if code != 0 || !strings.Contains(out, "Babysat: no") || !strings.Contains(out, "Remote Control: off") {
		t.Fatalf("show web = %d\n%s", code, out)
	}
	_, out, _ = runCmd(t, env, "show", "nightly")
	if !strings.Contains(out, "State: stuck") || !strings.Contains(out, "Running: no") {
		t.Fatalf("show nightly:\n%s", out)
	}
}

func TestBabysitByNameReachesEngineWithVia(t *testing.T) {
	env, e, _ := testEnv(t)
	code, out, _ := runCmd(t, env, "babysit", "api")
	if code != 0 || !strings.Contains(out, "babysit done.") {
		t.Fatalf("babysit api = %d %q", code, out)
	}
	if len(e.calls) != 1 || e.calls[0] != "babysit "+cID1+" cli" {
		t.Fatalf("calls = %q", e.calls)
	}
}

func TestFlagsAfterTheSession(t *testing.T) {
	env, _, _ := testEnv(t)
	for _, args := range [][]string{{"babysit", "api", "--json"}, {"babysit", "--json", "api"}} {
		code, out, _ := runCmd(t, env, args...)
		doc := oneJSON(t, out)
		if code != 0 || doc["ok"] != true {
			t.Errorf("%v = %d %v", args, code, doc)
		}
	}
	if code, _, _ := runCmd(t, env, "activity", "worker", "-n", "5"); code != 0 {
		t.Errorf("activity worker -n 5 = %d", code)
	}
}

func TestStopNeedsYes(t *testing.T) {
	env, e, _ := testEnv(t)
	code, _, errOut := runCmd(t, env, "stop", "worker")
	if code != 2 || !strings.Contains(errOut, "--yes") || len(e.calls) != 0 {
		t.Fatalf("stop without --yes = %d, %q, calls %q", code, errOut, e.calls)
	}
	if code, _, _ := runCmd(t, env, "stop", "worker", "--yes"); code != 0 {
		t.Fatalf("stop worker --yes = %d", code)
	}
	if e.calls[0] != "stop "+cID4+" cli" {
		t.Fatalf("calls = %q", e.calls)
	}
}

func TestRetryAStuckSessionByName(t *testing.T) {
	env, e, _ := testEnv(t)
	if code, _, _ := runCmd(t, env, "retry", "nightly"); code != 0 || e.calls[0] != "retry "+cID3+" cli" {
		t.Fatalf("retry nightly = %d, %q", code, e.calls)
	}
}

func TestRefusedIsExit1(t *testing.T) {
	env, e, _ := testEnv(t)
	e.refuse = "unbabysit"
	code, out, errOut := runCmd(t, env, "unbabysit", "nightly")
	if code != 1 || out != "" || !strings.Contains(errOut, "not being babysat") {
		t.Fatalf("refused = %d %q %q", code, out, errOut)
	}
	code, out, _ = runCmd(t, env, "unbabysit", "nightly", "--json")
	if doc := oneJSON(t, out); code != 1 || doc["ok"] != false {
		t.Fatalf("refused --json = %d %v", code, doc)
	}
}

func TestAmbiguousAndUnknownAreExit4(t *testing.T) {
	env, e, _ := testEnv(t)
	v := ctlView()
	v.Sessions[1].Name = "api"
	e.view = v
	code, _, errOut := runCmd(t, env, "babysit", "api")
	if code != 4 || !strings.Contains(errOut, "aaaaaaaa") || !strings.Contains(errOut, "bbbbbbbb") {
		t.Fatalf("ambiguous = %d %q", code, errOut)
	}
	if code, _, errOut := runCmd(t, env, "show", "nope"); code != 4 || !strings.Contains(errOut, "ccbabysitter list") {
		t.Fatalf("unknown = %d %q", code, errOut)
	}
	if len(e.calls) != 0 {
		t.Fatalf("an ambiguous or unknown name reached the engine: %q", e.calls)
	}
}

func TestSelfOutsideASessionIsExit4(t *testing.T) {
	env, e, _ := testEnv(t)
	code, _, errOut := runCmd(t, env, "babysit", "self")
	if code != 4 || !strings.Contains(errOut, "not running inside a Claude Code session") || len(e.calls) != 0 {
		t.Fatalf("self = %d %q", code, errOut)
	}
}

func TestSelfInsideABabysatSession(t *testing.T) {
	env, e, _ := testEnv(t)
	env.self = func(supervise.View) (int, bool) { return 104, true }
	if code, _, _ := runCmd(t, env, "unbabysit", "self"); code != 0 || e.calls[0] != "unbabysit "+cID4+" cli" {
		t.Fatalf("unbabysit self = %d %q", code, e.calls)
	}
}

func TestJSONErrorIsOneDocument(t *testing.T) {
	env, _, _ := testEnv(t)
	code, out, _ := runCmd(t, env, "babysit", "nope", "--json")
	doc := oneJSON(t, out)
	if code != 4 || doc["ok"] != false || doc["code"] != "not-found" || doc["error"] == "" {
		t.Fatalf("doc = %v, code %d", doc, code)
	}
}

func TestNotRunningCases(t *testing.T) {
	stale := func(t *testing.T) string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := "http://" + ln.Addr().String()
		ln.Close()
		return addr
	}
	other := func(t *testing.T) string {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(404)
			w.Write([]byte(`{"message":"not here"}`))
		}))
		t.Cleanup(ts.Close)
		return ts.URL
	}
	cases := map[string]func(t *testing.T) string{
		"no page-url":   func(*testing.T) string { return "" },
		"stale address": stale,
		"other program": other,
	}
	for name, addr := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if a := addr(t); a != "" {
				if err := state.SavePageURL(dir, a); err != nil {
					t.Fatal(err)
				}
			}
			env := controlEnv{stateDir: dir, self: func(supervise.View) (int, bool) { return 0, false }}
			for _, args := range [][]string{{"status"}, {"list"}, {"babysit", "self"}, {"activity"}} {
				code, _, errOut := runCmd(t, env, args...)
				if code != 3 || !strings.Contains(errOut, "Start it with: ccbabysitter") {
					t.Errorf("%v = %d, %q", args, code, errOut)
				}
			}
			code, out, _ := runCmd(t, env, "list", "--json")
			if doc := oneJSON(t, out); code != 3 || doc["code"] != "not-running" {
				t.Errorf("list --json = %d %v", code, doc)
			}
		})
	}
}

func TestStatusJSONWhenStopped(t *testing.T) {
	env := controlEnv{stateDir: t.TempDir(), self: func(supervise.View) (int, bool) { return 0, false }}
	code, out, _ := runCmd(t, env, "status", "--json")
	doc := oneJSON(t, out)
	if code != 3 || doc["running"] != false || len(doc) != 2 {
		t.Fatalf("status --json stopped = %d %v", code, doc)
	}
}

// Not running, every system gives the same way to start it: a plain run,
// which sets the service up again even after uninstall removed it.
func TestNotRunningSaysToRunCCBabysitter(t *testing.T) {
	env := controlEnv{stateDir: t.TempDir(), self: func(supervise.View) (int, bool) { return 0, false }}
	_, _, errOut := runCmd(t, env, "list")
	if !strings.Contains(errOut, "Start it with: ccbabysitter") || strings.Contains(errOut, "systemctl") {
		t.Fatalf("%q", errOut)
	}
}

func TestSettingsShowAndChange(t *testing.T) {
	env, e, _ := testEnv(t)
	code, out, _ := runCmd(t, env, "settings")
	if code != 0 || !strings.Contains(out, "auto-babysit: on") || !strings.Contains(out, "theme: auto") {
		t.Fatalf("settings = %d\n%s", code, out)
	}
	if code, _, _ := runCmd(t, env, "settings", "theme", "dark"); code != 0 || e.view.Settings.Theme != "dark" || !e.view.Settings.AutoBabysit {
		t.Fatalf("settings theme dark = %d, %+v", code, e.view.Settings)
	}
	if code, _, _ := runCmd(t, env, "settings", "auto-babysit", "off"); code != 0 || e.view.Settings.AutoBabysit {
		t.Fatalf("auto-babysit off = %d, %+v", code, e.view.Settings)
	}
	for _, bad := range [][]string{{"settings", "theme", "pink"}, {"settings", "nope", "on"}, {"settings", "theme"}} {
		if code, _, errOut := runCmd(t, env, bad...); code != 2 || errOut == "" {
			t.Errorf("%v = %d %q", bad, code, errOut)
		}
	}
}

func TestURLFlagTargetsAnotherCopy(t *testing.T) {
	other, _, ts := testEnv(t)
	keyed := ts.URL + "/?token=" + state.ReadPageKey(other.stateDir)
	env := controlEnv{stateDir: t.TempDir(), self: func(supervise.View) (int, bool) { return 0, false }}
	if code, out, _ := runCmd(t, env, "list", "--url", keyed); code != 0 || !strings.Contains(out, "api") {
		t.Fatalf("list --url = %d\n%s", code, out)
	}
	// Without the key in the address, this state folder's key goes only to
	// the address its own live copy saved. There is no key here at all.
	if code, out, _ := runCmd(t, env, "list", "--url", ts.URL, "--json"); code != 1 || oneJSON(t, out)["code"] != "no-key" {
		t.Fatalf("list --url without the key = %d\n%s", code, out)
	}
	// A folder that holds the very key, with its lock held, still does not
	// send it to an address that is not the one it saved.
	if err := os.WriteFile(filepath.Join(env.stateDir, "page-key"), []byte(state.ReadPageKey(other.stateDir)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := state.SavePageURL(env.stateDir, "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	holdLock(t, env.stateDir)
	if code, out, _ := runCmd(t, env, "list", "--url", ts.URL, "--json"); code != 1 || oneJSON(t, out)["code"] != "no-key" {
		t.Fatalf("list --url to an address not saved = %d\n%s", code, out)
	}
	// The folder's own saved address, given bare, gets the folder's key.
	if code, out, _ := runCmd(t, other, "list", "--url", ts.URL); code != 0 || !strings.Contains(out, "api") {
		t.Fatalf("list --url to the saved address = %d\n%s", code, out)
	}
	if code, _, _ := runCmd(t, env, "list", "--url", "http://example.com:1"); code != 2 {
		t.Fatalf("list --url example.com = %d, want 2", code)
	}
}

func TestActivityForOneSessionUsesItsLabels(t *testing.T) {
	env, e, _ := testEnv(t)
	e.log.Info("worker", "babysitting in the background")
	e.log.Info("api", "babysitting in a terminal window")
	code, out, _ := runCmd(t, env, "activity", "worker", "--json")
	var doc struct {
		Entries []state.Entry `json:"entries"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil || len(doc.Entries) != 1 || doc.Entries[0].Session != "worker" {
		t.Fatalf("activity worker --json = %d\n%s", code, out)
	}
	_, out, _ = runCmd(t, env, "activity", "--json")
	if json.Unmarshal([]byte(out), &doc) != nil || len(doc.Entries) != 2 {
		t.Fatalf("activity --json:\n%s", out)
	}
}

func TestUsageErrorsAreExit2(t *testing.T) {
	env, _, _ := testEnv(t)
	for _, args := range [][]string{{"babysit"}, {"babysit", "a", "b"}, {"list", "--nope"}, {"activity", "-n", "x"}, {"status", "extra"}} {
		if code, _, errOut := runCmd(t, env, args...); code != 2 || !strings.Contains(errOut, "ccbabysitter help") {
			t.Errorf("%v = %d %q", args, code, errOut)
		}
	}
}

func TestParseControlArgsSplitsFlagsFromWords(t *testing.T) {
	a, err := parseControlArgs("stop", []string{"api", "--yes", "--url", "http://127.0.0.1:5"})
	if err != nil || !a.yes || a.url != "http://127.0.0.1:5" || len(a.pos) != 1 || a.pos[0] != "api" {
		t.Fatalf("%+v %v", a, err)
	}
	a, err = parseControlArgs("activity", []string{"-n=3", "--", "-weird-name"})
	if err != nil || a.n != 3 || len(a.pos) != 1 || a.pos[0] != "-weird-name" {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := parseControlArgs("list", []string{"--yes"}); err == nil {
		t.Fatal("list accepted --yes")
	}
}

func TestHelpFlagPrintsThePageWithoutContactingTheServer(t *testing.T) {
	env, e, _ := testEnv(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"babysit", "--help"}, "ccbabysitter babysit"},
		{[]string{"stop", "-h", "--json"}, "ccbabysitter stop"},
		{[]string{"list", "-help"}, "ccbabysitter list"},
	} {
		code, out, errOut := runCmd(t, env, c.args...)
		if code != 0 || !strings.Contains(out, c.want) || errOut != "" {
			t.Errorf("%v = %d %q %q", c.args, code, out, errOut)
		}
	}
	if len(e.calls) != 0 {
		t.Fatalf("help reached the engine: %q", e.calls)
	}
	// With nothing running, help still answers.
	stopped := controlEnv{stateDir: t.TempDir(), self: func(supervise.View) (int, bool) { return 0, false }}
	if code, out, _ := runCmd(t, stopped, "babysit", "--help"); code != 0 || !strings.Contains(out, "ccbabysitter babysit") {
		t.Fatalf("babysit --help with nothing running = %d %q", code, out)
	}
}

// A copy that takes the request and then drops the connection, which is
// what a slow answer looks like once the client gives up, is not "not
// running": the action may still finish.
func TestNoAnswerIsExit1WithItsOwnCode(t *testing.T) {
	dir := t.TempDir()
	key, err := state.PageKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/state" {
			web.NewServer(&ctlEngine{view: ctlView()}, nil, "0.4.0", key).Handler().ServeHTTP(w, r)
			return
		}
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			conn.Close()
		}
	}))
	t.Cleanup(ts.Close)
	if err := state.SavePageURL(dir, ts.URL); err != nil {
		t.Fatal(err)
	}
	holdLock(t, dir)
	env := controlEnv{stateDir: dir, self: func(supervise.View) (int, bool) { return 0, false }}
	code, out, errOut := runCmd(t, env, "babysit", "api", "--json")
	doc := oneJSON(t, out)
	want := "CC Babysitter did not answer in time. The action may still finish. See ccbabysitter activity."
	if code != 1 || doc["code"] != "no-answer" || doc["error"] != want || !strings.Contains(errOut, want) {
		t.Fatalf("code %d, doc %v, stderr %q", code, doc, errOut)
	}
}

func keysOf(m map[string]any) string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// The JSON names are a promise to agents: fields are only ever added. This
// pins them, so a rename breaks here before it breaks an agent.
func TestJSONKeysArePinned(t *testing.T) {
	env, _, _ := testEnv(t)
	check := func(what string, got map[string]any, want string) {
		t.Helper()
		if k := keysOf(got); k != want {
			t.Errorf("%s keys:\n got %s\nwant %s", what, k, want)
		}
	}
	child := func(doc map[string]any, key string) map[string]any {
		t.Helper()
		m, ok := doc[key].(map[string]any)
		if !ok {
			t.Fatalf("%s is not an object in %v", key, doc)
		}
		return m
	}

	_, out, _ := runCmd(t, env, "status", "--json")
	doc := oneJSON(t, out)
	check("status", doc, "babysat,headless,keepAwake,running,schema,sessions,url,version")
	check("status keepAwake", child(doc, "keepAwake"), "held,supported")

	_, out, _ = runCmd(t, env, "show", "api", "--json")
	doc = oneJSON(t, out)
	check("show", doc, "schema,session")
	check("show api session", child(doc, "session"),
		"app,apps,babysat,canStop,canUnbabysit,folder,id,name,pid,remoteControl,running,scheduledTask,shortId,status,tokens,uptimeSeconds")
	check("show tokens", child(child(doc, "session"), "tokens"), "cacheRead,cacheWrite,input,output")

	// A babysat session adds state, which is only there when set.
	_, out, _ = runCmd(t, env, "show", "worker", "--json")
	check("show worker session", child(oneJSON(t, out), "session"),
		"app,apps,babysat,canStop,canUnbabysit,folder,id,name,pid,remoteControl,running,scheduledTask,shortId,state,status,tokens,uptimeSeconds")

	_, out, _ = runCmd(t, env, "settings", "--json")
	doc = oneJSON(t, out)
	check("settings", doc, "schema,settings")
	check("settings object", child(doc, "settings"), "autoBabysit,autostart,openBrowser,theme")
}

// status gives the address with the page's key, the one way to get it
// again after the banner has scrolled away.
func TestStatusShowsTheKeyedAddress(t *testing.T) {
	env, _, ts := testEnv(t)
	keyed := ts.URL + "/?token=" + state.ReadPageKey(env.stateDir)
	code, out, _ := runCmd(t, env, "status")
	if code != 0 || !strings.Contains(out, "CC Babysitter 0.4.0 is running at "+keyed+"\n") {
		t.Fatalf("status = %d\n%s", code, out)
	}
	code, out, _ = runCmd(t, env, "status", "--json")
	if doc := oneJSON(t, out); code != 0 || doc["url"] != keyed {
		t.Fatalf("status --json = %d %v", code, doc)
	}
}

// A copy that answers without the right key is running, so a wrong or
// missing key is exit 1 with its own code, never not running.
func TestWrongKeyIsNoKey(t *testing.T) {
	env, e, _ := testEnv(t)
	if err := os.WriteFile(filepath.Join(env.stateDir, "page-key"), []byte(strings.Repeat("cd", 32)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := "This needs the page's key. Run ccbabysitter to open the page again, or open the address ccbabysitter status prints."
	for _, args := range [][]string{{"status"}, {"list"}, {"babysit", "api"}, {"activity"}, {"settings", "theme", "dark"}, {"quit"}} {
		code, out, errOut := runCmd(t, env, append(args, "--json")...)
		doc := oneJSON(t, out)
		if code != 1 || doc["code"] != "no-key" || doc["error"] != want || !strings.Contains(errOut, want) {
			t.Errorf("%v = %d, %v, stderr %q", args, code, doc, errOut)
		}
	}
	if len(e.calls) != 0 {
		t.Fatalf("the engine was reached without the key: %v", e.calls)
	}
}

// After a crash the saved address may name a port that another account's
// server listens on now. With no live copy holding the folder's lock, the
// command line says it is not running and sends nothing there.
func TestStaleAddressIsNotRunningAndGetsNoKey(t *testing.T) {
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.String()+" "+r.Header.Get("Authorization"))
		w.Write([]byte(`{"version":"0.4.0"}`))
	}))
	t.Cleanup(ts.Close)
	dir := t.TempDir()
	if _, err := state.PageKey(dir); err != nil {
		t.Fatal(err)
	}
	if err := state.SavePageURL(dir, ts.URL); err != nil {
		t.Fatal(err)
	}
	env := controlEnv{stateDir: dir, self: func(supervise.View) (int, bool) { return 0, false }}
	for _, args := range [][]string{{"status"}, {"list"}, {"babysit", "api"}, {"activity"}, {"quit"}} {
		if code, _, errOut := runCmd(t, env, args...); code != 3 || !strings.Contains(errOut, "not running") {
			t.Errorf("%v = %d %q", args, code, errOut)
		}
	}
	if len(seen) != 0 {
		t.Fatalf("the saved address was asked: %q", seen)
	}
}

// testEnvWithQuit is testEnv with a quit hook on the server, which reports
// each call on the returned channel.
func testEnvWithQuit(t *testing.T) (controlEnv, chan struct{}) {
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
	srv := web.NewServer(&ctlEngine{view: ctlView(), log: log}, log, "0.4.0", key)
	quits := make(chan struct{}, 1)
	srv.OnQuit(func() { quits <- struct{}{} })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	if err := state.SavePageURL(dir, ts.URL); err != nil {
		t.Fatal(err)
	}
	holdLock(t, dir)
	return controlEnv{stateDir: dir}, quits
}

func TestQuitAsksTheRunningCopyToQuit(t *testing.T) {
	env, quits := testEnvWithQuit(t)
	code, out, _ := runCmd(t, env, "quit")
	if code != 0 || !strings.Contains(out, "CC Babysitter is quitting.") {
		t.Fatalf("quit = %d %q", code, out)
	}
	select {
	case <-quits:
	case <-time.After(2 * time.Second):
		t.Fatal("the running copy was not asked to quit")
	}

	env, _ = testEnvWithQuit(t)
	code, out, _ = runCmd(t, env, "quit", "--json")
	if doc := oneJSON(t, out); code != 0 || doc["ok"] != true || doc["schema"] != float64(1) {
		t.Fatalf("quit --json = %d %v", code, doc)
	}
}

func TestQuitTakesNoWords(t *testing.T) {
	env, quits := testEnvWithQuit(t)
	if code, _, errOut := runCmd(t, env, "quit", "now"); code != 2 || !strings.Contains(errOut, "quit takes no words") {
		t.Fatalf("quit now = %d %q", code, errOut)
	}
	if len(quits) != 0 {
		t.Fatal("a usage error asked the copy to quit")
	}
}

func TestQuitWhenNotRunningIsExit3(t *testing.T) {
	env := controlEnv{stateDir: t.TempDir()}
	if code, _, _ := runCmd(t, env, "quit"); code != 3 {
		t.Fatalf("quit with nothing running = %d, want 3", code)
	}
}

// A running copy from before quit existed answers /api/quit with a plain
// 404. That copy is running, so quit must not say it is not: it says the
// running copy is too old to quit this way and how to quit it instead.
func TestQuitAgainstAnOlderCopySaysHowToQuitIt(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/quit" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"version":"0.4.2"}`))
	}))
	t.Cleanup(ts.Close)
	dir := t.TempDir()
	if _, err := state.PageKey(dir); err != nil {
		t.Fatal(err)
	}
	if err := state.SavePageURL(dir, ts.URL); err != nil {
		t.Fatal(err)
	}
	holdLock(t, dir)
	env := controlEnv{stateDir: dir}

	code, _, errOut := runCmd(t, env, "quit")
	// A copy started at login has no window to press Ctrl+C in: the answer
	// names how to stop it on each system too.
	if code != 1 || strings.Contains(errOut, "not running") || !strings.Contains(errOut, "older version") || !strings.Contains(errOut, "Ctrl+C") ||
		!strings.Contains(errOut, "systemctl --user stop ccbabysitter") || !strings.Contains(errOut, "launchctl bootout gui/") || !strings.Contains(errOut, "Task Manager") {
		t.Fatalf("quit against an older copy = %d %q", code, errOut)
	}
	code, out, _ := runCmd(t, env, "quit", "--json")
	if doc := oneJSON(t, out); code != 1 || doc["ok"] != false {
		t.Fatalf("quit --json against an older copy = %d %v", code, doc)
	}
}

func TestClientLaunchURL(t *testing.T) {
	env, e, ts := testEnv(t)
	e.view.URL = ts.URL
	c, err := client.New(env.stateDir, "")
	if err != nil {
		t.Fatal(err)
	}
	u, err := c.LaunchURL(context.Background())
	if err != nil || !strings.HasPrefix(u, ts.URL+"/?token=") || strings.Contains(u, state.ReadPageKey(env.stateDir)) {
		t.Fatalf("LaunchURL = %q, %v", u, err)
	}
}

// A Claude Desktop scheduled task's run is listed after the other
// sessions, under its own heading, and its JSON and show say what it is.
// A machine running only runs still lists them.
func TestListPutsScheduledTaskRunsApart(t *testing.T) {
	env, e, _ := testEnv(t)
	e.view.Sessions = append(e.view.Sessions, supervise.SessionView{
		ID: "45454545-0000-4000-8000-000000000045", ShortID: "45454545", Name: "Daily report", Cwd: "/w/notes", PID: 145,
		Host: claude.HostDesktop, Live: []claude.Host{claude.HostDesktop}, Status: "busy", ScheduledTask: true,
	})
	code, out, _ := runCmd(t, env, "list")
	head := strings.Index(out, "Scheduled task runs, never babysat:")
	if code != 0 || head < 0 || strings.Index(out, "Daily report") < head || strings.Index(out, "api") > head {
		t.Fatalf("list = %d:\n%s", code, out)
	}
	_, out, _ = runCmd(t, env, "list", "--json")
	var doc struct {
		Sessions []client.Session `json:"sessions"`
	}
	if json.Unmarshal([]byte(out), &doc) != nil {
		t.Fatalf("list --json:\n%s", out)
	}
	runs := 0
	for _, s := range doc.Sessions {
		if s.ScheduledTask {
			runs++
		}
	}
	if runs != 1 || !strings.Contains(out, `"scheduledTask":true`) {
		t.Fatalf("list --json has %d scheduled runs:\n%s", runs, out)
	}
	_, out, _ = runCmd(t, env, "show", "45454545")
	if !strings.Contains(out, "Scheduled task: yes") {
		t.Fatalf("show:\n%s", out)
	}
	_, out, _ = runCmd(t, env, "show", "api")
	if strings.Contains(out, "Scheduled task") {
		t.Fatalf("show of an ordinary session:\n%s", out)
	}

	e.view.Sessions = e.view.Sessions[2:]
	e.view.Watches = nil
	_, out, _ = runCmd(t, env, "list")
	if strings.Contains(out, "No Claude Code sessions are running.") || !strings.Contains(out, "Daily report") {
		t.Fatalf("only runs:\n%s", out)
	}
}

// open asks the running copy for a one-time address and opens the browser
// on it, and says so without printing any key.
func TestOpenOpensTheBrowserWithALaunchAddress(t *testing.T) {
	env, e, ts := testEnv(t)
	e.view.URL = ts.URL // the launch address is built from the view's URL
	var opened string
	env.open = func(u string) error { opened = u; return nil }
	code, out, _ := runCmd(t, env, "open")
	if code != 0 || out != "Opened the CC Babysitter page in your browser.\n" {
		t.Fatalf("open = %d %q", code, out)
	}
	if !strings.HasPrefix(opened, ts.URL+"/?token=") {
		t.Fatalf("opened %q", opened)
	}
}

// Whatever open prints never carries the page's key.
func TestOpenPrintsNoKey(t *testing.T) {
	env, e, ts := testEnv(t)
	e.view.URL = ts.URL
	env.open = func(string) error { return nil }
	key := state.ReadPageKey(env.stateDir)
	for _, args := range [][]string{{"open"}, {"open", "--json"}} {
		_, out, errOut := runCmd(t, env, args...)
		if key == "" || strings.Contains(out+errOut, key) || strings.Contains(out+errOut, "token=") {
			t.Fatalf("%v printed the key: %q %q", args, out, errOut)
		}
	}
}

// On a machine with no display there is no browser to open: open says how
// to connect from another computer, on a line of its own, and opens
// nothing.
func TestOpenOnAHeadlessMachinePrintsTheTunnel(t *testing.T) {
	env, e, _ := testEnv(t)
	e.view.URL = "http://127.0.0.1:47391/"
	env.headless = func() bool { return true }
	opened := false
	env.open = func(string) error { opened = true; return nil }
	code, out, _ := runCmd(t, env, "open")
	if code != 0 || opened || !strings.Contains(out, "\n  ssh -L ") || !strings.Contains(out, "ccbabysitter status") {
		t.Fatalf("open = %d %q, opened %v", code, out, opened)
	}
}

// Whether there is a display is decided where open runs, as a plain
// ccbabysitter does, not where CC Babysitter runs: a service set up as a
// server has no display of its own while the person runs open at the
// screen, and the other way round over ssh.
func TestOpenDecidesTheDisplayWhereItRuns(t *testing.T) {
	env, e, ts := testEnv(t)
	e.view.URL = ts.URL
	e.view.Env.Headless = true
	env.headless = func() bool { return false }
	opened := false
	env.open = func(string) error { opened = true; return nil }
	if code, out, _ := runCmd(t, env, "open"); code != 0 || !opened || strings.Contains(out, "ssh -L") {
		t.Fatalf("headless service, caller at a screen: open = %d %q, opened %v", code, out, opened)
	}
	e.view.Env.Headless = false
	env.headless = func() bool { return true }
	opened = false
	if code, out, _ := runCmd(t, env, "open"); code != 0 || opened || !strings.Contains(out, "ssh -L ") {
		t.Fatalf("service with a display, caller over ssh: open = %d %q, opened %v", code, out, opened)
	}
}

// With no port to forward, open prints no broken ssh line.
func TestOpenWithoutAPortPrintsNoTunnel(t *testing.T) {
	env, e, _ := testEnv(t)
	e.view.URL = ""
	env.headless = func() bool { return true }
	env.open = func(string) error { t.Fatal("opened a browser"); return nil }
	code, out, _ := runCmd(t, env, "open")
	if code != 0 || strings.Contains(out, "ssh -L") || !strings.Contains(out, "no display") {
		t.Fatalf("open = %d %q", code, out)
	}
}

// A browser that does not start is an error with what to do instead.
func TestOpenSaysWhenTheBrowserDidNotOpen(t *testing.T) {
	env, e, ts := testEnv(t)
	e.view.URL = ts.URL
	env.open = func(string) error { return errors.New("no browser") }
	code, _, errOut := runCmd(t, env, "open")
	if code != 1 || !strings.Contains(errOut, "ccbabysitter status") {
		t.Fatalf("open = %d %q", code, errOut)
	}
}

// open is a control command: words after it are wrong, and when nothing
// runs it says so with exit code 3, starting nothing.
func TestOpenUsageAndNotRunning(t *testing.T) {
	env, _, _ := testEnv(t)
	if code, _, _ := runCmd(t, env, "open", "extra"); code != 2 {
		t.Fatalf("open extra = %d", code)
	}
	stopped := controlEnv{stateDir: t.TempDir(), self: func(supervise.View) (int, bool) { return 0, false }}
	if code, _, errOut := runCmd(t, stopped, "open"); code != 3 || !strings.Contains(errOut, "Start it with: ccbabysitter") {
		t.Fatalf("open, not running = %d %q", code, errOut)
	}
}
