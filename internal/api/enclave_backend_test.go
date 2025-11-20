package signerapi

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	signerv1 "github.com/aegis-sign/wallet/docs/api/gen/go"
	"github.com/aegis-sign/wallet/internal/infra/enclaveclient"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const testBufSize = 1024 * 1024

type streamingServer struct {
	signerv1.UnimplementedSignerServiceServer
}

func (streamingServer) SignStream(stream signerv1.SignerService_SignStreamServer) error {
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&signerv1.SignResponse{Signature: append([]byte{}, req.GetDigest()...)}); err != nil {
			return err
		}
	}
}

func (streamingServer) Create(context.Context, *signerv1.CreateRequest) (*signerv1.CreateResponse, error) {
	return &signerv1.CreateResponse{KeyId: "generated"}, nil
}

func newTestPool(t *testing.T) (*enclaveclient.Pool, *grpc.Server, *bufconn.Listener) {
	t.Helper()
	lis := bufconn.Listen(testBufSize)
	srv := grpc.NewServer()
	signerv1.RegisterSignerServiceServer(srv, streamingServer{})
	go func() { _ = srv.Serve(lis) }()
	cfg := enclaveclient.DefaultConfig()
	cfg.MinConns = 1
	cfg.MaxConns = 1
	cfg.HealthCheckInterval = 200 * time.Millisecond
	pool, err := enclaveclient.NewPool(cfg,
		enclaveclient.WithRegisterer(prometheus.NewRegistry()),
		enclaveclient.WithDialer(func(ctx context.Context, target enclaveclient.Target, _ enclaveclient.Config) (*grpc.ClientConn, error) {
			return grpc.DialContext(ctx, target.Endpoint,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
			)
		}))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = pool.Close()
		srv.Stop()
	})
	pool.RegisterTarget(enclaveclient.Target{ID: "enclave-1", Endpoint: "buf"})
	return pool, srv, lis
}

func TestEnclaveBackendSign(t *testing.T) {
	pool, _, _ := newTestPool(t)
	backend, err := NewEnclaveBackend(pool, StaticTargetSelector{TargetID: "enclave-1"})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req := &signerv1.SignRequest{KeyId: "k1", Digest: []byte("payload")}
	resp, err := backend.Sign(ctx, req)
	require.NoError(t, err)
	require.Equal(t, []byte("payload"), resp.GetSignature())
}

func TestEnclaveBackendCreate(t *testing.T) {
	pool, _, _ := newTestPool(t)
	backend, err := NewEnclaveBackend(pool, StaticTargetSelector{TargetID: "enclave-1"}, WithCallTimeout(500*time.Millisecond))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp, err := backend.Create(ctx, &signerv1.CreateRequest{})
	require.NoError(t, err)
	require.Equal(t, "generated", resp.GetKeyId())
}

func TestStickySelector(t *testing.T) {
	selector, err := NewStickySelector([]string{"a", "b"})
	require.NoError(t, err)
	t.Run("create rotates", func(t *testing.T) {
		first, _ := selector.SelectForCreate(context.Background(), &signerv1.CreateRequest{})
		second, _ := selector.SelectForCreate(context.Background(), &signerv1.CreateRequest{})
		if first == second {
			t.Fatalf("expected round robin across targets")
		}
	})
	t.Run("sign sticky", func(t *testing.T) {
		req := &signerv1.SignRequest{KeyId: "hot-key"}
		target1, _ := selector.SelectForSign(context.Background(), req)
		target2, _ := selector.SelectForSign(context.Background(), req)
		if target1 != target2 {
			t.Fatalf("expected sticky mapping, got %s vs %s", target1, target2)
		}
	})
}
