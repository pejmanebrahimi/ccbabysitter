// Package client talks to the copy of CC Babysitter that is running now,
// through the same local API its page uses. The command line's control
// commands are built on it, so the page and the command line always drive
// the same engine.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/state"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
)

// ErrNotRunning means no copy of CC Babysitter answers: no address was
// saved, nothing listens there, or what answers is some other program.
var ErrNotRunning = errors.New("CC Babysitter is not running")

// ErrNoKey means CC Babysitter answered but refused the request for want
// of the page's key: there was none to send, or the one sent is not the
// running copy's. The sentence is the one the page itself answers with.
var ErrNoKey = errors.New("This needs the page's key. Open the address CC Babysitter printed, or run ccbabysitter status.")

// ErrNoAnswer means CC Babysitter was reached but gave no answer in time,
// or dropped the connection after the request was sent. An action may
// still finish.
var ErrNoAnswer = errors.New("CC Babysitter did not answer in time")

// Error is a refusal that is not an engine result, such as a malformed
// request, carrying the sentence the server wrote.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

// How long a request may take. A view is read from memory, so it answers at
// once. An action queues on the supervisor, which runs claude commands for up
// to a minute each, so it gets longer.
const (
	viewTimeout   = 10 * time.Second
	actionTimeout = 90 * time.Second
)

// Client is one running copy's address, its page's key, and an HTTP
// client to reach it.
type Client struct {
	base                       string
	key                        string
	hc                         *http.Client
	viewTimeout, actionTimeout time.Duration
}

// New finds the running copy: raw when it is given, else the address the
// running copy saved in stateDir. raw may carry the page's key the way the
// start banner prints it, http://127.0.0.1:PORT/?token=KEY, and then that
// key is the one sent; otherwise the key the running copy saved in
// stateDir is, but only to that copy's own address.
//
// The saved address, and the folder's key, are used only while a live
// copy holds stateDir's single-instance lock. An address left behind by a
// copy that crashed may name a port that another account's server listens
// on now, and the folder's key must never be sent there. The caller wires
// the real process check with state.SetPIDChecker. An address the person
// gives is their choice, and is used as it is, but the folder's key goes
// with it only when it is the address the live copy saved: any other port
// may be another account's server, or another copy such as a demo, and
// gets no key at all. A real copy answers that with its no-key refusal.
func New(stateDir, raw string) (*Client, error) {
	saved := state.PageURL(stateDir)
	live := saved != "" && state.IsHeld(stateDir)
	base, key := raw, ""
	if raw == "" {
		if !live {
			return nil, ErrNotRunning
		}
		base = saved
	} else {
		var ok bool
		base, key, ok = splitKeyedURL(raw)
		if !ok {
			return nil, fmt.Errorf("%s is not a CC Babysitter page address. Use http://127.0.0.1:PORT or the address CC Babysitter printed", withoutQuery(raw))
		}
	}
	if key == "" && live && base == saved {
		key = state.ReadPageKey(stateDir)
	}
	hc := &http.Client{
		// A redirect is never followed: it is not CC Babysitter's answer, and a
		// POST must not be replayed somewhere else.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Client{base: base, key: key, hc: hc, viewTimeout: viewTimeout, actionTimeout: actionTimeout}, nil
}

// splitKeyedURL reads a page address given on the command line: a plain
// one, or one with a token query parameter that holds a key and nothing
// else after the address. It returns the plain address and the key, empty
// when there is none, and false when raw is neither.
func splitKeyedURL(raw string) (base, key string, ok bool) {
	if state.ValidPageURL(raw) {
		return raw, "", true
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || (u.Path != "" && u.Path != "/") || u.Fragment != "" || u.RawFragment != "" {
		return "", "", false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) != 1 || len(q["token"]) != 1 {
		return "", "", false
	}
	key = q.Get("token")
	if !state.ValidPageKey(key) {
		return "", "", false
	}
	base = u.Scheme + "://" + u.Host
	if !state.ValidPageURL(base) {
		return "", "", false
	}
	return base, key, true
}

// withoutQuery is raw with anything from its query on left out, so an
// error message never repeats a key that was given in it.
func withoutQuery(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		return raw[:i] + "?..."
	}
	return raw
}

// URL is the page address this client talks to, without the key.
func (c *Client) URL() string { return c.base }

// KeyedURL is the page address with the key, the one a person opens, or
// the plain address when there is no key to give.
func (c *Client) KeyedURL() string { return state.KeyedURL(c.base, c.key) }

// sendFailure is what a failed request means: nothing listens there when the
// connection could not be made, and CC Babysitter did not answer when it was
// made and then no answer came.
func sendFailure(err error) error {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return ErrNotRunning
	}
	return ErrNoAnswer
}

// send makes one request, giving it timeout, and returns the status and body.
func (c *Client) send(ctx context.Context, timeout time.Duration, method, path string, body any) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.key != "" {
		req.Header.Set("Authorization", "token "+c.key)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, sendFailure(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return 0, nil, ErrNoAnswer
	}
	if resp.StatusCode == http.StatusUnauthorized && isErrorBody(data) {
		return 0, nil, ErrNoKey
	}
	return resp.StatusCode, data, nil
}

// isErrorBody reports whether data is CC Babysitter's JSON error shape
// with a sentence in it, which is how a refusal for want of the key is
// told apart from some other program asking for a login.
func isErrorBody(data []byte) bool {
	var e struct {
		OK      *bool  `json:"ok"`
		Message string `json:"message"`
	}
	return json.Unmarshal(data, &e) == nil && e.OK != nil && !*e.OK && e.Message != ""
}

// View is everything the page shows now. Only CC Babysitter's own answer
// counts: anything else at this address means it is not running.
func (c *Client) View(ctx context.Context) (supervise.View, error) {
	status, data, err := c.send(ctx, c.viewTimeout, http.MethodGet, "/api/state", nil)
	if err != nil {
		return supervise.View{}, err
	}
	var v supervise.View
	if status != http.StatusOK || json.Unmarshal(data, &v) != nil || v.Version == "" {
		return supervise.View{}, ErrNotRunning
	}
	return v, nil
}

// result reads an action's answer: a Result on 200 and 409, an Error when
// the server refused the request itself.
func result(status int, data []byte) (supervise.Result, error) {
	var r supervise.Result
	if status == http.StatusOK || status == http.StatusConflict {
		if json.Unmarshal(data, &r) != nil {
			return supervise.Result{}, ErrNotRunning
		}
		return r, nil
	}
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &e) != nil || e.Message == "" {
		return supervise.Result{}, ErrNotRunning
	}
	return supervise.Result{}, &Error{Status: status, Message: e.Message}
}

func (c *Client) action(ctx context.Context, id, verb string, body any) (supervise.Result, error) {
	if body == nil {
		body = struct{}{}
	}
	status, data, err := c.send(ctx, c.actionTimeout, http.MethodPost, "/api/sessions/"+url.PathEscape(id)+"/"+verb+"?via=cli", body)
	if err != nil {
		return supervise.Result{}, err
	}
	return result(status, data)
}

// Babysit is the page's Babysit button, with its start at login box.
func (c *Client) Babysit(ctx context.Context, id string, startAtLogin bool) (supervise.Result, error) {
	return c.action(ctx, id, "babysit", map[string]bool{"startAtLogin": startAtLogin})
}

// Unbabysit is the page's Unbabysit button.
func (c *Client) Unbabysit(ctx context.Context, id string) (supervise.Result, error) {
	return c.action(ctx, id, "unbabysit", nil)
}

// Retry is the page's Try again button on a stuck session.
func (c *Client) Retry(ctx context.Context, id string) (supervise.Result, error) {
	return c.action(ctx, id, "resume-watch", nil)
}

// Stop stops a session's background copy, as the page does once its
// dialog is confirmed.
func (c *Client) Stop(ctx context.Context, id string) (supervise.Result, error) {
	return c.action(ctx, id, "stop", nil)
}

// olderCopyQuit is quit's refusal when the running copy is from before
// quit existed, which answers the route with a plain 404 or 405.
const olderCopyQuit = "The running CC Babysitter is an older version that cannot quit this way. Quit it with Ctrl+C in its window. If it was started at login, stop it on Linux with: systemctl --user stop ccbabysitter, on macOS with: launchctl bootout gui/$(id -u)/com.ccbabysitter, or on Windows by ending ccbabysitter.exe in Task Manager."

// Quit asks the running copy to quit. Babysat sessions keep running. A
// copy from before quit existed has answered, so it is running: it gets a
// refusal that says how to quit it, not "not running".
func (c *Client) Quit(ctx context.Context) (supervise.Result, error) {
	status, data, err := c.send(ctx, c.actionTimeout, http.MethodPost, "/api/quit?via=cli", struct{}{})
	if err != nil {
		return supervise.Result{}, err
	}
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		return supervise.Result{Message: olderCopyQuit}, nil
	}
	return result(status, data)
}

// Activity is the Activity panel, newest first, at most n entries.
func (c *Client) Activity(ctx context.Context, n int) ([]state.Entry, error) {
	status, data, err := c.send(ctx, c.viewTimeout, http.MethodGet, fmt.Sprintf("/api/activity?n=%d", n), nil)
	if err != nil {
		return nil, err
	}
	var out []state.Entry
	if status != http.StatusOK || json.Unmarshal(data, &out) != nil {
		return nil, ErrNotRunning
	}
	return out, nil
}

// SetSettings changes the settings patch names, by their JSON names, and
// leaves the others as they are.
func (c *Client) SetSettings(ctx context.Context, patch map[string]any) (supervise.Result, error) {
	status, data, err := c.send(ctx, c.actionTimeout, http.MethodPut, "/api/settings?via=cli", patch)
	if err != nil {
		return supervise.Result{}, err
	}
	return result(status, data)
}
