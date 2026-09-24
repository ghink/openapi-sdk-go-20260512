package openapi_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	openapi "go.gh.ink/openapi/sdk/20260512/v3"
	"go.gh.ink/openapi/sdk/20260512/v3/client"
	realname "go.gh.ink/openapi/sdk/20260512/v3/private/real_name"
	shortlink "go.gh.ink/openapi/sdk/20260512/v3/public/short_link"
)

// api is a stand-in for the Ghink Open API.
//
// The real service answers every request on HTTP 200 and puts the outcome in
// the envelope's code field, so these tests ask for a code rather than a
// status; a true non-2xx only stands for a failure in front of the handler.
type api struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	calls   []apiCall
	mints   int
	apiHits int
}

type apiCall struct {
	method      string
	path        string
	auth        string
	contentType string
	userAgent   string
	body        string
}

// serve records every request, then hands it to handle. Tokens are minted on
// demand so a renewal can be told apart from the bootstrap fetch.
func serve(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, a *api)) *api {
	t.Helper()
	a := &api{t: t}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		a.mu.Lock()
		a.calls = append(a.calls, apiCall{
			method:      r.Method,
			path:        r.URL.Path,
			auth:        r.Header.Get("Authorization"),
			contentType: r.Header.Get("Content-Type"),
			userAgent:   r.Header.Get("User-Agent"),
			body:        string(body),
		})
		isToken := r.URL.Path == tokenPath
		if isToken {
			a.mints++
		} else {
			a.apiHits++
		}
		index := len(a.calls)
		a.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-request-id", fmt.Sprintf("rid-%d", index))
		handle(w, r, a)
	}))
	t.Cleanup(a.srv.Close)
	return a
}

// The paths the service registers under its current version prefix.
const (
	tokenPath    = "/v3.1/openapi/token"
	shortAddPath = "/v3.1/short_link/add"
	cnidPath     = "/v3.1/real_name/cnid"
)

func (a *api) apiCalls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.apiHits
}

func (a *api) tokenMints() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mints
}

// call returns the 1-based request.
func (a *api) call(i int) apiCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i < 1 || i > len(a.calls) {
		a.t.Fatalf("no request %d (only %d were made)", i, len(a.calls))
	}
	return a.calls[i-1]
}

func (a *api) endpoint() string       { return a.srv.URL + "/v3.1" }
func (a *api) url(path string) string { return a.srv.URL + path }

// envelope answers with a business code on HTTP 200.
func envelope(w http.ResponseWriter, code int, msg, data string) {
	fmt.Fprintf(w, `{"code":%d,"msg":%q,"data":%s}`, code, msg, data)
}

func succeed(w http.ResponseWriter, data string) { envelope(w, 200, "success", data) }

// tokenOK mints a fresh token, which the service also does on every fetch.
func tokenOK(w http.ResponseWriter, a *api) {
	fmt.Fprintf(w, `{"code":200,"msg":"success","data":{"token":"tok-%d"}}`, a.tokenMints())
}

// quietLogger keeps SDK and resty diagnostics out of test output.
type quietLogger struct{}

func (quietLogger) Debug(context.Context, ...any) {}
func (quietLogger) Info(context.Context, ...any)  {}
func (quietLogger) Warn(context.Context, ...any)  {}
func (quietLogger) Error(context.Context, ...any) {}

// newClient builds a client against the mock. One second is the shortest retry
// gap the SDK allows, so cases that exercise backoff cost a second each.
func newClient(t *testing.T, a *api, options ...client.Option) *client.Client {
	t.Helper()
	defaults := []client.Option{
		client.WithEndpoint(a.endpoint()),
		client.WithMaxRetries(2),
		client.WithRetryDelay(1),
		client.WithExponentialBackoff(false),
		client.WithLogger(quietLogger{}),
	}
	c, err := client.NewClient("sid", "skey", append(defaults, options...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// always denies with the given code; a struct handler keeps the cases readable.
func denyWith(code int) func(w http.ResponseWriter, r *http.Request, a *api) {
	return func(w http.ResponseWriter, r *http.Request, a *api) {
		if r.URL.Path == tokenPath {
			tokenOK(w, a)
			return
		}
		envelope(w, code, "scenario", "null")
	}
}

// Every request has to identify the SDK, on the token fetch as much as on the
// work itself.
func TestUserAgentOnEveryRequest(t *testing.T) {
	a := serve(t, func(w http.ResponseWriter, r *http.Request, a *api) {
		if r.URL.Path == tokenPath {
			tokenOK(w, a)
			return
		}
		succeed(w, "null")
	})
	c := newClient(t, a)

	if res := c.Send(a.url("/v3.1/time"), http.MethodGet, nil).WithToken(); res.Err != nil {
		t.Fatalf("send: %v", res.Err)
	}
	for i := 1; i <= a.apiCalls()+1; i++ {
		if got := a.call(i).userAgent; got != openapi.UserAgent {
			t.Errorf("request %d User-Agent = %q, want %q", i, got, openapi.UserAgent)
		}
	}
}

// The wire contract: version-prefixed paths, Basic for a token, Bearer for
// work, JSON body only where there is one.
func TestWireContract(t *testing.T) {
	a := serve(t, func(w http.ResponseWriter, r *http.Request, a *api) {
		if r.URL.Path == tokenPath {
			tokenOK(w, a)
			return
		}
		succeed(w, `{"link_id":"https://gh.ink/s/ab12cd"}`)
	})
	c := newClient(t, a)

	linkID, err := shortlink.Add(c, "https://example.com/page", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if linkID != "https://gh.ink/s/ab12cd" {
		t.Errorf("linkID = %q", linkID)
	}

	tok := a.call(1)
	if tok.method != http.MethodGet || tok.path != tokenPath {
		t.Errorf("token fetch = %s %s, want GET %s", tok.method, tok.path, tokenPath)
	}
	if !strings.HasPrefix(tok.auth, "Basic ") {
		t.Errorf("token fetch auth = %q, want HTTP Basic", tok.auth)
	}

	add := a.call(2)
	if add.method != http.MethodPost || add.path != shortAddPath {
		t.Errorf("add = %s %s, want POST %s", add.method, add.path, shortAddPath)
	}
	if add.auth != "Bearer tok-1" {
		t.Errorf("add auth = %q, want the token just minted", add.auth)
	}
	if add.contentType != "application/json" {
		t.Errorf("add content-type = %q", add.contentType)
	}
	if add.body != `{"link":"https://example.com/page","validity":null}` {
		t.Errorf("add body = %s", add.body)
	}

	// A GET invents no body and no content-type
	if res := c.Send(a.url("/v3.1/time"), http.MethodGet, nil).WithToken(); res.Err != nil {
		t.Fatalf("send: %v", res.Err)
	}
	get := a.call(3)
	if get.body != "" || get.contentType != "" {
		t.Errorf("GET carried body %q and content-type %q", get.body, get.contentType)
	}
}

// Retry decisions come from the business code plus whether the request can be
// duplicated downstream, not from the HTTP status.
func TestRetryPolicyByCode(t *testing.T) {
	cases := []struct {
		name     string
		code     int
		wantGet  int
		wantPost int
	}{
		{"success", 200, 1, 1},
		// throttled before quota is taken or the handler runs, so replaying
		// cannot duplicate anything
		{"throttled", 429, 2, 2},
		// produced by the handler, after a write may have reached upstream
		{"server error", 500, 2, 1},
		{"upstream unavailable", 7001, 2, 1},
		// denial is not retried by the transport; only the renewal replays it
		{"permission denied", 1001, 2, 2},
		{"no quota", 1002, 1, 1},
		{"application gone", 1003, 1, 1},
		{"missing parameter", 2001, 1, 1},
		{"invalid link uri", 2002, 1, 1},
		{"geo not found", 4001, 1, 1},
	}

	for _, tc := range cases {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			want := tc.wantGet
			if method == http.MethodPost {
				want = tc.wantPost
			}
			t.Run(fmt.Sprintf("%s/%s", tc.name, method), func(t *testing.T) {
				a := serve(t, denyWith(tc.code))
				c := newClient(t, a)

				c.Send(a.url("/v3.1/work"), method, map[string]string{"k": "v"}).WithToken()
				if got := a.apiCalls(); got != want {
					t.Errorf("endpoint hit %d times, want %d", got, want)
				}
			})
		}
	}
}

// A throttled call that would then succeed has to come back, for both methods.
func TestThrottledRequestRecovers(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			var seen int
			var mu sync.Mutex
			a := serve(t, func(w http.ResponseWriter, r *http.Request, a *api) {
				if r.URL.Path == tokenPath {
					tokenOK(w, a)
					return
				}
				mu.Lock()
				seen++
				first := seen == 1
				mu.Unlock()
				if first {
					envelope(w, 429, "too many requests", "null")
					return
				}
				succeed(w, `{"link_id":"https://gh.ink/s/ok"}`)
			})
			c := newClient(t, a)

			res := c.Send(a.url(shortAddPath), method, map[string]string{"link": "x"}).WithToken()
			if !res.OK() {
				t.Fatalf("still failing after the throttle: code=%d msg=%q err=%v", res.Code, res.Msg, res.Err)
			}
			if got := a.apiCalls(); got != 2 {
				t.Errorf("endpoint hit %d times, want 2", got)
			}
		})
	}
}

// A stale token is renewed once and replayed with the new credential; the
// denial is then handed to the caller instead of being looped on.
func TestTokenRenewalReissuesOnce(t *testing.T) {
	a := serve(t, denyWith(1001))
	c := newClient(t, a)

	res := c.Send(a.url(shortAddPath), http.MethodPost, map[string]string{"link": "x"}).WithToken()

	if got := a.tokenMints(); got != 2 {
		t.Errorf("minted %d tokens, want 2: bootstrap plus one renewal", got)
	}
	if got := a.apiCalls(); got != 2 {
		t.Errorf("endpoint hit %d times, want 2", got)
	}
	if got := a.call(2).auth; got != "Bearer tok-1" {
		t.Errorf("first attempt auth = %q", got)
	}
	if got := a.call(4).auth; got != "Bearer tok-2" {
		t.Errorf("replay auth = %q, want the renewed token", got)
	}
	// the denial reaches the caller as a code, not as a lost retry count
	if res.Code != 1001 {
		t.Errorf("Code = %d, want the denial surfaced", res.Code)
	}
	if res.Err != nil {
		t.Errorf("Err = %v, want nil for a completed business rejection", res.Err)
	}
	if res.RequestID == "" {
		t.Error("RequestID lost on a denied call")
	}
}

// With key credentials there is nothing to renew.
func TestKeyAuthDoesNotRenew(t *testing.T) {
	a := serve(t, denyWith(1001))
	c := newClient(t, a)

	res := c.Send(a.url(shortAddPath), http.MethodPost, map[string]string{"link": "x"}).WithKey()

	if got := a.tokenMints(); got != 1 {
		t.Errorf("minted %d tokens, want only the bootstrap", got)
	}
	if got := a.apiCalls(); got != 1 {
		t.Errorf("endpoint hit %d times, want 1", got)
	}
	if res.Code != 1001 || res.OK() {
		t.Errorf("code=%d ok=%v, want the denial surfaced", res.Code, res.OK())
	}
}

// A request that never got a usable answer must still report what it saw.
func TestFailedRequestKeepsTracing(t *testing.T) {
	a := serve(t, func(w http.ResponseWriter, r *http.Request, a *api) {
		if r.URL.Path == tokenPath {
			tokenOK(w, a)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "gateway down")
	})
	c := newClient(t, a)

	res := c.Send(a.url("/v3.1/work"), http.MethodPost, nil).WithToken()

	if res.Err == nil {
		t.Fatal("want an error for a 503")
	}
	if res.HTTPStatus != http.StatusServiceUnavailable {
		t.Errorf("HTTPStatus = %d, want 503", res.HTTPStatus)
	}
	if res.RequestID == "" {
		t.Error("RequestID lost when the call failed")
	}
	if got := a.apiCalls(); got != 1 {
		t.Errorf("POST replayed %d times, want 1", got)
	}
}

// data is handed over verbatim, so an integer beyond float64 precision survives.
func TestEnvelopeDataVerbatim(t *testing.T) {
	const big = int64(9007199254740993) // 2^53 + 1, not representable as float64

	a := serve(t, func(w http.ResponseWriter, r *http.Request, a *api) {
		if r.URL.Path == tokenPath {
			tokenOK(w, a)
			return
		}
		succeed(w, fmt.Sprintf(`{"id":%d,"link_id":"l"}`, big))
	})
	c := newClient(t, a)

	res := c.Send(a.url("/v3.1/work"), http.MethodGet, nil).WithToken()
	if !res.OK() {
		t.Fatalf("code=%d err=%v", res.Code, res.Err)
	}
	if got := string(res.Body); !strings.Contains(got, "9007199254740993") {
		t.Errorf("data = %s, want the digits unchanged", got)
	}

	var out struct {
		ID int64 `json:"id"`
	}
	if err := res.Unmarshal(&out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.ID != big {
		t.Errorf("ID = %d, want %d", out.ID, big)
	}
}

// A malformed answer is transport-level: GET retries it, POST does not.
func TestMalformedBodyRetriedForGetOnly(t *testing.T) {
	for _, tc := range []struct {
		method string
		want   int
	}{
		{http.MethodGet, 2},
		{http.MethodPost, 1},
	} {
		t.Run(tc.method, func(t *testing.T) {
			a := serve(t, func(w http.ResponseWriter, r *http.Request, a *api) {
				if r.URL.Path == tokenPath {
					tokenOK(w, a)
					return
				}
				fmt.Fprint(w, `<html>not json</html>`)
			})
			c := newClient(t, a)

			res := c.Send(a.url("/v3.1/work"), tc.method, nil).WithToken()
			if res.Err == nil {
				t.Fatal("want a decoding error")
			}
			if got := a.apiCalls(); got != tc.want {
				t.Errorf("hit %d times, want %d", got, tc.want)
			}
		})
	}
}

// Add maps validity to unix seconds, and a nil one to JSON null.
func TestShortLinkAddPayload(t *testing.T) {
	a := serve(t, func(w http.ResponseWriter, r *http.Request, a *api) {
		if r.URL.Path == tokenPath {
			tokenOK(w, a)
			return
		}
		succeed(w, `{"link_id":"https://gh.ink/s/ok"}`)
	})
	c := newClient(t, a)

	if _, err := shortlink.Add(c, "https://example.com", nil); err != nil {
		t.Fatalf("Add: %v", err)
	}
	validity := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := shortlink.Add(c, "https://example.com", &validity); err != nil {
		t.Fatalf("Add with validity: %v", err)
	}

	if want := `{"link":"https://example.com","validity":null}`; a.call(2).body != want {
		t.Errorf("nil validity = %s, want %s", a.call(2).body, want)
	}
	want := fmt.Sprintf(`{"link":"https://example.com","validity":%d}`, validity.Unix())
	if a.call(3).body != want {
		t.Errorf("validity = %s, want %s", a.call(3).body, want)
	}
	// one token covers the session; Add must not re-mint
	if got := a.tokenMints(); got != 1 {
		t.Errorf("minted %d tokens, want 1", got)
	}
}

// The service echoes the request into data on a parameter rejection, so a
// caller that skipped OK() would decode its own payload instead of a result.
func TestShortLinkAddSurfacesRejection(t *testing.T) {
	a := serve(t, func(w http.ResponseWriter, r *http.Request, a *api) {
		if r.URL.Path == tokenPath {
			tokenOK(w, a)
			return
		}
		envelope(w, 2001, "missing required parameters", `{"link":"","validity":null}`)
	})
	c := newClient(t, a)

	linkID, err := shortlink.Add(c, "", nil)
	if err == nil {
		t.Fatalf("Add accepted a rejection: linkID=%q", linkID)
	}
	if linkID != "" {
		t.Errorf("linkID = %q, want empty", linkID)
	}
	if !strings.Contains(err.Error(), "status code") {
		t.Errorf("err = %v, want the status-code sentinel", err)
	}
}

// A malformed ID is rejected locally, without spending a billed request.
func TestVerifyCNIDLocalShortCircuit(t *testing.T) {
	for _, bad := range []string{"", "123", "11010519491231002A", "11010519491231001", "11010519491332002X"} {
		t.Run(bad, func(t *testing.T) {
			a := serve(t, func(w http.ResponseWriter, r *http.Request, a *api) {
				if r.URL.Path == tokenPath {
					tokenOK(w, a)
					return
				}
				succeed(w, `{"ok":true}`)
			})
			c := newClient(t, a)

			ok, err := realname.VerifyCNID(c, bad, "张三")
			if ok || err != nil {
				t.Errorf("VerifyCNID(%q) = %v, %v; want false, nil", bad, ok, err)
			}
			if got := a.apiCalls(); got != 0 {
				t.Errorf("%q made %d requests, want none", bad, got)
			}
		})
	}
}

// A checksum-valid ID is uppercased before it goes out.
func TestVerifyCNIDRequestShape(t *testing.T) {
	a := serve(t, func(w http.ResponseWriter, r *http.Request, a *api) {
		if r.URL.Path == tokenPath {
			tokenOK(w, a)
			return
		}
		succeed(w, `{"ok":true}`)
	})
	c := newClient(t, a)

	// checksum digit is X; lowercase here to pin the normalisation
	ok, err := realname.VerifyCNID(c, "11010519491231002x", "张三")
	if err != nil {
		t.Fatalf("VerifyCNID: %v", err)
	}
	if !ok {
		t.Error("VerifyCNID = false, want true")
	}

	call := a.call(2)
	if call.path != cnidPath {
		t.Errorf("path = %q, want %s", call.path, cnidPath)
	}
	if call.body != `{"id":"11010519491231002X","name":"张三"}` {
		t.Errorf("body = %s, want the ID uppercased", call.body)
	}
}

// A denied bootstrap has to fail construction rather than yield a client that
// cannot authorise anything.
func TestNewClientFailsWhenTokenDenied(t *testing.T) {
	a := serve(t, func(w http.ResponseWriter, r *http.Request, a *api) {
		envelope(w, 1001, "permission denied", "null")
	})

	_, err := client.NewClient("sid", "skey",
		client.WithEndpoint(a.endpoint()),
		client.WithMaxRetries(2),
		client.WithRetryDelay(1),
		client.WithExponentialBackoff(false),
		client.WithLogger(quietLogger{}),
	)
	if err == nil {
		t.Fatal("NewClient succeeded without a token")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("err = %v, want the token-acquisition failure", err)
	}
}

// A request that dies below HTTP is the case where replaying a POST creates a
// duplicate: the write may already have reached the service before the
// connection dropped. accepts is the only thing that can count attempts here,
// because nothing answers.
func TestTransportFailureNotReplayedForPost(t *testing.T) {
	for _, tc := range []struct {
		method string
		want   int
	}{
		{http.MethodGet, 2},
		{http.MethodPost, 1},
	} {
		t.Run(tc.method, func(t *testing.T) {
			var accepts atomic.Int32
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			go func() {
				for {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					accepts.Add(1)
					_ = conn.Close() // answer nothing
				}
			}()
			t.Cleanup(func() { _ = ln.Close() })

			c, err := client.NewClient("sid", "skey",
				client.WithEndpoint("http://"+ln.Addr().String()+"/v3.1"),
				client.WithMaxRetries(2),
				client.WithRetryDelay(1),
				client.WithExponentialBackoff(false),
				client.WithLogger(quietLogger{}),
				client.EnableToken(false),
			)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}

			res := c.Send("http://"+ln.Addr().String()+"/v3.1/work", tc.method, map[string]string{"k": "v"}).WithKey()
			if res.Err == nil {
				t.Fatal("want a transport error")
			}
			if res.HTTPStatus != 0 {
				t.Errorf("HTTPStatus = %d, want 0 for a request that never got a response", res.HTTPStatus)
			}
			if got := int(accepts.Load()); got != tc.want {
				t.Errorf("dialled %d times, want %d", got, tc.want)
			}
		})
	}
}

// The identification the service logs has to stay in step with the version.
func TestVersionIdentifiers(t *testing.T) {
	if !strings.Contains(openapi.UserAgent, openapi.Version) {
		t.Errorf("User-Agent %q does not carry Version %q", openapi.UserAgent, openapi.Version)
	}
	if !strings.HasPrefix(openapi.Endpoint, "https://") {
		t.Errorf("default endpoint %q is not https", openapi.Endpoint)
	}
	if !strings.HasSuffix(openapi.Endpoint, "/v3.1") {
		t.Errorf("default endpoint %q is not the version the paths assume", openapi.Endpoint)
	}
}
