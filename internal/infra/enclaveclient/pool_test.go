package enclaveclient

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	signerv1 "github.com/aegis-sign/wallet/docs/api/gen/go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

type mockSignerServer struct {
	signerv1.UnimplementedSignerServiceServer
}

func (m mockSignerServer) Create(context.Context, *signerv1.CreateRequest) (*signerv1.CreateResponse, error) {
	return &signerv1.CreateResponse{KeyId: "k1"}, nil
}

func (m mockSignerServer) Sign(context.Context, *signerv1.SignRequest) (*signerv1.SignResponse, error) {
	return &signerv1.SignResponse{Signature: []byte("sig")}, nil
}

func setupBufConn(t *testing.T) (*grpc.Server, *bufconn.Listener) {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	signerv1.RegisterSignerServiceServer(srv, mockSignerServer{})
	go func() {
		_ = srv.Serve(lis)
	}()
	return srv, lis
}

func TestPoolAcquireAndRelease(t *testing.T) {
	srv, lis := setupBufConn(t)
	t.Cleanup(srv.Stop)
	cfg := DefaultConfig()
	cfg.MinConns = 1
	cfg.MaxConns = 2
	cfg.HealthCheckInterval = 50 * time.Millisecond
	cfg.AcquireTimeout = 200 * time.Millisecond
	pool, err := NewPool(cfg,
		WithRegisterer(prometheus.NewRegistry()),
		WithDialer(func(ctx context.Context, target Target, _ Config) (*grpc.ClientConn, error) {
			return grpc.DialContext(ctx, target.Endpoint,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
			)
		}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	pool.RegisterTarget(Target{ID: "enclave-a", Endpoint: "buf"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lease, err := pool.Acquire(ctx, "enclave-a")
	require.NoError(t, err)
	require.NotNil(t, lease.Conn())
	client := lease.Client()
	resp, err := client.Sign(ctx, &signerv1.SignRequest{KeyId: "k1", Digest: make([]byte, 32)})
	require.NoError(t, err)
	require.Equal(t, "sig", string(resp.GetSignature()))
	lease.Release(nil)
	pool.Resize(2, 3)
	require.Equal(t, 2, pool.Config().MinConns)
}

func TestPoolDrainPreventsAcquire(t *testing.T) {
	srv, lis := setupBufConn(t)
	t.Cleanup(srv.Stop)
	cfg := DefaultConfig()
	cfg.MinConns = 1
	cfg.MaxConns = 1
	cfg.HealthCheckInterval = time.Second
	pool, err := NewPool(cfg,
		WithRegisterer(prometheus.NewRegistry()),
		WithDialer(func(ctx context.Context, target Target, _ Config) (*grpc.ClientConn, error) {
			return grpc.DialContext(ctx, target.Endpoint,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
			)
		}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	pool.RegisterTarget(Target{ID: "enclave-b", Endpoint: "buf"})
	require.NoError(t, pool.Drain("enclave-b"))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = pool.Acquire(ctx, "enclave-b")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrPoolDraining))
}

func TestConnPoolRace(t *testing.T) {
	srv, lis := setupBufConn(t)
	t.Cleanup(srv.Stop)
	cfg := DefaultConfig()
	cfg.MinConns = 2
	cfg.MaxConns = 4
	cfg.HealthCheckInterval = 200 * time.Millisecond
	pool, err := NewPool(cfg,
		WithRegisterer(prometheus.NewRegistry()),
		WithDialer(func(ctx context.Context, target Target, _ Config) (*grpc.ClientConn, error) {
			return grpc.DialContext(ctx, target.Endpoint,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
			)
		}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	pool.RegisterTarget(Target{ID: "race", Endpoint: "buf"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := pool.Acquire(ctx, "race")
			if err != nil {
				return
			}
			client := lease.Client()
			_, _ = client.Sign(ctx, &signerv1.SignRequest{KeyId: "k1", Digest: make([]byte, 32)})
			lease.Release(nil)
		}()
	}
	wg.Wait()
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("SIGN_CONN_POOL_MIN", "8")
	t.Setenv("SIGN_CONN_POOL_MAX", "16")
	t.Setenv("SIGN_CONN_POOL_ACQUIRE_TIMEOUT", "500ms")
	t.Setenv("SIGN_CONN_POOL_RETRY_JITTER", "0.1")
	cfg := LoadConfigFromEnv()
	require.Equal(t, 8, cfg.MinConns)
	require.Equal(t, 16, cfg.MaxConns)
	require.Equal(t, 500*time.Millisecond, cfg.AcquireTimeout)
	require.InDelta(t, 0.1, cfg.Backoff.Jitter, 0.001)
}

func TestBackoffGrowth(t *testing.T) {
	cfg := BackoffConfig{Initial: 25 * time.Millisecond, Max: 200 * time.Millisecond, Jitter: 0}
	b := NewBackoff(cfg)
	d1 := b.Next()
	d2 := b.Next()
	d3 := b.Next()
	require.Equal(t, 25*time.Millisecond, d1)
	require.Equal(t, 50*time.Millisecond, d2)
	require.Equal(t, 100*time.Millisecond, d3)
	b.Reset()
	require.Equal(t, 25*time.Millisecond, b.Next())
}

func TestCircuitBreakerTransition(t *testing.T) {
	cb := newCircuitBreaker(2, 10*time.Millisecond)
	require.Equal(t, stateHealthy, cb.State())
	require.False(t, cb.Failure())
	require.True(t, cb.Failure())
	require.Equal(t, stateDegraded, cb.State())
	require.True(t, cb.Allow())
	time.Sleep(20 * time.Millisecond)
	require.True(t, cb.Allow())
	cb.Drain()
	require.False(t, cb.Allow())
}
