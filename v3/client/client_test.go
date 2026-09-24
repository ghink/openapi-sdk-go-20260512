package client

import (
	"encoding/json"
	"errors"
	"testing"

	sdkerror "go.gh.ink/openapi/sdk/20260512/v3/errors"
)

func TestWithTimeout(t *testing.T) {
	c := &Client{}
	WithTimeout(10)(c)
	if c.timeout != 10 {
		t.Errorf("expected timeout 10, got %d", c.timeout)
	}
}

func TestWithMaxRetries_Zero(t *testing.T) {
	c := &Client{}
	WithMaxRetries(0)(c)
	if c.maxRetries != DefaultMaxRetries {
		t.Errorf("expected DefaultMaxRetries when 0, got %d", c.maxRetries)
	}
}

func TestWithMaxRetries_Negative(t *testing.T) {
	c := &Client{}
	WithMaxRetries(-1)(c)
	if c.maxRetries != DefaultMaxRetries {
		t.Errorf("expected DefaultMaxRetries when negative, got %d", c.maxRetries)
	}
}

func TestWithMaxRetries_Positive(t *testing.T) {
	c := &Client{}
	WithMaxRetries(3)(c)
	if c.maxRetries != 3 {
		t.Errorf("expected 3, got %d", c.maxRetries)
	}
}

func TestWithRetryDelay_Zero(t *testing.T) {
	c := &Client{}
	WithRetryDelay(0)(c)
	if c.retryDelay != DefaultRetryDelaySeconds {
		t.Errorf("expected DefaultRetryDelaySeconds when 0, got %d", c.retryDelay)
	}
}

func TestWithRetryDelay_Negative(t *testing.T) {
	c := &Client{}
	WithRetryDelay(-1)(c)
	if c.retryDelay != DefaultRetryDelaySeconds {
		t.Errorf("expected DefaultRetryDelaySeconds when negative, got %d", c.retryDelay)
	}
}

func TestWithRetryDelay_Positive(t *testing.T) {
	c := &Client{}
	WithRetryDelay(5)(c)
	if c.retryDelay != 5 {
		t.Errorf("expected 5, got %d", c.retryDelay)
	}
}

func TestWithExponentialBackoff(t *testing.T) {
	c := &Client{exponentialBackoff: true}
	WithExponentialBackoff(false)(c)
	if c.exponentialBackoff != false {
		t.Errorf("expected false, got %v", c.exponentialBackoff)
	}

	WithExponentialBackoff(true)(c)
	if c.exponentialBackoff != true {
		t.Errorf("expected true, got %v", c.exponentialBackoff)
	}
}

func TestEnableToken(t *testing.T) {
	c := &Client{enableToken: true}
	EnableToken(false)(c)
	if c.enableToken != false {
		t.Errorf("expected false, got %v", c.enableToken)
	}
}

func TestWithMarshal(t *testing.T) {
	c := &Client{}
	customMarshal := func(v any) ([]byte, error) { return []byte("test"), nil }
	WithMarshal(customMarshal)(c)
	if c.marshal == nil {
		t.Error("expected non-nil marshal")
	}
}

func TestWithUnmarshal(t *testing.T) {
	c := &Client{}
	customUnmarshal := func(data []byte, v any) error { return nil }
	WithUnmarshal(customUnmarshal)(c)
	if c.unmarshal == nil {
		t.Error("expected non-nil unmarshal")
	}
}

func TestWithLogger(t *testing.T) {
	c := &Client{}
	logger := NewLogger()
	WithLogger(logger)(c)
	if c.Logger == nil {
		t.Error("expected non-nil logger")
	}
}

func TestResult_OK(t *testing.T) {
	tests := []struct {
		code int
		ok   bool
	}{
		{200, true},
		{0, false},
		{404, false},
		{500, false},
		{1001, false},
	}

	for _, tt := range tests {
		r := &Result{Code: tt.code}
		if r.OK() != tt.ok {
			t.Errorf("Result{Code: %d}.OK() = %v, want %v", tt.code, r.OK(), tt.ok)
		}
	}
}

func TestResult_Unmarshal(t *testing.T) {
	body := []byte(`{"key":"value"}`)
	c := &Client{unmarshal: json.Unmarshal}
	r := &Result{
		client: c,
		Body:   body,
	}

	var target struct {
		Key string `json:"key"`
	}
	if err := r.Unmarshal(&target); err != nil {
		t.Errorf("unmarshal failed: %v", err)
	}
	if target.Key != "value" {
		t.Errorf("expected 'value', got '%s'", target.Key)
	}
}

func TestSend_MarshalError(t *testing.T) {
	c := &Client{
		marshal: func(v any) ([]byte, error) {
			return nil, errors.New("marshal error")
		},
	}

	s := c.Send("http://example.com", "GET", map[string]string{"a": "b"})
	if s.err == nil {
		t.Error("expected marshal error in sender")
	}
}

func TestResult_WithSdkError(t *testing.T) {
	r := &Result{Code: 500, Msg: "server error"}
	err := sdkerror.ErrRequestSendFailed.
		WithApiCode(r.Code).
		WithApiMessage(r.Msg).
		WithResponse(r)

	if err.ApiCode() != 500 {
		t.Errorf("expected apiCode 500, got %d", err.ApiCode())
	}
	if err.ApiMessage() != "server error" {
		t.Errorf("expected apiMessage 'server error', got '%s'", err.ApiMessage())
	}
	if !errors.Is(err, sdkerror.ErrRequestSendFailed) {
		t.Error("err should be ErrRequestSendFailed")
	}
}
