package unlock

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aegis-sign/wallet/internal/app/backend/keycache"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestDispatcherDedupPerKey(t *testing.T) {
	exec := &stubExecutor{}
	metrics := NewMetrics(newPromRegistry())
	d, err := NewDispatcher(Config{MaxQueue: 4, Workers: 1, Metrics: metrics}, exec)
	require.NoError(t, err)
	t.Cleanup(d.Close)

	evt := keycache.UnlockEvent{KeyID: "k1", Keyspace: "prod", Reason: "test"}
	require.NoError(t, d.NotifyUnlock(context.Background(), evt))
	require.NoError(t, d.NotifyUnlock(context.Background(), evt))

	require.Eventually(t, func() bool {
		return exec.CallCount() == 1
	}, time.Second, 10*time.Millisecond)
}

func TestDispatcherRetriesUpToMaxAttempts(t *testing.T) {
	exec := &stubExecutor{}
	exec.failures.Store(2)
	metrics := NewMetrics(newPromRegistry())
	d, err := NewDispatcher(Config{MaxQueue: 4, Workers: 1, BackoffBase: time.Millisecond, BackoffMax: 5 * time.Millisecond, Metrics: metrics}, exec)
	require.NoError(t, err)
	t.Cleanup(d.Close)

	evt := keycache.UnlockEvent{KeyID: "k-retry", Keyspace: "prod", Reason: "retry"}
	require.NoError(t, d.NotifyUnlock(context.Background(), evt))

	require.Eventually(t, func() bool {
		return exec.CallCount() == 3
	}, time.Second, 5*time.Millisecond)
	require.Equal(t, int64(3), exec.CallCount())
}

func TestDispatcherRateLimit(t *testing.T) {
	exec := &stubExecutor{}
	metrics := NewMetrics(newPromRegistry())
	d, err := NewDispatcher(Config{MaxQueue: 2, Workers: 1, RateLimit: 1, Metrics: metrics}, exec)
	require.NoError(t, err)
	t.Cleanup(d.Close)

	evt := keycache.UnlockEvent{KeyID: "k-rate", Keyspace: "prod", Reason: "rate"}
	require.NoError(t, d.NotifyUnlock(context.Background(), evt))
	err = d.NotifyUnlock(context.Background(), keycache.UnlockEvent{KeyID: "k-rate-2", Keyspace: "prod", Reason: "rate"})
	require.ErrorIs(t, err, ErrRateLimited)
}

type stubExecutor struct {
	count    atomic.Int64
	failures atomic.Int64
}

func (s *stubExecutor) Execute(ctx context.Context, payload JobPayload) keycache.UnlockResult {
	s.count.Add(1)
	if s.failures.Load() > 0 {
		s.failures.Add(-1)
		return keycache.UnlockResult{KeyID: payload.Event.KeyID, Success: false, Reason: payload.Event.Reason}
	}
	return keycache.UnlockResult{KeyID: payload.Event.KeyID, Success: true, Reason: payload.Event.Reason}
}

func (s *stubExecutor) CallCount() int64 {
	return s.count.Load()
}

func newPromRegistry() *prometheus.Registry {
	return prometheus.NewRegistry()
}
