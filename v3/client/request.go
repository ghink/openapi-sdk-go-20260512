package client

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.gh.ink/openapi/sdk/20260512/v3"
)

// Result provides the parsed API response
// Note: Code is the business-logic error code from API response (not HTTP status code)
// HTTP status codes are handled before parsing and are in the range 200-299 for success
type Result struct {
	client    *Client
	Code      int    // Business error code from API response (200 = success, 1001 = permission denied, etc.)
	Msg       string // Business error message from API response
	Body      []byte // Parsed response body (typically the "data" field from API response)
	Err       error  // Error during request/response processing (not API business logic errors)
	RequestID string // Request ID for tracing
}

// Sender provides a basic struct to send request
type Sender struct {
	client  *Client
	request *http.Request
	err     error
	payload []byte
}

// AuthType defines the authentication method
type AuthType int

// Send provides a sender to send request
func (c *Client) Send(url string, method string, payload any) *Sender {
	// Process payload
	var finalPayload io.Reader = nil
	var payloadBytes []byte
	if payload != nil {
		// Marshal payload
		jsonPayload, err := c.marshal(payload)
		if err != nil {
			return &Sender{
				client: c,
				err:    err,
			}
		}
		payloadBytes = jsonPayload
		finalPayload = strings.NewReader(string(jsonPayload))
	}

	// Build http request
	req, err := http.NewRequest(method, url, finalPayload)
	if err != nil {
		return &Sender{
			client: c,
			err:    err,
		}
	}

	// Set content-type
	if method == http.MethodPost {
		req.Header.Add("Content-Type", "application/json")
	}

	// Return sender
	return &Sender{
		client:  c,
		request: req,
		err:     nil,
		payload: payloadBytes,
	}
}

const requestIDHeader = "x-request-id"

// parse returns parsed body data from API response
// It extracts the business-layer error code (result.Code), message (result.Msg),
// and data field from the API response JSON
// Note: The Code field is business-logic error code (e.g., 200 = success, 1001 = permission denied),
// NOT the HTTP status code (which is in res.StatusCode)
func (s *Sender) parse(body []byte, requestID string) *Result {
	var result struct {
		Code int    `json:"code"` // Business error code
		Msg  string `json:"msg"`  // Business error message
		Data any    `json:"data"` // Business response data
	}

	// unmarshal body
	if err := s.client.unmarshal(body, &result); err != nil {
		return &Result{
			client:    s.client,
			Err:       err,
			RequestID: requestID,
		}
	}

	// Remarshal data part
	dataBody, err := s.client.marshal(result.Data)
	if err != nil {
		return &Result{
			client:    s.client,
			Err:       err,
			RequestID: requestID,
		}
	}

	// Return full result
	return &Result{
		client:    s.client,
		Code:      result.Code,
		Msg:       result.Msg,
		Body:      dataBody,
		RequestID: requestID,
	}
}

// setupAuthHeader sets authorisation header based on auth type
func (s *Sender) setupAuthHeader(req *http.Request, authType AuthType) {
	switch authType {
	case AuthTypeToken:
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", s.client.token))
	case AuthTypeKey:
		credentials := strings.Join([]string{s.client.secretID, s.client.secretKey}, ":")
		encoded := base64.StdEncoding.EncodeToString([]byte(credentials))
		req.Header.Set("Authorization", strings.Join([]string{"Basic", encoded}, " "))
	}
	req.Header.Set("User-Agent", openapi.UserAgent)
}

// newRequest returns a clone of the base request with a fresh body when possible
func (s *Sender) newRequest(attempt int) (*http.Request, error) {
	req := s.request.Clone(s.request.Context())
	if s.request.GetBody == nil {
		if len(s.payload) > 0 {
			req.Body = io.NopCloser(bytes.NewReader(s.payload))
			req.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(s.payload)), nil
			}
			return req, nil
		}
		if s.request.Body != nil && attempt > 0 {
			return nil, fmt.Errorf("request body is not rewindable; retries are not possible")
		}
		return req, nil
	}
	body, err := s.request.GetBody()
	if err != nil {
		return nil, err
	}
	req.Body = body
	return req, nil
}

// doRequest executes the common request retry logic
func (s *Sender) doRequest(authType AuthType, authTypeStr string) *Result {
	// Handle error
	if s.err != nil {
		return &Result{
			client: s.client,
			Err:    s.err,
		}
	}

	// Copy retry delay
	retryDelay := s.client.retryDelay
	var lastRequestID string

	for attempt := 0; attempt < s.client.maxRetries; attempt++ {
		if result := func() *Result {
			// Construct client
			client := &http.Client{
				Timeout: time.Duration(s.client.timeout) * time.Second,
			}

			req, err := s.newRequest(attempt)
			if err != nil {
				return &Result{
					client: s.client,
					Err:    err,
				}
			}

			// Add headers
			s.setupAuthHeader(req, authType)

			// Send request
			s.client.Logger.Debug(nil, fmt.Sprintf(
				"send request to %s, method %s with %s (attempt %d)", req.URL, req.Method, authTypeStr, attempt+1,
			))
			res, err := client.Do(req)
			if err != nil {
				s.client.Logger.Debug(nil, fmt.Sprintf("request failed: %v, retrying...", err))
				return nil // Retry on network errors
			}
			defer func(Body io.ReadCloser) {
				_ = Body.Close()
			}(res.Body)

			requestID := res.Header.Get(requestIDHeader)
			if requestID != "" {
				lastRequestID = requestID
			}

			// Handle HTTP status code error (support 2xx range)
			if res.StatusCode < 200 || res.StatusCode >= 300 {
				s.client.Logger.Debug(nil, fmt.Sprintf(
					"received HTTP status %d, requestID %s, retrying...",
					res.StatusCode, requestID,
				))
				return nil // Retry on non-2xx status codes
			}

			// Get request result
			body, err := io.ReadAll(res.Body)
			if err != nil {
				s.client.Logger.Debug(nil, fmt.Sprintf(
					"failed to read response body: %v, requestID %s, retrying...",
					err, requestID,
				))
				return nil // Retry on body read errors
			}

			// Parse result
			parsed := s.parse(body, requestID)

			// Output log
			var bodyRaw any
			if err = s.client.unmarshal(body, &bodyRaw); err != nil {
				s.client.Logger.Debug(nil, fmt.Sprintf(
					"failed to unmarshal response body: %v, requestID %s, retrying...",
					err, requestID,
				))
				return nil // Retry on unmarshal errors
			}
			s.client.Logger.Debug(nil, fmt.Sprintf(
				"openAPI response httpCode %d, apiCode %d, requestID %s, responseBody %s",
				res.StatusCode, parsed.Code, requestID, fmt.Sprint(bodyRaw),
			))

			// Check failed reason based on business error code
			// Note: parsed.Code is business-layer error code (not HTTP status code)
			// 1001 = permission denied (possibly token expired)
			if parsed.Code == 1001 {
				if authType == AuthTypeToken {
					s.client.Logger.Debug(nil, fmt.Sprintf(
						"permission denied, maybe token expired, try to renew, requestID %s",
						requestID,
					))

					// Sleep to prevent too many requests
					time.Sleep(time.Duration(retryDelay) * time.Second)

					if s.client.exponentialBackoff {
						retryDelay = capRetryDelay(retryDelay * 2)
					}

					if err = applyToken(s.client); err != nil {
						return &Result{
							client: s.client,
							Err:    err,
						}
					}

					return nil // Retry after token renewal
				}
				// For key auth, just log and retry
				s.client.Logger.Debug(nil, fmt.Sprintf("permission denied, requestID %s", requestID))

				// Sleep to prevent too many requests
				time.Sleep(time.Duration(retryDelay) * time.Second)

				if s.client.exponentialBackoff {
					retryDelay = capRetryDelay(retryDelay * 2)
				}

				return nil // Retry
			}

			// Return parsed result
			return parsed
		}(); result != nil {
			return result
		}

		// Wait before retrying
		if attempt < s.client.maxRetries-1 {
			if lastRequestID != "" {
				s.client.Logger.Debug(nil, fmt.Sprintf(
					"retrying in %d seconds..., requestID %s",
					retryDelay, lastRequestID,
				))
			} else {
				s.client.Logger.Debug(nil, fmt.Sprintf("retrying in %d seconds...", retryDelay))
			}

			time.Sleep(time.Duration(retryDelay) * time.Second)

			if s.client.exponentialBackoff {
				retryDelay = capRetryDelay(retryDelay * 2)
			}
		}
	}

	// If all retries failed, return an error
	return &Result{
		client: s.client,
		Err:    fmt.Errorf("request failed after %d retries", s.client.maxRetries),
	}
}

// capRetryDelay limits the retry delay to a maximum value (MaxRetryDelaySeconds)
// This prevents the exponential backoff from causing unreasonably long wait times
// For example: 1s -> 2s -> 4s -> 8s -> 16s -> 32s -> 60s -> 60s -> ...
func capRetryDelay(delay int) int {
	if delay > MaxRetryDelaySeconds {
		return MaxRetryDelaySeconds
	}
	return delay
}

// WithToken sends a request with token to authorise
func (s *Sender) WithToken() *Result {
	return s.doRequest(AuthTypeToken, "token")
}

// WithKey sends a request with SecretID and SecretKey to authorise
func (s *Sender) WithKey() *Result {
	return s.doRequest(AuthTypeKey, "key")
}

// OK returns whether the business-layer API call was successful
// It checks if Code (business error code) == 200, which indicates success
// This is different from HTTP status code (res.StatusCode), which is checked separately
func (r *Result) OK() bool {
	return r.Code == 200
}

// Unmarshal can unmarshal a request data body to customised struct
func (r *Result) Unmarshal(v any) error {
	return r.client.unmarshal(r.Body, v)
}
