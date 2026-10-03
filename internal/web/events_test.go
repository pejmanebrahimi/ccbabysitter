package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// readEvent scans the SSE body for the next "event: " line and returns its
// name, failing the test if none arrives within the deadline.
func readEvent(t *testing.T, rd *bufio.Reader) string {
	t.Helper()
	type result struct {
		name string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		for {
			line, err := rd.ReadString('\n')
			if strings.HasPrefix(line, "event: ") {
				done <- result{name: strings.TrimSpace(line[len("event: "):])}
				return
			}
			if err != nil {
				done <- result{err: err}
				return
			}
		}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r.name
	case <-time.After(2 * time.Second):
		t.Fatal("no event arrived")
		return ""
	}
}

func TestEventsSendsViewOnConnectAndOnNotify(t *testing.T) {
	ts, _, s := newTS(t)
	r, err := keyed.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if ct := r.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatal(ct)
	}
	rd := bufio.NewReader(r.Body)
	if ev := readEvent(t, rd); ev != "view" {
		t.Fatalf("first event is the view, got %q", ev)
	}
	s.Notify()
	if ev := readEvent(t, rd); ev != "view" {
		t.Fatalf("notify pushes a view, got %q", ev)
	}
}

func TestEventsForwardsActivityEntries(t *testing.T) {
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &fakeEngine{}
	s := NewServer(e, log, "0.1.0", testKey)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	r, err := keyed.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	rd := bufio.NewReader(r.Body)
	if ev := readEvent(t, rd); ev != "view" {
		t.Fatalf("first event is the view, got %q", ev)
	}
	log.Info("s1", "did a thing")
	if ev := readEvent(t, rd); ev != "activity" {
		t.Fatalf("expected an activity event, got %q", ev)
	}
}

func TestEventsCapsConcurrentClients(t *testing.T) {
	ts, _, s := newTS(t)
	defer ts.Close()
	s.hub.maxClients = 2

	var bodies []*http.Response
	for i := 0; i < 2; i++ {
		r, err := keyed.Get(ts.URL + "/api/events")
		if err != nil {
			t.Fatal(err)
		}
		if r.StatusCode != http.StatusOK {
			t.Fatalf("client %d: want 200, got %d", i, r.StatusCode)
		}
		bodies = append(bodies, r)
	}
	defer func() {
		for _, r := range bodies {
			r.Body.Close()
		}
	}()

	r, err := keyed.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503 once the cap is reached, got %d", r.StatusCode)
	}
	var eb errorBody
	if err := json.NewDecoder(r.Body).Decode(&eb); err != nil {
		t.Fatal(err)
	}
	if eb.OK || eb.Message == "" {
		t.Fatalf("503 body must be the error shape with a sentence: %+v", eb)
	}
}

func TestNotifyIsCheapWithNoClientAndWithASlowClient(t *testing.T) {
	ts, _, s := newTS(t)
	defer ts.Close()

	start := time.Now()
	for i := 0; i < 100; i++ {
		s.Notify()
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("100 Notify calls with no client took %v", elapsed)
	}

	r, err := keyed.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	// A slow client: nothing reads the body from here on, so its buffered
	// channel fills up quickly. Notify must still return fast.
	start = time.Now()
	for i := 0; i < 100; i++ {
		s.Notify()
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("100 Notify calls with a slow client took %v", elapsed)
	}
}

// A stream handler holds a connection open with nothing in the request to
// notice a shutdown through, so without being told it would keep the
// shutdown waiting for its whole grace period.
func TestShutdownEndsOpenStreamsPromptly(t *testing.T) {
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(&fakeEngine{}, log, "0.1.0", testKey)
	srv := &http.Server{Handler: s.Handler()}
	srv.RegisterOnShutdown(s.Shutdown)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()

	r, err := keyed.Get("http://" + ln.Addr().String() + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	rd := bufio.NewReader(r.Body)
	if ev := readEvent(t, rd); ev != "view" {
		t.Fatalf("first event is the view, got %q", ev)
	}

	const grace = 3 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	start := time.Now()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if took := time.Since(start); took > grace/3 {
		t.Fatalf("shutdown waited %v for an open stream, well into a %v grace period", took, grace)
	}
}

func TestFormatSSE(t *testing.T) {
	got := string(formatSSE("view", []byte(`{"a":1}`)))
	if want := "event: view\ndata: {\"a\":1}\n\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
