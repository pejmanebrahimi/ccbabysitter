package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/client"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
)

// The control commands against a real running copy in demo mode: the same
// serve path the user starts, the same HTTP server and guard, scripted
// sessions only, so nothing real is touched.
func TestControlCommandsAgainstTheDemo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncWriter{}
	done := make(chan int, 1)
	dirCh := make(chan string, 1)
	go func() {
		done <- serve(ctx, serveOptions{Demo: true, NoOpen: true, Port: 0, onStateDir: func(d string) { dirCh <- d }}, out)
	}()
	<-dirCh
	// The banner's full address, with the demo's own key, as a person or
	// an agent would copy it.
	url := waitForPageURL(t, out)
	if !strings.Contains(url, "/?token=") {
		t.Fatalf("the banner's address has no key: %q", url)
	}

	env := controlEnv{stateDir: t.TempDir(), self: func(supervise.View) (int, bool) { return 0, false }}
	must := func(want int, args ...string) string {
		t.Helper()
		code, stdout, stderr := runCmd(t, env, append(args, "--url", url)...)
		if code != want {
			t.Fatalf("%v = %d, want %d\nstdout: %s\nstderr: %s", args, code, want, stdout, stderr)
		}
		return stdout
	}

	if status := must(0, "status"); !strings.Contains(status, " is running at "+url+"\n") {
		t.Fatalf("status does not give the address with the key:\n%s", status)
	}
	var doc struct {
		Sessions []client.Session `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(must(0, "list", "--json")), &doc); err != nil {
		t.Fatal(err)
	}
	var free, stoppable *client.Session
	for i := range doc.Sessions {
		s := &doc.Sessions[i]
		if free == nil && s.Running && !s.Babysat && s.App != "other" && !s.ScheduledTask {
			free = s
		}
		if stoppable == nil && s.CanStop {
			stoppable = s
		}
	}
	if free == nil || stoppable == nil {
		t.Fatalf("the demo has no running unbabysat session or no stoppable one: %+v", doc.Sessions)
	}
	must(0, "babysit", free.ShortID)
	must(0, "show", free.ShortID)
	must(0, "unbabysit", free.ShortID)
	must(2, "stop", stoppable.ShortID)
	must(0, "stop", stoppable.ShortID, "--yes")
	act := must(0, "activity", "--json")
	if !strings.Contains(act, "from the command line") {
		t.Fatalf("activity does not show the command line:\n%s", act)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop")
	}
}
