package web

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// pingInterval is how often an idle connection gets a comment line, which
// is enough to keep most reverse proxies and SSH tunnels from deciding the
// connection is dead and closing it.
const pingInterval = 25 * time.Second

// maxEventClients caps how many browser tabs can hold an event stream open
// at once. Nothing about a legitimate use of this page needs more than a
// handful, and an unbounded number of held-open connections is itself a
// resource a hostile page could try to exhaust.
const maxEventClients = 32

// eventHub fans a "view" event out to every client of GET /api/events, and
// forwards the activity log to each client as its own "activity" events.
// One broadcaster goroutine does the work of building and marshalling the
// view; it is started the first time a client connects, since there is
// nothing to broadcast to before that. notify() only ever signals that
// goroutine and never blocks, which is what lets a supervisor call it from
// its own loop.
type eventHub struct {
	engine Engine
	log    *state.Log

	signal chan struct{}
	once   sync.Once

	// done is closed when the server is shutting down. A stream handler
	// has no request body to notice a shutdown through, so without this it
	// would sit on its own request context until the browser gave up,
	// which is far longer than the grace period a shutdown allows.
	done     chan struct{}
	closeOne sync.Once

	mu         sync.Mutex
	clients    map[chan []byte]struct{}
	maxClients int

	// lastMarshalErr remembers the last encoding failure reported, so a
	// view that cannot be encoded is explained once rather than on every
	// single update for as long as it stays broken. lastGood is the newest
	// view that could be encoded: a page opened while the current one
	// cannot be is shown that instead of nothing at all, since a blank
	// page with no explanation is the worst thing to hand somebody.
	errMu          sync.Mutex
	lastMarshalErr string
	lastGood       []byte
}

func newEventHub(e Engine, log *state.Log) *eventHub {
	return &eventHub{
		engine:     e,
		log:        log,
		signal:     make(chan struct{}, 1),
		done:       make(chan struct{}),
		clients:    map[chan []byte]struct{}{},
		maxClients: maxEventClients,
	}
}

// close tells every open stream handler to return. It is safe to call more
// than once, and it never waits for anything.
func (h *eventHub) close() {
	h.closeOne.Do(func() { close(h.done) })
}

// notify wakes the broadcaster goroutine. The channel is buffered by one
// and the send is non-blocking, so a caller on a hot path (the
// supervisor's own loop) never waits, and a burst of calls collapses into
// a single broadcast once the goroutine gets to run.
func (h *eventHub) notify() {
	select {
	case h.signal <- struct{}{}:
	default:
	}
}

// ensureStarted starts the broadcaster goroutine once, on the first client
// connection. It never stops for the life of the process, which matches
// every other long-lived goroutine this program starts.
func (h *eventHub) ensureStarted() {
	h.once.Do(func() {
		go h.broadcastLoop()
	})
}

func (h *eventHub) broadcastLoop() {
	for range h.signal {
		h.broadcastView()
	}
}

// broadcastView builds the view and marshals it exactly once, then hands
// the same bytes to every client. A client whose buffer is full (it is not
// reading fast enough) simply misses this update; it gets the next one
// instead of ever making this loop wait.
func (h *eventHub) broadcastView() {
	msg, ok := h.viewMessage()
	if !ok {
		return
	}
	h.mu.Lock()
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
		}
	}
	h.mu.Unlock()
}

// viewMessage encodes the current view as an event to broadcast. It
// reports false when the view cannot be encoded, so a client that is
// already connected simply misses this update and keeps what it has rather
// than being handed the same stale view over and over.
func (h *eventHub) viewMessage() ([]byte, bool) {
	data, err := json.Marshal(h.engine.View())
	if err != nil {
		// Live updates stopping for good, silently, is the worst way this
		// can go wrong: the page keeps showing numbers that stopped being
		// true minutes ago and nothing says so. The reason is written down
		// once, and the last view that did encode is kept for whoever
		// connects next.
		h.reportMarshalError(err)
		return nil, false
	}
	h.rememberGoodView(data)
	return formatSSE("view", data), true
}

// openingMessage is the first event a newly connected client is sent. A
// client that has nothing yet is worse off than one that is merely behind,
// so when the current view cannot be encoded it is given the last one that
// could rather than an empty page with no explanation.
func (h *eventHub) openingMessage() ([]byte, bool) {
	if msg, ok := h.viewMessage(); ok {
		return msg, true
	}
	if cached := h.goodView(); cached != nil {
		return formatSSE("view", cached), true
	}
	return nil, false
}

// rememberGoodView keeps the newest view that could be encoded, and
// forgets any failure that came before it: a failure that happens again
// after things had recovered is news, not a repeat.
func (h *eventHub) rememberGoodView(data []byte) {
	kept := make([]byte, len(data))
	copy(kept, data)
	h.errMu.Lock()
	h.lastGood = kept
	h.lastMarshalErr = ""
	h.errMu.Unlock()
}

// goodView returns the newest view that could be encoded, or nil when
// none ever has been.
func (h *eventHub) goodView() []byte {
	h.errMu.Lock()
	defer h.errMu.Unlock()
	return h.lastGood
}

// reportMarshalError writes an encoding failure to the activity log the
// first time it is seen, and again only if the failure changes or if it
// comes back after things had started working again.
func (h *eventHub) reportMarshalError(err error) {
	msg := err.Error()
	h.errMu.Lock()
	repeat := h.lastMarshalErr == msg
	h.lastMarshalErr = msg
	h.errMu.Unlock()
	if repeat || h.log == nil {
		return
	}
	h.log.Error("", "the page view could not be encoded, so the page is still showing the last one it received: "+msg)
}

// tryAddClient registers ch as a client unless maxClients are already
// connected, in which case it reports false and registers nothing.
func (h *eventHub) tryAddClient(ch chan []byte) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= h.maxClients {
		return false
	}
	h.clients[ch] = struct{}{}
	return true
}

func (h *eventHub) removeClient(ch chan []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

// serveHTTP is GET /api/events. It sends the current view immediately,
// then a fresh one on every notify() and one activity entry per log write,
// until the request's context ends.
func (h *eventHub) serveHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "this server cannot stream events")
		return
	}

	ch := make(chan []byte, 8)
	if !h.tryAddClient(ch) {
		writeError(w, http.StatusServiceUnavailable, "too many event-stream clients are already connected")
		return
	}
	defer h.removeClient(ch)
	h.ensureStarted()

	entries, cancel := h.log.Subscribe()
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if msg, ok := h.openingMessage(); ok {
		_, _ = w.Write(msg)
		flusher.Flush()
	}

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case msg := <-ch:
			_, _ = w.Write(msg)
			flusher.Flush()
		case e := <-entries:
			data, err := json.Marshal(e)
			if err != nil {
				continue
			}
			_, _ = w.Write(formatSSE("activity", data))
			flusher.Flush()
		case <-ticker.C:
			_, _ = w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		case <-h.done:
			return
		case <-r.Context().Done():
			return
		}
	}
}

// formatSSE renders one server-sent event. data is JSON and therefore
// never contains a bare newline, so it always fits on a single data line.
func formatSSE(event string, data []byte) []byte {
	out := []byte("event: " + event + "\ndata: ")
	out = append(out, data...)
	return append(out, "\n\n"...)
}
