package signerapi

import (
	"context"
	"io"
	"testing"
	"time"

	signerv1 "github.com/aegis-sign/wallet/docs/api/gen/go"
	"github.com/aegis-sign/wallet/internal/app/backend/keycache"
	"github.com/aegis-sign/wallet/pkg/apierrors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCSignValidatesDigest(t *testing.T) {
	server := NewGRPCServer(&stubBackend{}, nil)
	_, err := server.Sign(context.Background(), &signerv1.SignRequest{Digest: []byte{1}})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument, got %v", status.Code(err))
	}
}

func TestGRPCSignInvalidKey(t *testing.T) {
	server := NewGRPCServer(&stubBackend{
		signFn: func(ctx context.Context, req *signerv1.SignRequest) (*signerv1.SignResponse, error) {
			return nil, apierrors.New(apierrors.CodeInvalidKey, "unknown key")
		},
	}, nil)
	_, err := server.Sign(context.Background(), &signerv1.SignRequest{Digest: repeatBytes(0x01, 32)})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected not found, got %v", status.Code(err))
	}
}

func TestGRPCSignStream(t *testing.T) {
	backend := &stubBackend{
		signFn: func(ctx context.Context, req *signerv1.SignRequest) (*signerv1.SignResponse, error) {
			return &signerv1.SignResponse{Signature: req.GetDigest()}, nil
		},
	}
	server := NewGRPCServer(backend, nil)
	stream := &fakeSignStream{
		ctx: context.Background(),
		reqs: []*signerv1.SignRequest{
			{Digest: repeatBytes(0x01, 32), KeyId: "k1"},
			{Digest: repeatBytes(0x02, 32), KeyId: "k2"},
		},
	}
	if err := server.SignStream(stream); err != nil {
		t.Fatalf("sign stream failed: %v", err)
	}
	if len(stream.sent) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(stream.sent))
	}
	if !equalBytes(stream.sent[0].GetSignature(), repeatBytes(0x01, 32)) {
		t.Fatalf("unexpected response payload")
	}
}

func TestGRPCSignUnlockQueuesEvent(t *testing.T) {
	queue := &testUnlockQueue{}
	responder := NewUnlockResponder(UnlockResponderConfig{
		Queue:    queue,
		Keyspace: "prod",
		MinRetry: 50 * time.Millisecond,
		MaxRetry: 50 * time.Millisecond,
	})
	server := NewGRPCServer(&stubBackend{
		signFn: func(context.Context, *signerv1.SignRequest) (*signerv1.SignResponse, error) {
			return nil, keycache.NewUnlockRequiredError("dek", 0)
		},
	}, responder)
	_, err := server.Sign(context.Background(), &signerv1.SignRequest{Digest: repeatBytes(0x01, 32), KeyId: "k-grpc"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected unavailable, got %v", status.Code(err))
	}
	if queue.lastEvent.KeyID != "k-grpc" {
		t.Fatalf("expected key recorded, got %s", queue.lastEvent.KeyID)
	}
}

type testUnlockQueue struct {
	lastEvent keycache.UnlockEvent
}

func (t *testUnlockQueue) NotifyUnlock(ctx context.Context, event keycache.UnlockEvent) error {
	t.lastEvent = event
	return nil
}

type fakeSignStream struct {
	signerv1.SignerService_SignStreamServer
	ctx  context.Context
	reqs []*signerv1.SignRequest
	sent []*signerv1.SignResponse
	idx  int
}

func (f *fakeSignStream) Context() context.Context { return f.ctx }

func (f *fakeSignStream) Recv() (*signerv1.SignRequest, error) {
	if f.idx >= len(f.reqs) {
		return nil, io.EOF
	}
	req := f.reqs[f.idx]
	f.idx++
	return req, nil
}

func (f *fakeSignStream) Send(resp *signerv1.SignResponse) error {
	f.sent = append(f.sent, resp)
	return nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func repeatBytes(b byte, n int) []byte {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = b
	}
	return buf
}
