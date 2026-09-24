package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// nullLogger keeps resty and SDK debug output out of test logs.
type nullLogger struct{}

func (nullLogger) Debug(context.Context, ...any) {}
func (nullLogger) Info(context.Context, ...any)  {}
func (nullLogger) Warn(context.Context, ...any)  {}
func (nullLogger) Error(context.Context, ...any) {}

func TestClassify(t *testing.T) {
	// The business codes the SDK treats specially. Every other code is a
	// permanent rejection the caller has to fix something for.
	tests := []struct {
		name       string
		code       int
		idempotent bool
		want       action
	}{
		{"success", codeSuccess, true, actionDone},
		{"success writes", codeSuccess, false, actionDone},
		{"throttled reads", codeThrottled, true, actionRetry},
		// 429 is decided by the permission middleware before quota is taken,
		// so replaying it cannot duplicate anything
		{"throttled writes", codeThrottled, false, actionRetry},
		{"server error reads", codeServerError, true, actionRetry},
		// a failed write may already have reached the upstream
		{"server error writes", codeServerError, false, actionDone},
		{"upstream down reads", codeUpstreamUnavailable, true, actionRetry},
		{"upstream down writes", codeUpstreamUnavailable, false, actionDone},
		{"permission denied", codePermissionDenied, true, actionRenewToken},
		{"permission denied writes", codePermissionDenied, false, actionRenewToken},
		{"no quota", 1002, true, actionDone},
		{"application gone", 1003, true, actionDone},
		{"missing parameter", 2001, true, actionDone},
		{"invalid link uri", 2002, true, actionDone},
		{"invalid validity", 2003, true, actionDone},
		{"geo not found", 4001, true, actionDone},
		{"unknown code", 9999, true, actionDone},
		{"zero code", 0, true, actionDone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classify(tt.code, tt.idempotent); got != tt.want {
				t.Errorf("classify(%d, idempotent=%v) = %v, want %v", tt.code, tt.idempotent, got, tt.want)
			}
		})
	}
}

func TestIs2xx(t *testing.T) {
	for status, want := range map[int]bool{
		0:                          false,
		199:                        false,
		http.StatusOK:              true,
		201:                        true,
		299:                        true,
		http.StatusMultipleChoices: false,
		429:                        false,
		500:                        false,
	} {
		if got := is2xx(status); got != want {
			t.Errorf("is2xx(%d) = %v, want %v", status, got, want)
		}
	}
}

func TestAuthTypeString(t *testing.T) {
	if got := AuthTypeToken.String(); got != "token" {
		t.Errorf("AuthTypeToken.String() = %q", got)
	}
	if got := AuthTypeKey.String(); got != "key" {
		t.Errorf("AuthTypeKey.String() = %q", got)
	}
	if got := AuthType(7).String(); got != "unknown" {
		t.Errorf("AuthType(7).String() = %q", got)
	}
}

// A POST is the only thing that can duplicate work downstream, so nothing about
// it may be retried by the transport layer.
func TestIdempotentOnlyForGet(t *testing.T) {
	if !(&Sender{method: "GET"}).idempotent() {
		t.Error("GET should be idempotent")
	}
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE", ""} {
		if (&Sender{method: m}).idempotent() {
			t.Errorf("%q should not be idempotent", m)
		}
	}
}

// An envelope whose data field is absent leaves Body empty and decodes into
// nothing rather than inventing a payload.
func TestUnmarshalWithoutData(t *testing.T) {
	r := &Result{client: &Client{unmarshal: json.Unmarshal}}

	var out map[string]any
	if err := r.Unmarshal(&out); err != nil {
		t.Fatalf("Unmarshal on empty Body = %v, want nil", err)
	}
	if out != nil {
		t.Errorf("decoded into %v, want nothing", out)
	}
}

func TestUnmarshalNullData(t *testing.T) {
	// the API sends "data":null for the codes that carry no payload
	r := &Result{client: &Client{unmarshal: json.Unmarshal}, Body: []byte("null")}

	var out struct {
		LinkID string `json:"link_id"`
	}
	if err := r.Unmarshal(&out); err != nil {
		t.Fatalf("Unmarshal(null) = %v, want nil", err)
	}
	if out.LinkID != "" {
		t.Errorf("LinkID = %q, want empty", out.LinkID)
	}
}

func TestResultOKOnlyForSuccess(t *testing.T) {
	for _, code := range []int{codeSuccess} {
		if !(&Result{Code: code}).OK() {
			t.Errorf("Result{Code: %d}.OK() = false, want true", code)
		}
	}
	for _, code := range []int{0, 201, 404, codeThrottled, codeServerError, codePermissionDenied} {
		if (&Result{Code: code}).OK() {
			t.Errorf("Result{Code: %d}.OK() = true, want false", code)
		}
	}
}
