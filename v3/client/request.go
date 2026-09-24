package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-resty/resty/v2"
)

// Result provides the parsed API response
// Code/Msg come from the business layer of the API envelope, HTTPStatus from
// the transport; they are independent, since this API carries every business
// code on an HTTP 200 response
type Result struct {
	client     *Client
	Code       int    // Business error code from API response (200 = success, 1001 = permission denied, etc.)
	Msg        string // Business error message from API response
	Body       []byte // The envelope's "data" field verbatim, never re-encoded; null for the codes that carry no payload
	HTTPStatus int    // HTTP status of the last attempt, 0 when no response was received
	Err        error  // Transport, HTTP status or decoding failure (not API business logic errors)
	RequestID  string // Request ID of the last attempt, for tracing
}

// Sender provides a basic struct to send request
type Sender struct {
	client  *Client
	url     string
	method  string
	payload []byte
	err     error
}

// apiEnvelope is the wrapper every endpoint returns.
// Data stays raw because decoding it through `any` would route large integers
// (snowflake IDs) past float64 and silently round them.
type apiEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

const requestIDHeader = "x-request-id"

// Send provides a sender to send request
func (c *Client) Send(url string, method string, payload any) *Sender {
	s := &Sender{client: c, url: url, method: method}
	if payload == nil {
		return s
	}
	body, err := c.marshal(payload)
	if err != nil {
		s.err = err
		return s
	}
	s.payload = body
	return s
}

// WithToken sends a request with token to authorise
func (s *Sender) WithToken() *Result {
	return s.doRequest(AuthTypeToken)
}

// WithKey sends a request with SecretID and SecretKey to authorise
func (s *Sender) WithKey() *Result {
	return s.doRequest(AuthTypeKey)
}

// OK returns whether the business-layer API call was successful
// It checks if Code (business error code) == 200, which indicates success
// This is different from HTTP status code (HTTPStatus), which is checked separately
func (r *Result) OK() bool {
	return r.Code == codeSuccess
}

// Unmarshal can unmarshal a request data body to customised struct
// A response whose envelope carried no data field decodes into nothing
func (r *Result) Unmarshal(v any) error {
	if len(r.Body) == 0 {
		return nil
	}
	return r.client.unmarshal(r.Body, v)
}

// doRequest issues the request, then replays it once after a token renewal if
// the API reported a stale credential. The replay is not renewed again, so a
// caller can never be stuck in a renewal loop
func (s *Sender) doRequest(authType AuthType) *Result {
	if s.err != nil {
		return &Result{client: s.client, Err: s.err}
	}

	result := s.attempt(authType)

	if classify(result.Code, s.idempotent()) != actionRenewToken ||
		authType != AuthTypeToken || !s.client.enableToken {
		return result
	}

	s.client.Logger.Debug(nil, fmt.Sprintf(
		"permission denied, maybe token expired, trying to renew, requestID %s", result.RequestID,
	))
	time.Sleep(time.Duration(s.client.retryDelay) * time.Second)

	if err := applyToken(s.client); err != nil {
		result.Err = err
		return result
	}
	return s.attempt(authType)
}

// attempt hands the request to resty, which owns the transport-level retry and
// backoff, and maps the last attempt onto a Result
func (s *Sender) attempt(authType AuthType) *Result {
	req := s.client.rc.R().
		AddRetryCondition(s.shouldRetry)

	switch authType {
	case AuthTypeToken:
		req.SetAuthToken(s.client.token)
	case AuthTypeKey:
		req.SetBasicAuth(s.client.secretID, s.client.secretKey)
	}
	if s.method == http.MethodPost {
		req.SetHeader("Content-Type", "application/json")
	}
	if s.payload != nil {
		req.SetBody(s.payload)
	}

	s.client.Logger.Debug(nil, fmt.Sprintf(
		"send request to %s, method %s with %s", s.url, s.method, authType,
	))
	res, err := req.Execute(s.method, s.url)
	return s.toResult(res, err)
}

// action is what the SDK may do about a response.
type action int

const (
	actionDone       action = iota // hand it to the caller as-is
	actionRetry                    // transient failure, safe to replay
	actionRenewToken               // credential went stale, refresh and replay
)

// classify maps a business code onto what the SDK may do about it.
//
// Whether replaying is safe depends on where the code was produced, not on the
// code alone: 429 comes from the permission middleware before quota is taken or
// the handler runs, so it can never describe a partially applied request and is
// replayable even for POST. 500 and 7001 come from the handler and can follow an
// upstream call that already succeeded (short link creation has no
// de-duplication), so only idempotent requests may replay them.
func classify(code int, idempotent bool) action {
	switch code {
	case codePermissionDenied:
		return actionRenewToken
	case codeThrottled:
		return actionRetry
	case codeServerError, codeUpstreamUnavailable:
		if idempotent {
			return actionRetry
		}
	}
	return actionDone
}

// idempotent reports whether replaying this request can be observed downstream
func (s *Sender) idempotent() bool {
	return s.method == http.MethodGet
}

// shouldRetry decides whether resty issues another attempt.
//
// Transport-level trouble is only ever replayed for idempotent requests. A
// business code is judged by classify, which is the one place that knows why
// each code is or is not safe to replay.
func (s *Sender) shouldRetry(res *resty.Response, err error) bool {
	if err != nil || res == nil {
		return s.idempotent()
	}
	if !is2xx(res.StatusCode()) {
		return s.idempotent()
	}
	envelope, decodeErr := s.decode(res.Body())
	if decodeErr != nil {
		return s.idempotent()
	}
	return classify(envelope.Code, s.idempotent()) == actionRetry
}

// toResult maps the last attempt onto a Result. The request ID and HTTP status
// are kept even when the attempt failed, so callers can trace a dead request.
func (s *Sender) toResult(res *resty.Response, err error) *Result {
	result := &Result{client: s.client, Err: err}
	if res == nil {
		return result
	}

	result.HTTPStatus = res.StatusCode()
	result.RequestID = res.Header().Get(requestIDHeader)
	if err != nil {
		return result
	}

	envelope, decodeErr := s.decode(res.Body())
	if decodeErr == nil {
		result.Code, result.Msg = envelope.Code, envelope.Msg
		result.Body = envelope.Data
	}

	switch {
	case decodeErr != nil:
		result.Err = decodeErr
	case !is2xx(result.HTTPStatus):
		result.Err = fmt.Errorf("received http status %d", result.HTTPStatus)
	}

	s.client.Logger.Debug(nil, fmt.Sprintf(
		"openAPI response httpCode %d, apiCode %d, requestID %s, attempts %d, responseBody %s",
		result.HTTPStatus, result.Code, result.RequestID, res.Request.Attempt, res.Body(),
	))
	return result
}

// decode reads the business layer out of a response body
func (s *Sender) decode(body []byte) (*apiEnvelope, error) {
	var envelope apiEnvelope
	if err := s.client.unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	return &envelope, nil
}

// is2xx reports whether an HTTP status counts as a successful transport result
func is2xx(status int) bool {
	return status >= http.StatusOK && status < http.StatusMultipleChoices
}
