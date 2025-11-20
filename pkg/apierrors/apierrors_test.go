package apierrors

import (
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
)

func TestHTTPStatus(t *testing.T) {
	cases := map[Code]int{
		CodeInvalidArgument: 400,
		CodeRetryLater:      429,
		CodeUnlockRequired:  503,
		CodeInvalidKey:      404,
		Code("UNKNOWN"):     500,
	}

	for code, want := range cases {
		if got := HTTPStatus(code); got != want {
			t.Fatalf("HTTPStatus(%s)=%d, want %d", code, got, want)
		}
	}
}

func TestGRPCStatus(t *testing.T) {
	cases := map[Code]codes.Code{
		CodeInvalidArgument: codes.InvalidArgument,
		CodeRetryLater:      codes.ResourceExhausted,
		CodeUnlockRequired:  codes.Unavailable,
		CodeInvalidKey:      codes.NotFound,
		Code("UNKNOWN"):     codes.Internal,
	}

	for code, want := range cases {
		if got := GRPCStatus(code); got != want {
			t.Fatalf("GRPCStatus(%s)=%s, want %s", code, got, want)
		}
	}
}

func TestRequiresRetryAfter(t *testing.T) {
	if !RequiresRetryAfter(CodeRetryLater) {
		t.Fatal("RetryLater should require header")
	}
	if !RequiresRetryAfter(CodeUnlockRequired) {
		t.Fatal("UnlockRequired should require header")
	}
	if RequiresRetryAfter(CodeInvalidArgument) {
		t.Fatal("InvalidArgument should not require header")
	}
}

func TestErrorRetryAfterHint(t *testing.T) {
	err := New(CodeRetryLater, "slow down").WithRetryAfter(1500 * time.Millisecond)
	if hint := err.RetryAfterHint(); hint != "2" {
		t.Fatalf("expected retryAfter 2, got %q", hint)
	}
	if err.Error() != "slow down" {
		t.Fatalf("unexpected Error(): %s", err.Error())
	}
	if hint := New(CodeRetryLater, "").RetryAfterHint(); hint != "" {
		t.Fatalf("expected empty hint, got %q", hint)
	}
}

func TestFromError(t *testing.T) {
	original := New(CodeInvalidKey, "unknown key")
	wrapped := fmt.Errorf("wrap: %w", original)
	if apiErr, ok := FromError(wrapped); !ok {
		t.Fatal("expected to unwrap api error")
	} else if apiErr.Code != CodeInvalidKey {
		t.Fatalf("unexpected code %s", apiErr.Code)
	}
	if _, ok := FromError(fmt.Errorf("other")); ok {
		t.Fatal("should not unwrap plain error")
	}
}
