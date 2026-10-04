package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// maxBodyBytes caps every request body this server reads. Nothing it
// accepts is ever larger, and a page that reached this address by accident
// or on purpose should not be able to make it read an unbounded amount of
// data.
const maxBodyBytes = 64 * 1024

// errorBody is the JSON shape of every error response: a plain sentence
// meant to be shown to whoever is looking, not a machine-readable code.
type errorBody struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Message: msg})
}

// The Content-Security-Policy every answer carries is cspDefault, then
// cspRest. Only the cross-site exchange page puts a script-src between
// them, allowing its one script by hash (see exchangeByPage). They are
// variables rather than constants for the same reason keyPage is: they
// are a header's syntax, not sentences.
var (
	cspDefault = "default-src 'self'; "
	cspRest    = "frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
)

// guard wraps every route this server has, including static files and the
// event stream. Any page open in the user's browser can address
// 127.0.0.1, and this server can terminate processes and start sessions,
// so the browser's own same-origin policy is not trusted on its own: every
// request is checked here before a handler ever sees it.
//
//   - The Host header must name loopback, so a DNS name an attacker
//     controls (rebound to point at this machine) cannot be used to reach
//     it: browsers send the Host the address bar shows, and only a
//     loopback address is ever typed into it by this program.
//   - Any request, GET and HEAD included, that carries an Origin header is
//     refused unless that origin is this same loopback origin. A same-
//     origin page never sends Origin on a plain navigation, a GET fetch, or
//     an EventSource connection, so this cockpit's own page is unaffected;
//     a cross-origin page's GET, and a sandboxed iframe or a file:// page
//     (which send "Origin: null"), are not. This is what stands in for
//     Fetch Metadata on a browser that does not send Sec-Fetch-Site at all.
//   - A GET or HEAD whose Sec-Fetch-Site says cross-site is refused too,
//     which stops another site's page from reading this server's state
//     through a no-cors image or script tag, or from opening the event
//     stream with EventSource, even on a browser new enough to send the
//     header but old enough that some other check here might miss it.
//     The one exception is a top-level navigation that carries the page's
//     right key, or a launch token, as its token, which is answered with
//     the key exchange and nothing else (see crossSiteExchange): that is
//     how the printed address arrives when it is followed from another
//     page or a local file rather than typed or opened directly.
//   - A request that is not GET or HEAD must also declare itself as JSON,
//     and any Sec-Fetch-Site header present must say the request came from
//     this same loopback origin. A plain form post or a cross-site fetch
//     cannot produce an application/json request body without JavaScript
//     reading the response, which the browser blocks for a cross-origin
//     target; requiring the header closes the one gap that leaves (a
//     "simple" request with a text/plain body).
//   - No Access-Control-* header is ever sent, so a browser never learns
//     it may hand a cross-origin response back to page script.
//   - Last, every request must carry the page's key. The checks above stop
//     other web pages, but any other account on the same machine can
//     reach 127.0.0.1 with a program of its own, and the port is no
//     secret; the key lives in a file only this account can read. It is
//     accepted the way Jupyter Server accepts its token: as the cookie
//     cookieName names, as an "Authorization: token KEY" (or "Bearer KEY")
//     header, which the command line sends, or as a "token" query
//     parameter on a GET or HEAD, which is exchanged for the cookie and a
//     move to the same address without it, so the key leaves the address
//     bar and the history: a 303 redirect, or for a cross-site navigation
//     a page whose script moves on with location.replace, which takes the
//     page's own entry, with the key, out of the history. The token may
//     also be a one-time launch token (see launchTokens), which is how a
//     browser is opened without the key on a command line; it works only
//     once, and the cookie it is exchanged for holds this run's session
//     rather than the key, unless the cookie already held the key, which
//     it keeps. The cookie is accepted holding either; the session is
//     never accepted in the header or as the token. A request without the
//     key, or with a wrong one, is answered 401 and never reaches a
//     handler. The query parameter is never accepted on any other method,
//     so the key alone in a link can never make the server act.
func guard(key, session string, launch *launchTokens, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", cspDefault+cspRest)

		if r.Method == http.MethodOptions {
			writeError(w, http.StatusForbidden, "this server does not answer cross-origin preflight requests")
			return
		}
		if !loopbackHost(r.Host) {
			writeError(w, http.StatusForbidden, "this server only answers requests addressed to a loopback host")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !loopbackOrigin(origin) {
			writeError(w, http.StatusForbidden, "cross-origin requests are not accepted")
			return
		}

		sfs := r.Header.Get("Sec-Fetch-Site")
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if sfs == "cross-site" {
				// The one cross-site request answered is the key exchange
				// for a top-level navigation that carries the right key or
				// a launch token, which is how the address arrives when it
				// is followed from another page or a local file. It is
				// answered with the cookie and a page that moves on, and
				// nothing else.
				if value := crossSiteExchange(r, key, session, launch); value != "" {
					exchangeByPage(w, r, value)
					return
				}
				writeError(w, http.StatusForbidden, "cross-site requests are not accepted")
				return
			}
		} else {
			if !jsonContentType(r.Header.Get("Content-Type")) {
				writeError(w, http.StatusForbidden, "requests must be sent as application/json")
				return
			}
			if sfs != "" && sfs != "same-origin" && sfs != "none" {
				writeError(w, http.StatusForbidden, "cross-site requests are not accepted")
				return
			}
		}

		// A right token is exchanged even on a request that carries the key
		// some other way, so the key never stays in the address bar.
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if value := tokenExchanges(r, key, session, launch); value != "" {
				exchangeToken(w, r, value)
				return
			}
		}
		if !hasKey(r, key, session) {
			refuseWithoutKey(w, r)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// KeyMessage is what a request without the page's key, or with a wrong
// one, is told. It names the two ways a person gets the page again, and
// never the key itself. A page that ccbabysitter opened holds only that
// run's session, so after a restart running ccbabysitter opens it again.
const KeyMessage = "This needs the page's key. Run ccbabysitter to open the page again, or open the address ccbabysitter status prints."

// keyCookieMaxAge is how long the browser keeps the key cookie, in
// seconds: a year, since the key itself is kept across restarts.
const keyCookieMaxAge = 365 * 24 * 60 * 60

// keyMatches compares a key a request carries with the page's key in
// constant time, so the time an answer takes says nothing about how much
// of a guess was right. An empty value never matches, whatever key is.
func keyMatches(got, key string) bool {
	if got == "" || key == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(key)) == 1
}

// cookieName is the name of the key cookie for the address r was sent to:
// "ccb_key_" and the port. Browsers keep cookies per host, not per port,
// so two copies on one machine, such as a real one and a demo, would
// otherwise overwrite each other's cookie. The port is read from the Host
// header, which is the address the browser shows; a Host without one
// falls back to the port the connection arrived on.
func cookieName(r *http.Request) string {
	port := ""
	if _, p, err := net.SplitHostPort(r.Host); err == nil {
		port = p
	} else if addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if _, p, err := net.SplitHostPort(addr.String()); err == nil {
			port = p
		}
	}
	return "ccb_key_" + port
}

// hasKey reports whether r carries the page's key, or this run's session,
// in the cookie for its port, or the key in an Authorization header of the
// token or Bearer scheme. The session is only ever a cookie: it is what a
// launch token was exchanged for, and is not a key to hand to anything.
func hasKey(r *http.Request, key, session string) bool {
	for _, c := range r.CookiesNamed(cookieName(r)) {
		if keyMatches(c.Value, key) || keyMatches(c.Value, session) {
			return true
		}
	}
	scheme, value, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if ok && (strings.EqualFold(scheme, "token") || strings.EqualFold(scheme, "Bearer")) {
		return keyMatches(value, key)
	}
	return false
}

// exchangeToken answers a GET or HEAD that carried the right key, or a
// launch token, as its token query parameter: it sets the cookie to value,
// the key or this run's session, and sends the browser with a 303 to the
// same path with the other query parameters kept and the token taken out.
// The browser follows a redirect without leaving the address it came from
// in the history, so the key stays in neither that nor the address bar.
// The path goes back as a path on this same page only: one that a browser
// could read as another host, starting with two slashes or a slash and a
// backslash, is sent to the page's root instead.
func exchangeToken(w http.ResponseWriter, r *http.Request, value string) {
	setKeyCookie(w, r, value)
	w.Header().Set("Location", cleanLocation(r))
	w.WriteHeader(http.StatusSeeOther)
}

// setKeyCookie sets the key cookie for the port r was sent to, holding
// value, the key or this run's session. The answer
// that sets it also says to send no Referer from it, so the address with
// the token, which the browser was just on, is never passed on.
func setKeyCookie(w http.ResponseWriter, r *http.Request, value string) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName(r),
		Value:    value,
		Path:     "/",
		MaxAge:   keyCookieMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// cleanLocation is where an exchange sends the browser: r's path with the
// other query parameters kept and the token taken out, or the page's root
// when the path is one a browser could read as another host.
func cleanLocation(r *http.Request) string {
	q := r.URL.Query()
	q.Del("token")
	loc := r.URL.EscapedPath()
	if !strings.HasPrefix(loc, "/") || strings.HasPrefix(loc, "//") || strings.HasPrefix(loc, "/\\") {
		loc = "/"
	}
	if rest := q.Encode(); rest != "" {
		loc += "?" + rest
	}
	return loc
}

// crossSiteExchange reports whether a cross-site GET is a top-level
// navigation that carries the right key, or a launch token, as its token.
// The address CC Babysitter prints arrives as cross-site when the person
// follows it from another page, a chat or a mail in a web app, or a local
// file, rather than typing it or having it opened directly. Nothing but
// such a navigation qualifies: not a frame, an image, a script or a
// fetch, and never without the key, so another site gains nothing from it
// it did not already have.
//
// It returns the value the cookie is to hold, or "" when r is not such a
// navigation.
func crossSiteExchange(r *http.Request, key, session string, launch *launchTokens) string {
	if r.Method != http.MethodGet ||
		r.Header.Get("Sec-Fetch-Mode") != "navigate" ||
		r.Header.Get("Sec-Fetch-Dest") != "document" {
		return ""
	}
	return tokenExchanges(r, key, session, launch)
}

// tokenExchanges reads r's token query parameter and returns the value the
// cookie is to hold in exchange for it: the key for the key, this run's
// session for a launch token that is still good, and "" for anything else.
// A launch token on a request whose cookie already holds the key keeps the
// key, so a launch never trades a key cookie for a session that ends with
// this run. A launch token is used up here, so this is asked only once
// everything else about the request already allows the exchange.
func tokenExchanges(r *http.Request, key, session string, launch *launchTokens) string {
	tok := r.URL.Query().Get("token")
	switch {
	case keyMatches(tok, key):
		return key
	case session != "" && launch.redeem(tok):
		if cookieHoldsKey(r, key) {
			return key
		}
		return session
	}
	return ""
}

// cookieHoldsKey reports whether r's key cookie for its port holds the
// page's key itself, not this run's session.
func cookieHoldsKey(r *http.Request, key string) bool {
	for _, c := range r.CookiesNamed(cookieName(r)) {
		if keyMatches(c.Value, key) {
			return true
		}
	}
	return false
}

// exchangeByPage is the exchange for a cross-site navigation. A browser
// does not send a SameSite=Strict cookie on the next step of a redirect
// chain that began on another site, so a 303 would arrive without it and
// be refused. Instead the answer is a page with the cookie that moves on
// to the clean address itself, which makes the next step a navigation
// from this same origin, carrying the cookie. The page holds no data, and
// the key is not in it.
//
// Unlike a redirect, this page is an entry in the history of its own, at
// the address with the key. Its one script moves on with
// location.replace, which puts the clean address in that entry's place,
// so the key does not stay behind. The page's Content-Security-Policy
// allows that script by its hash and no other; the meta refresh is the
// way on when scripts are off.
func exchangeByPage(w http.ResponseWriter, r *http.Request, value string) {
	setKeyCookie(w, r, value)
	clean := cleanLocation(r)
	script := exchangeScript(clean)
	sum := sha256.Sum256([]byte(script))
	w.Header().Set("Content-Security-Policy", cspDefault+
		"script-src 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'; "+cspRest)
	loc := html.EscapeString(clean)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "<!doctype html>\n<html lang=\"en\">\n<head><meta charset=\"utf-8\"><title>CC Babysitter</title>\n"+
		"<script>"+script+"</script>\n"+
		"<meta http-equiv=\"refresh\" content=\"0;url="+loc+"\"></head>\n"+
		"<body><p><a href=\""+loc+"\">Open CC Babysitter</a></p></body>\n</html>\n")
}

// exchangeScript is the exchange page's script, which moves on to loc in
// place of the page's own history entry. loc goes in as a JSON string,
// which json.Marshal writes with every <, > and & as a \u escape, along
// with the quote and the backslash, so nothing in a path can end the
// string or the script element early.
func exchangeScript(loc string) string {
	b, _ := json.Marshal(loc)
	return "location.replace(" + string(b) + ")"
}

// keyPage is what a browser opening the page without the key is shown: a
// plain page with the sentence and nothing else, no data and no scripts.
// The sentence needs no escaping: its only special character is an
// apostrophe, which is plain text in HTML outside an attribute.
// It is a variable rather than a constant because it is markup, not a
// sentence, and the test that holds string constants to the sentence rules
// is not meant for it.
var keyPage = "<!doctype html>\n<html lang=\"en\">\n<head><meta charset=\"utf-8\"><title>CC Babysitter</title></head>\n<body><p>" + KeyMessage + "</p></body>\n</html>\n"

// refuseWithoutKey answers a request that does not carry the page's key:
// 401 with the plain keyPage for the page itself, and the usual JSON error
// for everything else, the API, the event stream and static files alike.
func refuseWithoutKey(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, keyPage)
		return
	}
	writeError(w, http.StatusUnauthorized, KeyMessage)
}

// loopbackHost reports whether a Host or Origin host (with or without a
// port, bracketed or not) names loopback: the literal name "localhost"
// (case-insensitive, an optional trailing dot ignored), or any literal IP
// address for which net.IP.IsLoopback reports true. That covers 127.0.0.1
// and every other address in 127.0.0.0/8, ::1, and the equivalent
// unabbreviated IPv6 form some clients send, [0:0:0:0:0:0:0:1]. Anything
// that is not itself a loopback name or a loopback IP is rejected, even
// when it merely contains one as a substring, such as
// 127.0.0.1.evil.example.
func loopbackHost(h string) bool {
	host := h
	if hostPart, _, err := net.SplitHostPort(h); err == nil {
		host = hostPart
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	return name == "localhost"
}

// loopbackOrigin reports whether an Origin header is exactly "http://"
// followed by a loopback host, any port, and nothing else: no path, query
// or fragment. Browsers never put one of those on an Origin header, so
// requiring their absence only rejects a value that was hand-crafted to
// look like a loopback origin while naming something else.
func loopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return false
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return loopbackHost(u.Host)
}

// jsonContentType reports whether a Content-Type header names
// application/json, ignoring parameters such as charset.
func jsonContentType(ct string) bool {
	if ct == "" {
		return false
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	return mt == "application/json"
}
