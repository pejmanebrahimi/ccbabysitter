// Package web is CC Babysitter's local HTTP API: the JSON routes the page
// calls, the server-sent-events stream that keeps it live, and a scripted
// demo engine that exercises the page without touching a real session. Everything here listens on 127.0.0.1
// only, and every request is checked by guard before a handler ever sees
// it, since any page open in the user's browser can address this server.
package web

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"sync"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/state"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
)

// Engine is everything the web layer needs from a supervisor. It is
// narrow on purpose so a scripted demo can stand in for the real thing;
// *supervise.Supervisor satisfies it as-is.
type Engine interface {
	View() supervise.View
	Babysit(id string, startAtLogin bool, via supervise.Via) supervise.Result
	Unbabysit(id string, via supervise.Via) supervise.Result
	Stop(id string, via supervise.Via) supervise.Result
	ResumeWatch(id string, via supervise.Via) supervise.Result
	OpenTerminal(id string) supervise.Result
	SetSettings(next state.Settings, via supervise.Via) supervise.Result
}

var _ Engine = (*supervise.Supervisor)(nil)

// Server answers the page's API and its event stream. It is safe to reuse
// across many connections and its handlers are safe for concurrent use.
type Server struct {
	engine  Engine
	log     *state.Log
	version string
	// key is the page's access key, which guard requires on every request.
	key string
	// launch holds the one-time tokens LaunchURL hands out.
	launch *launchTokens
	// session is this run's own secret, which a launch token is exchanged
	// for in place of the key: random, in memory only, never logged, and
	// gone when the server is, so a cookie holding it stops working on a
	// restart.
	session string

	hub *eventHub

	// settingsMu keeps two settings changes from reading the same current
	// settings and the second one undoing the first.
	settingsMu sync.Mutex

	// quitMu guards quit, which serve sets once before serving.
	quitMu sync.Mutex
	// quit stops the program serving this page, as Ctrl+C does. Nil means
	// quitting is not available, as in a server built only for a test.
	quit func()
}

// NewServer builds a Server around an engine, the activity log it should
// read the /api/activity feed and the SSE activity events from, and the
// page's access key, which every request must carry. The key must be one
// this program makes, 64 hex characters: a server that would compare
// requests against an empty or malformed key is a programming error, and
// it panics rather than serve without the key check.
func NewServer(e Engine, log *state.Log, version, key string) *Server {
	// Checked here and not left to the caller: an empty key can then never
	// be the one every request is compared against.
	if !state.ValidPageKey(key) {
		panic("web.NewServer: the page key must be 64 hex characters")
	}
	return &Server{
		engine:  e,
		log:     log,
		version: version,
		key:     key,
		launch:  newLaunchTokens(),
		session: randomSecret(),
		hub:     newEventHub(e, log),
	}
}

// Shutdown tells every open event stream to end. It is meant to be wired
// to the HTTP server's own shutdown, since a stream handler is otherwise
// holding a connection open with nothing to notice a shutdown through, and
// the shutdown would wait out its whole grace period for it. It returns
// straight away and is safe to call more than once.
func (s *Server) Shutdown() { s.hub.close() }

// Notify pushes a fresh view to every connected event-stream client. It is
// meant to be wired to a Deps.Notify callback, which the supervisor calls
// from its own loop goroutine, so this must stay cheap and never block;
// see events.go for how that is kept true.
func (s *Server) Notify() {
	s.hub.notify()
}

// Handler returns the complete HTTP handler for this server: every route,
// wrapped in the request guard, which also requires the page's key.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	return guard(s.key, s.session, s.launch, mux)
}

// LaunchURL is the address to open a browser on: pageURL with a new
// one-time launch token in place of the key. It is exchanged for a cookie
// holding this run's session on its first use within launchTokenLife, so
// the key itself is never on the command line of the program that opens
// the browser, and whoever uses the token first never gets the key.
func (s *Server) LaunchURL(pageURL string) string {
	return pageURL + "/?token=" + s.launch.issue()
}

// OnQuit sets what POST /api/quit calls once it has answered: serve passes
// the cancel function of its own context, so a quit shuts everything down
// the way Ctrl+C does. It must not wait for the shutdown, which waits for
// the quit request itself to finish.
func (s *Server) OnQuit(f func()) {
	s.quitMu.Lock()
	defer s.quitMu.Unlock()
	s.quit = f
}

func (s *Server) routes(mux *http.ServeMux) {
	registerStatic(mux, s.version)

	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/events", s.hub.serveHTTP)

	mux.HandleFunc("POST /api/sessions/{id}/babysit", s.withID(s.handleBabysit))
	mux.HandleFunc("POST /api/sessions/{id}/unbabysit", s.withID(s.handleUnbabysit))
	mux.HandleFunc("POST /api/sessions/{id}/stop", s.withID(s.handleStop))
	mux.HandleFunc("POST /api/sessions/{id}/resume-watch", s.withID(s.handleResumeWatch))
	mux.HandleFunc("POST /api/sessions/{id}/open-terminal", s.withID(s.handleOpenTerminal))

	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handlePutSettings)

	mux.HandleFunc("POST /api/quit", s.handleQuit)
	mux.HandleFunc("POST /api/launch", s.handleLaunch)

	mux.HandleFunc("GET /api/activity", s.handleActivity)
}

// withID validates the {id} path value before calling next, so a session
// id that could not possibly be real (and could otherwise reach a
// filesystem path via the engine) never gets there.
func (s *Server) withID(next func(w http.ResponseWriter, r *http.Request, id string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !claude.ValidID(id) {
			writeError(w, http.StatusNotFound, "no such session")
			return
		}
		next(w, r, id)
	}
}

// writeResult renders a Result as the page expects it: 200 when the action
// succeeded, 409 when it was refused, the JSON body identical either way.
func writeResult(w http.ResponseWriter, res supervise.Result) {
	status := http.StatusOK
	if !res.OK {
		status = http.StatusConflict
	}
	writeJSON(w, status, res)
}

// decodeOptionalJSON decodes a JSON body into v, treating a missing or
// empty body as leaving v at its zero value: several actions (babysit,
// unbabysit, resume-watch) have nothing they must be told. Unknown fields
// and malformed JSON are reported as a 400 with the page's error shape,
// and the caller must not use w or v further when this returns false.
func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if err == io.EOF {
			return true
		}
		writeError(w, http.StatusBadRequest, "malformed request body: "+err.Error())
		return false
	}
	return true
}

// decodeRequiredJSON is decodeOptionalJSON without the empty-body
// allowance, for the one route (PUT /api/settings) whose body is not
// optional.
func decodeRequiredJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body: "+err.Error())
		return false
	}
	return true
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	// The body is encoded before anything is written, so a view that cannot
	// be encoded answers with a plain error instead of a 200 carrying half
	// a document that the page would then fail to parse. When there is an
	// older view that did encode, that is served instead: a page opened at
	// a bad moment is better off a little behind than blank.
	data, err := json.Marshal(s.engine.View())
	if err != nil {
		s.hub.reportMarshalError(err)
		cached := s.hub.goodView()
		if cached == nil {
			writeError(w, http.StatusInternalServerError, "the page view could not be encoded: "+err.Error())
			return
		}
		data = cached
	} else {
		s.hub.rememberGoodView(data)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// viaOf reads where a request came from. Only the command line marks its
// requests; anything else, the page included, is the page.
func viaOf(r *http.Request) supervise.Via {
	if r.URL.Query().Get("via") == string(supervise.ViaCLI) {
		return supervise.ViaCLI
	}
	return supervise.ViaPage
}

func (s *Server) handleBabysit(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		StartAtLogin bool `json:"startAtLogin"`
	}
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	writeResult(w, s.engine.Babysit(id, req.StartAtLogin, viaOf(r)))
}

func (s *Server) handleUnbabysit(w http.ResponseWriter, r *http.Request, id string) {
	writeResult(w, s.engine.Unbabysit(id, viaOf(r)))
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request, id string) {
	writeResult(w, s.engine.Stop(id, viaOf(r)))
}

func (s *Server) handleResumeWatch(w http.ResponseWriter, r *http.Request, id string) {
	writeResult(w, s.engine.ResumeWatch(id, viaOf(r)))
}

func (s *Server) handleOpenTerminal(w http.ResponseWriter, r *http.Request, id string) {
	writeResult(w, s.engine.OpenTerminal(id))
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.View().Settings)
}

// handlePutSettings changes the settings the body names and keeps the
// rest as they are, so the page can save each setting the moment it is
// changed without sending, and possibly undoing, all the others.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	prev := s.engine.View().Settings
	next := prev
	if !decodeRequiredJSON(w, r, &next) {
		return
	}
	via := viaOf(r)
	res := s.engine.SetSettings(next, via)
	if res.OK && via == supervise.ViaCLI && s.log != nil {
		for _, change := range settingChanges(prev, next) {
			s.log.Info("", change+via.Suffix())
		}
	}
	writeResult(w, res)
}

// quitMessage is what a quit answers, for the page and the command line.
const quitMessage = "CC Babysitter is quitting. Babysat sessions keep running, but nothing brings them back until you run ccbabysitter again."

// handleQuit answers, notes the quit in Activity, and only then calls the
// quit hook, so the answer reaches the page or the command line before the
// server starts shutting down. The shutdown waits for this handler to
// return, so the answer is never cut off.
func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	s.quitMu.Lock()
	quit := s.quit
	s.quitMu.Unlock()
	if quit == nil {
		writeResult(w, supervise.Result{Message: "Quitting is not available here."})
		return
	}
	if s.log != nil {
		s.log.Info("", "asked to quit"+viaOf(r).Suffix())
	}
	writeResult(w, supervise.Result{OK: true, Message: quitMessage})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	quit()
}

// handleLaunch answers a one-time address to open the page with, for the
// program that opens a browser on it: the launcher. The key itself never
// goes on that program's command line.
func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		OK  bool   `json:"ok"`
		URL string `json:"url"`
	}{true, s.LaunchURL(s.engine.View().URL)})
}

// settingChanges names each setting, other than start at login, that
// differs between prev and next, the way the command line names it:
// "auto-babysit off", "theme dark". Start at login is left out because the
// engine logs that change itself, with the file it wrote or removed.
func settingChanges(prev, next state.Settings) []string {
	onOff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	var out []string
	if prev.AutoBabysit != next.AutoBabysit {
		out = append(out, "auto-babysit "+onOff(next.AutoBabysit))
	}
	if prev.AutoOpenBrowser != next.AutoOpenBrowser {
		out = append(out, "open-browser "+onOff(next.AutoOpenBrowser))
	}
	if prev.Theme != next.Theme {
		out = append(out, "theme "+next.Theme)
	}
	return out
}

// handleActivity answers GET /api/activity?session=&n=. n defaults to 200
// and is capped at 500; entries come back newest first, which is the order
// the page's feed reads top to bottom.
func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	n := 200
	if raw := r.URL.Query().Get("n"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			n = parsed
		}
	}
	if n > 500 {
		n = 500
	}
	entries := s.log.Recent(n, session)
	slices.Reverse(entries)
	if entries == nil {
		entries = []state.Entry{}
	}
	writeJSON(w, http.StatusOK, entries)
}
