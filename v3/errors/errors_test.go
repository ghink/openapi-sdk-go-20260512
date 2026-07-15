package errors

import (
	"errors"
	"testing"
)

func TestNew(t *testing.T) {
	err := New("test error")
	if err.Error() != "test error" {
		t.Errorf("expected 'test error', got '%s'", err.Error())
	}
	if err.ApiCode() != 0 {
		t.Errorf("expected apiCode 0, got %d", err.ApiCode())
	}
	if err.ApiMessage() != "" {
		t.Errorf("expected empty apiMessage, got '%s'", err.ApiMessage())
	}
	if err.RequestID() != "" {
		t.Errorf("expected empty requestID, got '%s'", err.RequestID())
	}
	if err.Response() != nil {
		t.Errorf("expected nil response, got %v", err.Response())
	}
	if err.Unwrap() != nil {
		t.Errorf("expected nil raw for sentinel, got %v", err.Unwrap())
	}
}

func TestNewWithOptions(t *testing.T) {
	err := New("test",
		WithApiCode(500),
		WithApiMessage("server error"),
		WithRequestID("req-123"),
		WithResponse(map[string]string{"key": "val"}),
	)

	if err.ApiCode() != 500 {
		t.Errorf("expected apiCode 500, got %d", err.ApiCode())
	}
	if err.ApiMessage() != "server error" {
		t.Errorf("expected apiMessage 'server error', got '%s'", err.ApiMessage())
	}
	if err.RequestID() != "req-123" {
		t.Errorf("expected requestID 'req-123', got '%s'", err.RequestID())
	}
	if err.Response() == nil {
		t.Error("expected non-nil response")
	}
}

func TestSdkError_Unwrap(t *testing.T) {
	sentinel := New("sentinel")
	if sentinel.Unwrap() != nil {
		t.Error("sentinel's Unwrap() should return nil")
	}

	derived := sentinel.WithApiCode(1001)
	if derived.Unwrap() != sentinel {
		t.Error("derived's Unwrap() should return sentinel")
	}
}

func TestErrorsIs_Sentinel(t *testing.T) {
	if !errors.Is(ErrRequestSendFailed, ErrRequestSendFailed) {
		t.Error("sentinel should match itself via errors.Is")
	}
	if errors.Is(ErrRequestSendFailed, ErrTokenAcquisitionFailed) {
		t.Error("different sentinels should not match")
	}
}

func TestErrorsIs_Derived(t *testing.T) {
	sentinel := ErrRequestSendFailed

	derived := sentinel.WithApiCode(1001)
	if !errors.Is(derived, sentinel) {
		t.Error("derived (single With*) should match sentinel")
	}

	chained := sentinel.WithApiCode(1001).WithApiMessage("boom").WithRequestID("r1")
	if !errors.Is(chained, sentinel) {
		t.Error("derived (chained With*) should match sentinel")
	}

	if errors.Is(derived, ErrTokenAcquisitionFailed) {
		t.Error("derived should not match a different sentinel")
	}
}

func TestErrorsIs_DerivedFromDerived(t *testing.T) {
	sentinel := ErrRequestSendFailed
	first := sentinel.WithApiCode(1001)
	second := first.WithRequestID("req-123")

	if !errors.Is(second, sentinel) {
		t.Error("second-derivation should still match root sentinel")
	}
}

func TestErrorsIs_NoInfiniteLoop(t *testing.T) {
	sentinel := ErrRequestSendFailed
	unrelated := New("unrelated")
	if errors.Is(sentinel, unrelated) {
		t.Error("sentinel should not match unrelated error")
	}
}

func TestWithMethods_DoNotMutateOriginal(t *testing.T) {
	original := New("original")

	_ = original.WithApiCode(404)
	if original.ApiCode() != 0 {
		t.Error("WithApiCode should not mutate original")
	}

	_ = original.WithApiMessage("msg")
	if original.ApiMessage() != "" {
		t.Error("WithApiMessage should not mutate original")
	}

	_ = original.WithRequestID("rid")
	if original.RequestID() != "" {
		t.Error("WithRequestID should not mutate original")
	}

	_ = original.WithResponse("resp")
	if original.Response() != nil {
		t.Error("WithResponse should not mutate original")
	}
}

func TestSdkError_Error(t *testing.T) {
	err := New("custom message")
	if err.Error() != "custom message" {
		t.Errorf("expected 'custom message', got '%s'", err.Error())
	}

	derived := err.WithApiCode(500).WithApiMessage("api msg")
	if derived.Error() != "custom message" {
		t.Errorf("Error() should still return original message, got '%s'", derived.Error())
	}
}

func TestPredefinedErrors(t *testing.T) {
	tests := []struct {
		err     *SdkError
		message string
	}{
		{ErrTokenAcquisitionFailed, "failed to acquire authentication token"},
		{ErrTokenUnmarshalFailed, "failed to unmarshal token response"},
		{ErrRequestSendFailed, "failed to send request"},
		{ErrResponseUnmarshalFailed, "failed to unmarshal response body"},
		{ErrStatusError, "open api service returned status code"},
	}

	for _, tt := range tests {
		if tt.err.Error() != tt.message {
			t.Errorf("expected '%s', got '%s'", tt.message, tt.err.Error())
		}
		if tt.err.Unwrap() != nil {
			t.Errorf("%s should have nil raw", tt.message)
		}
	}
}

func TestErrorsAs_SdkError(t *testing.T) {
	var sdkErr *SdkError
	err := error(ErrRequestSendFailed.WithApiCode(500))
	if !errors.As(err, &sdkErr) {
		t.Error("errors.As should succeed for *SdkError")
	}
	if sdkErr.ApiCode() != 500 {
		t.Errorf("expected apiCode 500, got %d", sdkErr.ApiCode())
	}
}

func TestClone_PreservesRawOnChainedCalls(t *testing.T) {
	sentinel := ErrRequestSendFailed
	a := sentinel.WithApiCode(1)
	b := a.WithApiCode(2)
	c := b.WithApiCode(3)

	if !errors.Is(c, sentinel) {
		t.Error("c should match sentinel")
	}
	if !errors.Is(b, sentinel) {
		t.Error("b should match sentinel")
	}
	if !errors.Is(a, sentinel) {
		t.Error("a should match sentinel")
	}
}
