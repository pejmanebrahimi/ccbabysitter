package web

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/state"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
)

func decodeBody(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

// doRequest builds a request against the test server, carrying testKey
// unless the caller sets an Authorization header or a cookie, and letting
// the caller override any header (including Host, which req.Host controls
// rather than req.Header).
func doRequest(t *testing.T, method, url string, headers map[string]string, body string) *http.Response {
	t.Helper()
	return doRequestWith(t, keyed, method, url, headers, body)
}

// doPlainRequest is doRequest without the key: only what the caller sets
// is sent.
func doPlainRequest(t *testing.T, method, url string, headers map[string]string, body string) *http.Response {
	t.Helper()
	return doRequestWith(t, &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, method, url, headers, body)
}

func doRequestWith(t *testing.T, c *http.Client, method, url string, headers map[string]string, body string) *http.Response {
	t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func assertForbiddenNoCall(t *testing.T, e *fakeEngine, resp *http.Response) {
	t.Helper()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %d", resp.StatusCode)
	}
	var eb errorBody
	decodeBody(t, resp, &eb)
	if eb.OK || eb.Message == "" {
		t.Fatalf("403 body must be the error shape with a sentence: %+v", eb)
	}
	if len(e.calls) != 0 {
		t.Fatal("engine must not be called:", e.calls)
	}
}

func TestGuardRejectsForeignHost(t *testing.T) {
	ts, e, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Host": "evil.example"}, "")
	assertForbiddenNoCall(t, e, resp)
}

func TestGuardRejectsHostThatMerelyContainsLoopback(t *testing.T) {
	ts, e, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Host": "127.0.0.1.evil.example"}, "")
	assertForbiddenNoCall(t, e, resp)
}

func TestGuardRejectsForeignOrigin(t *testing.T) {
	ts, e, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodPost, ts.URL+"/api/sessions/"+testID1+"/babysit", map[string]string{
		"Content-Type": "application/json", "Origin": "http://evil.example",
	}, "")
	assertForbiddenNoCall(t, e, resp)
}

func TestGuardRejectsNonJSONContentType(t *testing.T) {
	ts, e, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodPost, ts.URL+"/api/sessions/"+testID1+"/babysit", map[string]string{
		"Content-Type": "text/plain",
	}, "")
	assertForbiddenNoCall(t, e, resp)
}

func TestGuardRejectsMissingContentType(t *testing.T) {
	ts, e, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodPost, ts.URL+"/api/sessions/"+testID1+"/babysit", map[string]string{}, "")
	assertForbiddenNoCall(t, e, resp)
}

func TestGuardRejectsCrossSitePost(t *testing.T) {
	ts, e, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodPost, ts.URL+"/api/sessions/"+testID1+"/babysit", map[string]string{
		"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site",
	}, "")
	assertForbiddenNoCall(t, e, resp)
}

func TestGuardRejectsCrossSiteGet(t *testing.T) {
	ts, e, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Sec-Fetch-Site": "cross-site"}, "")
	assertForbiddenNoCall(t, e, resp)
}

func TestGuardRejectsForeignOriginOnGetState(t *testing.T) {
	ts, e, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Origin": "http://evil.example"}, "")
	assertForbiddenNoCall(t, e, resp)
}

func TestGuardRejectsForeignOriginOnEvents(t *testing.T) {
	ts, e, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/events", map[string]string{"Origin": "http://evil.example"}, "")
	assertForbiddenNoCall(t, e, resp)
}

func TestGuardRejectsNullOriginOnGet(t *testing.T) {
	ts, e, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Origin": "null"}, "")
	assertForbiddenNoCall(t, e, resp)
}

func TestGuardAcceptsLoopbackOriginOnGet(t *testing.T) {
	ts, _, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Origin": "http://localhost:47391"}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestGuardRejectsLoopbackOriginWithAPath(t *testing.T) {
	ts, e, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodPost, ts.URL+"/api/sessions/"+testID1+"/babysit", map[string]string{
		"Content-Type": "application/json", "Origin": "http://127.0.0.1:47391/x",
	}, "")
	assertForbiddenNoCall(t, e, resp)
}

func TestGuardAcceptsExpandedIPv6LoopbackHost(t *testing.T) {
	ts, _, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Host": "[0:0:0:0:0:0:0:1]:47391"}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestGuardAcceptsLocalhostWithTrailingDot(t *testing.T) {
	ts, _, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Host": "localhost."}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestGuardRejectsOptionsPreflight(t *testing.T) {
	ts, e, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodOptions, ts.URL+"/api/sessions/"+testID1+"/babysit", map[string]string{
		"Content-Type": "application/json",
	}, "")
	assertForbiddenNoCall(t, e, resp)
}

func TestGuardRejectsInvalidIDWithoutCallingEngine(t *testing.T) {
	ts, e, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodPost, ts.URL+"/api/sessions/not-a-real-id/babysit", map[string]string{
		"Content-Type": "application/json",
	}, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
	if len(e.calls) != 0 {
		t.Fatal("engine must not be called:", e.calls)
	}
}

func TestGuardEnforcesBodySizeLimit(t *testing.T) {
	ts, e, _ := newTS(t)
	defer ts.Close()
	big := `{"theme":"` + strings.Repeat("x", 70*1024) + `"}`
	resp := doRequest(t, http.MethodPut, ts.URL+"/api/settings", map[string]string{
		"Content-Type": "application/json",
	}, big)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for an oversized body, got %d", resp.StatusCode)
	}
	if len(e.calls) != 0 {
		t.Fatal("engine must not be called with an oversized body:", e.calls)
	}
}

func TestGuardAcceptsLoopbackHostWithPort(t *testing.T) {
	ts, _, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Host": "localhost:5000"}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestGuardAcceptsBracketedIPv6HostWithPort(t *testing.T) {
	ts, _, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Host": "[::1]:47391"}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestGuardAcceptsLoopbackOrigin(t *testing.T) {
	ts, e, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodPost, ts.URL+"/api/sessions/"+testID1+"/babysit", map[string]string{
		"Content-Type": "application/json", "Origin": "http://127.0.0.1:47391",
	}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if len(e.calls) != 1 {
		t.Fatal("engine must be called for a legitimate loopback request:", e.calls)
	}
}

func TestGuardNeverSendsAccessControlHeaders(t *testing.T) {
	ts, _, _ := newTS(t)
	defer ts.Close()
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Origin": "http://evil.example"}, "")
	for k := range resp.Header {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Fatal("no Access-Control-* header must ever be sent:", k)
		}
	}
}

// keyRefusal is the sentence every request without the page's key is
// answered with.
const keyRefusal = "This needs the page's key."

// portOfTS is the port a test server listens on, as a string.
func portOfTS(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}

func TestNoKeyIsRefused(t *testing.T) {
	ts, _, _ := newTS(t)
	for _, path := range []string{"/", "/api/state", "/api/events", "/app.js"} {
		resp, err := http.Get(ts.URL + path) // no key
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), "This needs the page's key.") {
			t.Errorf("%s: %d %q", path, resp.StatusCode, body)
		}
		if path == "/" && strings.Contains(string(body), "<script") {
			t.Error("the refusal page must not carry scripts")
		}
	}
}

// The refusal carries the same security headers as every other answer,
// and the page's refusal is plain HTML while the API's is the JSON error.
func TestNoKeyRefusalShapes(t *testing.T) {
	ts, e, _ := newTS(t)
	resp := doPlainRequest(t, http.MethodGet, ts.URL+"/", nil, "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("page refusal Content-Type %q", ct)
	}
	if !strings.Contains(string(body), "<title>CC Babysitter</title>") {
		t.Errorf("page refusal %q", body)
	}
	if resp.Header.Get("Content-Security-Policy") == "" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("headers %v", resp.Header)
	}

	resp = doPlainRequest(t, http.MethodPost, ts.URL+"/api/sessions/"+testID1+"/babysit", map[string]string{"Content-Type": "application/json"}, "{}")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST without a key: %d", resp.StatusCode)
	}
	var eb errorBody
	decodeBody(t, resp, &eb)
	if eb.OK || eb.Message != KeyMessage {
		t.Fatalf("API refusal %+v", eb)
	}
	if len(e.calls) != 0 {
		t.Fatal("engine must not be called without the key:", e.calls)
	}
}

func TestHeaderKeyIsAccepted(t *testing.T) {
	ts, _, _ := newTS(t)
	for _, h := range []string{"token " + testKey, "Bearer " + testKey} {
		resp := doPlainRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Authorization": h}, "")
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Authorization %.6s...: %d, want 200", h, resp.StatusCode)
		}
	}
	other := strings.Repeat("cd", 32)
	for _, h := range []string{"token " + other, "Bearer " + other, testKey, "Basic " + testKey, "token " + testKey[:63]} {
		resp := doPlainRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Authorization": h}, "")
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("Authorization %.6s...: %d, want 401", h, resp.StatusCode)
		}
	}
}

func TestTokenExchangeKeepsOtherParams(t *testing.T) {
	ts, _, _ := newTS(t)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(ts.URL + "/api/activity?n=5&token=" + testKey)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || loc != "/api/activity?n=5" {
		t.Fatalf("%d %q", resp.StatusCode, loc)
	}
	ck := resp.Header.Get("Set-Cookie")
	for _, want := range []string{"ccb_key_", "=" + testKey, "HttpOnly", "SameSite=Strict", "Path=/", "Max-Age="} {
		if !strings.Contains(ck, want) {
			t.Errorf("cookie %q lacks %q", ck, want)
		}
	}
}

// The exchange only ever sends the browser back to a path on this same
// page, never to another host through a path that starts with two
// slashes.
func TestTokenExchangeStaysOnThePage(t *testing.T) {
	ts, _, _ := newTS(t)
	for _, path := range []string{"//evil.example/x", "/\\evil.example/x", "/%2F%2Fevil.example", "/%5Cevil.example", "/%2f%2fevil.example", "/%5cevil.example"} {
		resp := doPlainRequest(t, http.MethodGet, ts.URL+path+"?token="+testKey, nil, "")
		resp.Body.Close()
		loc := resp.Header.Get("Location")
		if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/") || strings.HasPrefix(loc, "//") || strings.HasPrefix(loc, "/\\") {
			t.Errorf("%s: %d %q", path, resp.StatusCode, loc)
		}
	}
}

func TestTokenNotAcceptedOnPost(t *testing.T) {
	ts, e, _ := newTS(t)
	resp := doPlainRequest(t, http.MethodPost, ts.URL+"/api/sessions/"+testID1+"/babysit?token="+testKey,
		map[string]string{"Content-Type": "application/json"}, "{}")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), keyRefusal) {
		t.Fatalf("POST with ?token=: %d %q", resp.StatusCode, body)
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Fatal("a POST must never be given the cookie")
	}
	if len(e.calls) != 0 {
		t.Fatal("engine must not be called:", e.calls)
	}
}

func TestWrongCookieIsRefusedAndTokenFixesIt(t *testing.T) {
	ts, _, _ := newTS(t)
	port := portOfTS(t, ts)
	other := strings.Repeat("cd", 32)

	resp := doPlainRequest(t, http.MethodGet, ts.URL+"/", map[string]string{"Cookie": "ccb_key_" + port + "=" + other}, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong cookie: %d, want 401", resp.StatusCode)
	}

	resp = doPlainRequest(t, http.MethodGet, ts.URL+"/?token="+testKey, map[string]string{"Cookie": "ccb_key_" + port + "=" + other}, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("exchange: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	var got *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "ccb_key_"+port {
			got = c
		}
	}
	if got == nil || got.Value != testKey {
		t.Fatalf("the exchange did not set the right cookie: %v", resp.Cookies())
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(ts.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "ccb_key_" + port, Value: other, Path: "/"}})
	browser := &http.Client{Jar: jar}
	resp, err = browser.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("browser with a wrong cookie: %d, want 401", resp.StatusCode)
	}
	resp, err = browser.Get(ts.URL + "/?token=" + testKey)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("browser after the exchange: %d, want 200", resp.StatusCode)
	}
	if resp.Request.URL.RawQuery != "" {
		t.Fatalf("the key stayed in the address: %q", resp.Request.URL)
	}
	resp, err = browser.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the cookie does not open the API: %d", resp.StatusCode)
	}
}

func TestCookieNameCarriesThePort(t *testing.T) {
	ts, _, _ := newTS(t)
	port := portOfTS(t, ts)
	resp := doPlainRequest(t, http.MethodGet, ts.URL+"/?token="+testKey, nil, "")
	resp.Body.Close()
	cookies := resp.Cookies()
	if len(cookies) != 1 || cookies[0].Name != "ccb_key_"+port {
		t.Fatalf("cookies %v, want one named ccb_key_%s", cookies, port)
	}

	n, _ := strconv.Atoi(port)
	otherPort := strconv.Itoa(n + 1)
	resp = doPlainRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Cookie": "ccb_key_" + otherPort + "=" + testKey}, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a cookie for another port: %d, want 401", resp.StatusCode)
	}
	resp = doPlainRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Cookie": "ccb_key_" + port + "=" + testKey}, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the cookie for this port: %d, want 200", resp.StatusCode)
	}
}

func TestEmptyKeyNeverMatches(t *testing.T) {
	for _, bad := range []string{"", "short", strings.Repeat("z", 64)} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("NewServer accepted the key %q", bad)
				}
			}()
			NewServer(&fakeEngine{}, nil, "0.1.0", bad)
		}()
	}

	ts, _, _ := newTS(t)
	port := portOfTS(t, ts)
	for _, h := range []map[string]string{
		{"Authorization": "token "},
		{"Authorization": "token"},
		{"Authorization": "Bearer "},
		{"Cookie": "ccb_key_" + port + "="},
	} {
		resp := doPlainRequest(t, http.MethodGet, ts.URL+"/api/state", h, "")
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%v: %d, want 401", h, resp.StatusCode)
		}
	}
	resp := doPlainRequest(t, http.MethodGet, ts.URL+"/?token=", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an empty ?token=: %d, want 401", resp.StatusCode)
	}

	// The guard itself never matches an empty key, whatever reaches it.
	h := guard("", "", newLaunchTokens(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("an empty key let %s %s through", r.Method, r.URL)
	}))
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "http://127.0.0.1:47391/api/state", nil),
		httptest.NewRequest(http.MethodGet, "http://127.0.0.1:47391/api/state?token=", nil),
	} {
		req.Header.Set("Authorization", "token ")
		req.AddCookie(&http.Cookie{Name: "ccb_key_47391", Value: ""})
		req.AddCookie(&http.Cookie{Name: "ccb_key_", Value: ""})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", req.URL, rec.Code)
		}
	}
}

// The key is checked after every other guard check: a request from
// another site is refused as one, key or no key.
func TestKeyDoesNotOpenTheOtherChecks(t *testing.T) {
	ts, e, _ := newTS(t)
	resp := doPlainRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{
		"Authorization": "token " + testKey, "Origin": "http://evil.example",
	}, "")
	assertForbiddenNoCall(t, e, resp)
	resp = doPlainRequest(t, http.MethodGet, ts.URL+"/api/state?token="+testKey, map[string]string{"Host": "evil.example"}, "")
	if resp.Header.Get("Set-Cookie") != "" {
		t.Fatal("a foreign host must never be given the cookie")
	}
	assertForbiddenNoCall(t, e, resp)
}

// A right ?token= is always exchanged, even on a request that already
// carries the key, so the key never stays in the address bar.
func TestTokenIsExchangedEvenWithTheKey(t *testing.T) {
	ts, _, _ := newTS(t)
	port := portOfTS(t, ts)
	for _, h := range []map[string]string{
		{"Authorization": "token " + testKey},
		{"Cookie": "ccb_key_" + port + "=" + testKey},
	} {
		resp := doPlainRequest(t, http.MethodGet, ts.URL+"/?token="+testKey, h, "")
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
			t.Errorf("%v: %d %q", h, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
}

// navigationHeaders are what a browser sends on a top-level navigation
// that another site, or a page opened from a file, started.
func navigationHeaders(site string) map[string]string {
	return map[string]string{"Sec-Fetch-Site": site, "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}
}

// The page CC Babysitter opens a browser on is a file, so the navigation
// it starts arrives as cross-site, and a cookie set on a redirect in that
// chain would not be sent on the next step. The exchange then answers with
// a page that moves on from this same origin, and carries no data.
func TestCrossSiteNavigationWithTheTokenIsExchangedByAPage(t *testing.T) {
	ts, e, _ := newTS(t)
	port := portOfTS(t, ts)
	resp := doPlainRequest(t, http.MethodGet, ts.URL+"/api/activity?n=5&token="+testKey, navigationHeaders("cross-site"), "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("%d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	page := string(body)
	if !strings.Contains(page, `<meta http-equiv="refresh" content="0;url=/api/activity?n=5">`) || strings.Contains(page, testKey) {
		t.Fatalf("exchange page %q", page)
	}
	// The page moves on with location.replace, so the address with the key
	// does not stay behind in the history, and the meta refresh stays as
	// the way on when scripts are off. Only that one script may run.
	script := pageScript(t, page)
	if script != `location.replace("/api/activity?n=5")` {
		t.Fatalf("exchange script %q", script)
	}
	sum := sha256.Sum256([]byte(script))
	hash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src "+hash+";") || !strings.Contains(csp, "default-src 'self';") ||
		!strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "base-uri 'none'") ||
		!strings.Contains(csp, "form-action 'none'") || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("exchange page CSP %q, want the script's hash %s", csp, hash)
	}
	var got *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "ccb_key_"+port {
			got = c
		}
	}
	if got == nil || got.Value != testKey || !got.HttpOnly || got.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie %v", resp.Cookies())
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("the exchange page must carry the security headers")
	}
	if len(e.calls) != 0 {
		t.Fatal("engine must not be called:", e.calls)
	}

	// The step after it comes from this same origin with the cookie.
	resp = doPlainRequest(t, http.MethodGet, ts.URL+"/api/activity?n=5", map[string]string{
		"Sec-Fetch-Site": "same-origin", "Cookie": "ccb_key_" + port + "=" + testKey,
	}, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("after the exchange: %d", resp.StatusCode)
	}
	// An ordinary answer carries the default policy exactly: only the
	// exchange page is ever allowed a script by hash.
	if csp := resp.Header.Get("Content-Security-Policy"); csp != cspDefault+cspRest || strings.Contains(csp, "script-src") {
		t.Fatalf("ordinary answer CSP %q, want %q", csp, cspDefault+cspRest)
	}
}

// pageScript is the text of the one inline script in page, failing when
// there is not exactly one.
func pageScript(t *testing.T, page string) string {
	t.Helper()
	if strings.Count(page, "<script") != 1 || strings.Count(page, "</script>") != 1 {
		t.Fatalf("want exactly one script in %q", page)
	}
	_, rest, _ := strings.Cut(page, "<script>")
	script, _, _ := strings.Cut(rest, "</script>")
	return script
}

// The clean path goes into the exchange page's script as a JSON string, so
// nothing in it, a quote or a closing script tag, can end the string or
// the script early, and what the script is given is the path itself.
func TestExchangeScriptCannotBeBrokenOutOf(t *testing.T) {
	for _, loc := range []string{
		`/x"+alert(1)+"`, `/x');alert(1);//`, `/x</script><script>alert(1)</script>`,
		"/x\u2028\u2029", "/x\\\"", `/x<!--<script>`,
	} {
		script := exchangeScript(loc)
		inner, ok := strings.CutPrefix(script, "location.replace(")
		inner, ok2 := strings.CutSuffix(inner, ")")
		if !ok || !ok2 {
			t.Errorf("%q: script %q", loc, script)
			continue
		}
		var got string
		if err := json.Unmarshal([]byte(inner), &got); err != nil || got != loc {
			t.Errorf("%q: script gives %q, %v", loc, got, err)
		}
		if strings.ContainsAny(inner, "<>\u2028\u2029") || strings.Count(inner, `"`)-strings.Count(inner, `\"`) != 2 {
			t.Errorf("%q: script %q can end early", loc, script)
		}
	}

	// And through the guard: a path with a quote and a closing script tag
	// in it still gives a page with one script, which the CSP allows.
	ts, _, _ := newTS(t)
	resp := doPlainRequest(t, http.MethodGet, ts.URL+`/a'%22%3C/script%3E%3Cscript%3Ealert(1)//?token=`+testKey, navigationHeaders("cross-site"), "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	script := pageScript(t, string(body))
	sum := sha256.Sum256([]byte(script))
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") {
		t.Fatalf("CSP %q does not allow %q", resp.Header.Get("Content-Security-Policy"), script)
	}
	if !strings.Contains(string(body), `<meta http-equiv="refresh"`) {
		t.Fatalf("no meta refresh in %q", body)
	}
}

// The exception for a cross-site navigation is only ever the exchange:
// another site's page still cannot read anything or reach the API, with
// or without a token, and the exchange page never sends it elsewhere.
func TestCrossSiteStillCannotReachTheAPI(t *testing.T) {
	ts, e, _ := newTS(t)
	other := strings.Repeat("cd", 32)
	for name, c := range map[string]struct {
		url     string
		headers map[string]string
	}{
		"navigation without a token":  {"/api/state", navigationHeaders("cross-site")},
		"navigation with a wrong one": {"/api/state?token=" + other, navigationHeaders("cross-site")},
		"navigation with the cookie": {"/api/state", map[string]string{
			"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document", "Authorization": "token " + testKey}},
		"an image with the token":  {"/api/state?token=" + testKey, map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"}},
		"a frame with the token":   {"/?token=" + testKey, map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe"}},
		"a fetch with the token":   {"/api/state?token=" + testKey, map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty"}},
		"an origin with the token": {"/api/state?token=" + testKey, map[string]string{"Origin": "http://evil.example", "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}},
	} {
		resp := doPlainRequest(t, http.MethodGet, ts.URL+c.url, c.headers, "")
		if resp.Header.Get("Set-Cookie") != "" {
			t.Errorf("%s: given the cookie", name)
		}
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", name, resp.StatusCode)
		}
		resp.Body.Close()
	}
	for _, path := range []string{"//evil.example/x", "/\\evil.example/x", "/%2F%2Fevil.example", "/%5Cevil.example"} {
		resp := doPlainRequest(t, http.MethodGet, ts.URL+path+"?token="+testKey, navigationHeaders("cross-site"), "")
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		_, after, _ := strings.Cut(string(body), `content="0;url=`)
		if !strings.HasPrefix(after, "/") || strings.HasPrefix(after, "//") || strings.HasPrefix(after, "/\\") {
			t.Errorf("%s: exchange page %q", path, body)
		}
	}
	if len(e.calls) != 0 {
		t.Fatal("engine must not be called:", e.calls)
	}
}

// launchToken pulls the token out of a LaunchURL answer.
func launchToken(t *testing.T, ts *httptest.Server, u string) string {
	t.Helper()
	tok, ok := strings.CutPrefix(u, ts.URL+"/?token=")
	if !ok || len(tok) != 64 || tok == testKey {
		t.Fatalf("launch URL %q", u)
	}
	return tok
}

// keyCookie is the key cookie an answer sets for ts's port, or nil.
func keyCookie(t *testing.T, ts *httptest.Server, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == "ccb_key_"+portOfTS(t, ts) {
			return c
		}
	}
	return nil
}

// The browser is opened with a launch token, not the key: it works once,
// sets the cookie with the real key, and is no key at all after that.
func TestLaunchTokenWorksOnce(t *testing.T) {
	ts, _, s := newTS(t)
	u := s.LaunchURL(ts.URL)
	launchToken(t, ts, u)
	if s.LaunchURL(ts.URL) == u {
		t.Fatal("two launches got the same token")
	}

	resp := doPlainRequest(t, http.MethodGet, u, nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("first use: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if c := keyCookie(t, ts, resp); c == nil || c.Value != s.session || c.Value == testKey {
		t.Fatalf("first use set %v, want this run's session, not the key", resp.Cookies())
	}

	resp = doPlainRequest(t, http.MethodGet, u, nil, "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("Set-Cookie") != "" || !strings.Contains(string(body), keyRefusal) {
		t.Fatalf("second use: %d %q", resp.StatusCode, body)
	}
}

func TestLaunchTokenExpires(t *testing.T) {
	ts, _, s := newTS(t)
	now := time.Now()
	s.launch.now = func() time.Time { return now }

	fresh := s.LaunchURL(ts.URL)
	stale := s.LaunchURL(ts.URL)
	now = now.Add(launchTokenLife - time.Second)
	resp := doPlainRequest(t, http.MethodGet, fresh, nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("just inside its life: %d", resp.StatusCode)
	}
	now = now.Add(2 * time.Second)
	resp = doPlainRequest(t, http.MethodGet, stale, nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("Set-Cookie") != "" {
		t.Fatalf("expired: %d", resp.StatusCode)
	}
	if launchTokenLife != 2*time.Minute {
		t.Fatalf("launch token life %v", launchTokenLife)
	}
}

// The launch token opens the cross-site navigation exchange too, which is
// how a browser opened on it may arrive, and only once there as well.
func TestLaunchTokenOnACrossSiteNavigation(t *testing.T) {
	ts, _, s := newTS(t)
	u := s.LaunchURL(ts.URL)
	resp := doPlainRequest(t, http.MethodGet, u, navigationHeaders("cross-site"), "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if c := keyCookie(t, ts, resp); resp.StatusCode != http.StatusOK || c == nil || c.Value != s.session || strings.Contains(string(body), testKey) || strings.Contains(string(body), s.session) {
		t.Fatalf("first use: %d %v %q", resp.StatusCode, resp.Cookies(), body)
	}
	resp = doPlainRequest(t, http.MethodGet, u, navigationHeaders("cross-site"), "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("Set-Cookie") != "" {
		t.Fatalf("second use: %d", resp.StatusCode)
	}
}

// A launch token is only ever exchanged for the cookie: it is not a key
// in a header, never works on a POST, and is not used up by attempts that
// do not exchange it.
func TestLaunchTokenIsNotAKey(t *testing.T) {
	ts, e, s := newTS(t)
	u := s.LaunchURL(ts.URL)
	tok := launchToken(t, ts, u)
	for _, h := range []string{"token " + tok, "Bearer " + tok} {
		resp := doPlainRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Authorization": h}, "")
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("as a header: %d", resp.StatusCode)
		}
	}
	resp := doPlainRequest(t, http.MethodGet, ts.URL+"/api/state", map[string]string{"Cookie": "ccb_key_" + portOfTS(t, ts) + "=" + tok}, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("as a cookie: %d", resp.StatusCode)
	}
	resp = doPlainRequest(t, http.MethodPost, ts.URL+"/api/sessions/"+testID1+"/babysit?token="+tok, map[string]string{"Content-Type": "application/json"}, "{}")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || len(e.calls) != 0 {
		t.Fatalf("on a POST: %d %v", resp.StatusCode, e.calls)
	}
	resp = doPlainRequest(t, http.MethodGet, ts.URL+"/api/state?token="+tok, map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"}, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("from another site's image: %d", resp.StatusCode)
	}
	resp = doPlainRequest(t, http.MethodGet, u, nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("the attempts above used it up: %d", resp.StatusCode)
	}
}

// Two requests racing with the same launch token: exactly one wins.
func TestLaunchTokenRaceHasOneWinner(t *testing.T) {
	ts, _, s := newTS(t)
	u := s.LaunchURL(ts.URL)
	const n = 8
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := c.Get(u)
			if err != nil {
				codes <- 0
				return
			}
			resp.Body.Close()
			codes <- resp.StatusCode
		}()
	}
	wg.Wait()
	close(codes)
	won := 0
	for c := range codes {
		if c == http.StatusSeeOther {
			won++
		} else if c != http.StatusUnauthorized {
			t.Errorf("status %d", c)
		}
	}
	if won != 1 {
		t.Fatalf("%d requests exchanged the same launch token", won)
	}
}

// An exchange answer never tells the next page where the browser came
// from, so the address with the token does not leave in a Referer header.
func TestExchangesSendNoReferrer(t *testing.T) {
	ts, _, s := newTS(t)
	for name, c := range map[string]struct {
		url     string
		headers map[string]string
	}{
		"key, 303":                {ts.URL + "/?token=" + testKey, nil},
		"key, cross-site page":    {ts.URL + "/?token=" + testKey, navigationHeaders("cross-site")},
		"launch, 303":             {s.LaunchURL(ts.URL), nil},
		"launch, cross-site page": {s.LaunchURL(ts.URL), navigationHeaders("cross-site")},
	} {
		resp := doPlainRequest(t, http.MethodGet, c.url, c.headers, "")
		resp.Body.Close()
		if resp.Header.Get("Set-Cookie") == "" || resp.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: %d, Referrer-Policy %q", name, resp.StatusCode, resp.Header.Get("Referrer-Policy"))
		}
	}
}

// A browser that already holds the key in its cookie keeps it when it
// follows a launch address: the launch does not trade the key for this
// run's session, which would stop working on the next restart. A cookie
// holding anything else is still replaced with the session.
func TestLaunchKeepsAKeyCookie(t *testing.T) {
	ts, _, s := newTS(t)
	name := "ccb_key_" + portOfTS(t, ts)
	for _, hdr := range []map[string]string{
		{"Cookie": name + "=" + testKey},
		{"Cookie": name + "=" + testKey, "Sec-Fetch-Site": "none", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"},
	} {
		resp := doPlainRequest(t, http.MethodGet, s.LaunchURL(ts.URL), hdr, "")
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("%v: %d, want 303", hdr, resp.StatusCode)
		}
		if c := keyCookie(t, ts, resp); c == nil || c.Value != testKey {
			t.Fatalf("%v: set %v, want the key kept", hdr, resp.Cookies())
		}
	}
	for _, held := range []string{s.session, strings.Repeat("cd", 32)} {
		resp := doPlainRequest(t, http.MethodGet, s.LaunchURL(ts.URL), map[string]string{"Cookie": name + "=" + held}, "")
		resp.Body.Close()
		if c := keyCookie(t, ts, resp); c == nil || c.Value != s.session {
			t.Fatalf("cookie %q: set %v, want this run's session", held, resp.Cookies())
		}
	}
}

// Whoever uses a launch token first, which may be another account that
// read it off the opener's command line, gets a session for this run of
// the server only, never the key: the cookie stops working on a restart.
// The address with the key still sets the key, since whoever has that has
// the key anyway.
func TestLaunchGivesASessionNotTheKey(t *testing.T) {
	ts, _, s := newTS(t)
	if !state.ValidPageKey(s.session) || s.session == testKey {
		t.Fatalf("session %q", s.session)
	}
	resp := doPlainRequest(t, http.MethodGet, s.LaunchURL(ts.URL), nil, "")
	resp.Body.Close()
	c := keyCookie(t, ts, resp)
	if c == nil || c.Value != s.session {
		t.Fatalf("cookie %v", resp.Cookies())
	}
	cookie := map[string]string{"Cookie": c.Name + "=" + c.Value}
	resp = doPlainRequest(t, http.MethodGet, ts.URL+"/api/state", cookie, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the session cookie on this server: %d", resp.StatusCode)
	}

	// The session is not a key: not in a header, not as a token.
	for _, h := range []map[string]string{
		{"Authorization": "token " + s.session},
		{"Authorization": "Bearer " + s.session},
	} {
		resp = doPlainRequest(t, http.MethodGet, ts.URL+"/api/state", h, "")
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%v: %d, want 401", h, resp.StatusCode)
		}
	}
	for _, hdr := range []map[string]string{nil, navigationHeaders("cross-site")} {
		resp = doPlainRequest(t, http.MethodGet, ts.URL+"/?token="+s.session, hdr, "")
		resp.Body.Close()
		if resp.StatusCode == http.StatusSeeOther || resp.StatusCode == http.StatusOK || resp.Header.Get("Set-Cookie") != "" {
			t.Errorf("?token=<session> %v: %d", hdr, resp.StatusCode)
		}
	}

	// A new run with the same key has a session of its own.
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	next := NewServer(&fakeEngine{view: supervise.View{Version: "0.1.0"}}, log, "0.1.0", testKey)
	ts2 := httptest.NewServer(next.Handler())
	t.Cleanup(ts2.Close)
	if next.session == s.session {
		t.Fatal("two runs share a session")
	}
	resp = doPlainRequest(t, http.MethodGet, ts2.URL+"/api/state", map[string]string{"Cookie": "ccb_key_" + portOfTS(t, ts2) + "=" + s.session}, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the old session on a new run: %d, want 401", resp.StatusCode)
	}

	// The address with the key still sets the key.
	resp = doPlainRequest(t, http.MethodGet, ts.URL+"/?token="+testKey, nil, "")
	resp.Body.Close()
	if c := keyCookie(t, ts, resp); c == nil || c.Value != testKey {
		t.Fatalf("?token=<key> set %v", resp.Cookies())
	}
}

// Another web page cannot make CC Babysitter quit: not by a cross-site
// post, not from a foreign origin, not as a form, and not without the key.
func TestAnotherPageCannotQuit(t *testing.T) {
	for name, headers := range map[string]map[string]string{
		"cross-site":     {"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site"},
		"foreign origin": {"Content-Type": "application/json", "Origin": "http://evil.example"},
		"form post":      {"Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"},
	} {
		ts, _, s := newTS(t)
		called := false
		s.OnQuit(func() { called = true })
		resp := doRequest(t, http.MethodPost, ts.URL+"/api/quit", headers, "{}")
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden || called {
			t.Errorf("%s: %d, hook called %v", name, resp.StatusCode, called)
		}
	}
	ts, _, s := newTS(t)
	called := false
	s.OnQuit(func() { called = true })
	resp := doPlainRequest(t, http.MethodPost, ts.URL+"/api/quit", map[string]string{"Content-Type": "application/json"}, "{}")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || called {
		t.Errorf("no key: %d, hook called %v", resp.StatusCode, called)
	}
}
