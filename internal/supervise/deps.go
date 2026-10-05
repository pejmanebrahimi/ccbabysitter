package supervise

import (
	"os"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/power"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// Deps bundles everything a reconcile pass and the actions need, so every
// side effect can be replaced with a fake in tests.
type Deps struct {
	Store  *state.Store
	Log    *state.Log
	Obs    *observe.Observer
	Procs  procs.Procs
	Runner claude.Runner
	Power  power.KeepAwake

	Env func() hosts.Env

	ProjectsDir string
	Now         func() time.Time

	// Home is the account's home folder, where a background session cannot
	// be started. ClaudeConfig is the CLI's own settings file, read to tell
	// whether it trusts a folder. Both default to the real ones.
	Home         string
	ClaudeConfig string

	// SSHTarget reports user@host for this machine, which a person on
	// another one would ssh to. It answers only on a machine with no
	// display, and its answer turns the attach command into one that can
	// be run from elsewhere. The address can change while the program
	// runs, so it is asked each time rather than read once; it may be
	// called from the loop's goroutine and must be quick. It may be nil.
	SSHTarget func() string

	// Notify is called after each snapshot the reconcile loop publishes and
	// after each action it runs, so a caller such as the web layer can push
	// a fresh view out. It may be nil.
	//
	// Reading the view from inside it is always safe. Calling an action
	// from inside it is never safe: once the loop is running that would
	// wait for the loop that is calling it, and before the loop has started
	// an action runs on the caller's own goroutine with the supervisor's
	// own lock held, so Notify can be invoked part way through one.
	// Whatever it does should be quick, since the caller waits for it to
	// return. It is not called while the supervisor is being built.
	Notify func()
	// Autostart enables or disables launching this program at login and
	// reports the path it wrote or removed. It may be nil on a platform
	// that offers no such thing.
	Autostart func(enable bool) (path string, err error)
	// AutostartInstalled reports whether the login item Autostart writes
	// is there now, so a saved setting can follow one added or removed by
	// hand. It only ever looks, and it may be nil where there is no such
	// thing.
	AutostartInstalled func() (bool, error)

	// OpenTerminal opens the system's own terminal on `claude attach` for
	// the background copy with the given short id, and names what it
	// opened. It never runs the CLI itself, and it may be nil where there
	// is no way to open one.
	OpenTerminal func(short string) (opened string, err error)
	// TerminalName says what OpenTerminal would open here, so the page's
	// button can name it. It may be nil where there is no way to open one.
	TerminalName func() string

	// URL is the address this program is serving its page on, passed
	// straight through to the view for display. The layer that owns the
	// listener fills it in, since only that layer knows the port.
	URL string

	Backoff time.Duration
	// AutostartNote is what Activity says when the first look at the login
	// item finds one the settings do not know about because ccbabysitter
	// wrote it itself, such as replacing an earlier version's. Empty means
	// there is no such note, and only the first look uses it.
	AutostartNote string
	// StartupGrace is how long after start, on a machine with a display, no
	// fallback runs, so apps that restore their own sessions at login go
	// first. Zero means the default of 90 seconds, and a negative value
	// means no grace at all.
	StartupGrace time.Duration
	// SettleTimeout is how long a resume that did not ask for Remote Control
	// looks for the new background copy before reporting its state.
	SettleTimeout time.Duration
	// RCSettleTimeout is how long a resume that asked for Remote Control
	// waits for the bridge before saying it is not there yet. Asking for it
	// and getting it are seconds apart, and a card that already shows it on
	// makes a message that says otherwise look like a bug in this program.
	RCSettleTimeout time.Duration

	// readStats reads one session's running token counts out of its
	// transcript, and forgetStats drops what was remembered about a session
	// that is gone. Both are only ever called from the stats worker
	// goroutine. They are fields so a test can stand in for the file
	// reading and make it take as long as it likes.
	readStats   func(id, path string) (claude.Stats, error)
	forgetStats func(id string)
	// listPast finds the conversations for the Not running list. It is
	// only ever called from the stats worker goroutine, and it is a field
	// so a test can make it as slow as it likes.
	listPast func(since time.Time, skip map[string]bool) []claude.PastSession
	// readTrust reads the CLI's trust flags out of ClaudeConfig. It is
	// only ever called from the stats worker goroutine, since the CLI
	// rewrites that file often and it can be large, and it is a field so a
	// test can make it as slow as it likes.
	readTrust func(now time.Time) claude.Trust
}

// transcriptStats keeps one reader per session, so each read only covers
// the bytes appended since the previous one. It has no lock: it belongs to
// the stats worker goroutine and nothing else ever reaches it.
type transcriptStats struct {
	readers map[string]*claude.StatsReader
}

func (t *transcriptStats) read(id, path string) (claude.Stats, error) {
	r := t.readers[id]
	if r == nil {
		r = &claude.StatsReader{}
		t.readers[id] = r
	}
	return r.Update(path)
}

func (t *transcriptStats) forget(id string) { delete(t.readers, id) }

// Via says where a request to act came from. The Activity panel names the
// command line, so a person can always see what an agent or a script did;
// the page and this program's own decisions read as before.
type Via string

const (
	ViaPage Via = ""
	ViaCLI  Via = "cli"
)

// Suffix is what an Activity entry ends with for a request from v.
func (v Via) Suffix() string {
	if v == ViaCLI {
		return ", from the command line"
	}
	return ""
}

// Result is the outcome of an action taken on a watch, meant to be shown to
// the user exactly as given.
type Result struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	ShortID string `json:"shortId,omitempty"`
	// AlreadyLiveIn names the host a resume found the session running in
	// already, and is empty for every other result. A caller keeping a
	// promise reads it to tell "there was nothing to do" apart from "this
	// did not work", which are recorded quite differently.
	AlreadyLiveIn claude.Host `json:"alreadyLiveIn,omitempty"`
}

// defaults fills in every duration and the clock that was left at its zero
// value.
func (d *Deps) defaults() {
	if d.Backoff == 0 {
		d.Backoff = 10 * time.Second
	}
	if d.StartupGrace == 0 {
		d.StartupGrace = 90 * time.Second
	}
	if d.SettleTimeout == 0 {
		d.SettleTimeout = 3 * time.Second
	}
	if d.RCSettleTimeout == 0 {
		d.RCSettleTimeout = 10 * time.Second
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Home == "" {
		d.Home, _ = os.UserHomeDir()
	}
	if d.ClaudeConfig == "" {
		d.ClaudeConfig = claude.ConfigFile()
	}
	if d.readStats == nil {
		t := &transcriptStats{readers: map[string]*claude.StatsReader{}}
		d.readStats = t.read
		d.forgetStats = t.forget
	}
	if d.forgetStats == nil {
		d.forgetStats = func(string) {}
	}
	if d.listPast == nil {
		dir := d.ProjectsDir
		d.listPast = func(since time.Time, skip map[string]bool) []claude.PastSession {
			return claude.ListPast(dir, since, pastMax, skip)
		}
	}
	if d.readTrust == nil {
		tf := &claude.TrustFile{Path: d.ClaudeConfig}
		d.readTrust = tf.Read
	}
}
