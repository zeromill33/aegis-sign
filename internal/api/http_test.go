package signerapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	signerv1 "github.com/aegis-sign/wallet/docs/api/gen/go"
	"github.com/aegis-sign/wallet/pkg/apierrors"
)

func TestHandleSignSuccess(t *testing.T) {
	digest := strings.Repeat("a", 64)
	handler := NewHTTPHandler(&stubBackend{
		signFn: func(_ context.Context, req *signerv1.SignRequest) (*signerv1.SignResponse, error) {
			if len(req.GetDigest()) != 32 {
				t.Fatalf("digest len=%d", len(req.GetDigest()))
			}
			return &signerv1.SignResponse{Signature: []byte{0x01, 0x02}, RecId: 7}, nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/sign", strings.NewReader(`{"keyId":"k1","digest":"`+digest+`","encoding":"hex"}`))
	rr := httptest.NewRecorder()
	handler.handleSign(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	var body signResponseBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Signature != hex.EncodeToString([]byte{0x01, 0x02}) {
		t.Fatalf("unexpected signature %s", body.Signature)
	}
	if body.RecID == nil || *body.RecID != 7 {
		t.Fatalf("expected recId=7, got %v", body.RecID)
	}
}

func TestHandleSignInvalidDigest(t *testing.T) {
	handler := NewHTTPHandler(&stubBackend{})
	req := httptest.NewRequest(http.MethodPost, "/sign", strings.NewReader(`{"keyId":"k1","digest":"zzz"}`))
	rr := httptest.NewRecorder()
	handler.handleSign(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rr.Code)
	}
	var body errorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Code != string(apierrors.CodeInvalidArgument) {
		t.Fatalf("unexpected code %s", body.Code)
	}
}

func TestHandleSignInvalidKey(t *testing.T) {
	handler := NewHTTPHandler(&stubBackend{
		signFn: func(_ context.Context, req *signerv1.SignRequest) (*signerv1.SignResponse, error) {
			return nil, apierrors.New(apierrors.CodeInvalidKey, "unknown key")
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/sign", strings.NewReader(`{"keyId":"k1","digest":"`+strings.Repeat("a", 64)+`"}`))
	rr := httptest.NewRecorder()
	handler.handleSign(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rr.Code)
	}
	var body errorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Code != string(apierrors.CodeInvalidKey) {
		t.Fatalf("unexpected code %s", body.Code)
	}
}

func TestCreateFastPathBudget(t *testing.T) {
	handler := NewHTTPHandler(&stubBackend{
		createFn: func(_ context.Context, req *signerv1.CreateRequest) (*signerv1.CreateResponse, error) {
			return &signerv1.CreateResponse{
				KeyId:     "plainkey-01HZYQTB6",
				PublicKey: bytesRepeat(0x01, 33),
				Address:   "0x1234",
			}, nil
		},
	})
	warmReq := httptest.NewRequest(http.MethodPost, "/create", strings.NewReader(`{}`))
	handler.handleCreate(httptest.NewRecorder(), warmReq)
	req := httptest.NewRequest(http.MethodPost, "/create", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	start := time.Now()
	handler.handleCreate(rr, req)
	dur := time.Since(start)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	if dur > 5*time.Millisecond {
		t.Fatalf("/create handler exceeded 5ms budget: %s", dur)
	}
}

func bytesRepeat(b byte, n int) []byte {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = b
	}
	return buf
}

type stubBackend struct {
	createFn func(context.Context, *signerv1.CreateRequest) (*signerv1.CreateResponse, error)
	signFn   func(context.Context, *signerv1.SignRequest) (*signerv1.SignResponse, error)
}

func (s *stubBackend) Create(ctx context.Context, req *signerv1.CreateRequest) (*signerv1.CreateResponse, error) {
	if s.createFn == nil {
		return &signerv1.CreateResponse{}, nil
	}
	return s.createFn(ctx, req)
}

func (s *stubBackend) Sign(ctx context.Context, req *signerv1.SignRequest) (*signerv1.SignResponse, error) {
	if s.signFn == nil {
		return &signerv1.SignResponse{}, nil
	}
	return s.signFn(ctx, req)
}
