package keycache

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/aegis-sign/wallet/pkg/apierrors"
)

func TestEntryCheckoutWarm(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	notifier := &recordingNotifier{}
	plain := fixedPlain(0xAA)
	metrics := NewMetrics(prometheus.NewRegistry())
	entry, err := NewEntry(EntryConfig{
		KeyID:        "key-1",
		Enclave:      "enclave-a",
		Keyspace:     "prod",
		PlainKey:     plain,
		HasPlainKey:  true,
		CipherBlob:   []byte("cipher"),
		UsesLeft:     10,
		MaxUses:      10,
		PlainSoftTTL: time.Minute,
		PlainHardTTL: 2 * time.Minute,
		DEKValidFor:  time.Hour,
		Clock:        clock,
		Metrics:      metrics,
		Notifier:     notifier,
	})
	require.NoError(t, err)

	result, err := entry.Checkout(context.Background())
	require.NoError(t, err)
	require.Equal(t, plain, result.PlainKey)
	require.True(t, result.HasPlainKey)
	require.Equal(t, uint32(9), entry.UsesLeft())
	require.Equal(t, StateWarm, entry.State())
	require.Zero(t, notifier.Len())
}

func TestEntrySoftTTLTriggersNotifier(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	notifier := &recordingNotifier{}
	entry := mustEntry(t, EntryConfig{
		KeyID:        "key-soft",
		Enclave:      "enc",
		Keyspace:     "prod",
		PlainKey:     fixedPlain(0x01),
		HasPlainKey:  true,
		CipherBlob:   []byte("cipher"),
		UsesLeft:     100,
		MaxUses:      100,
		PlainSoftTTL: time.Millisecond,
		PlainHardTTL: time.Second,
		DEKValidFor:  time.Hour,
		Clock:        clock,
		Notifier:     notifier,
	})

	clock.Advance(2 * time.Millisecond)
	_, err := entry.Checkout(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, notifier.Len())
}

func TestEntryHardTTLTriggersRehydrate(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	stub := &stubRehydrator{plain: fixedPlain(0xBB)}
	entry := mustEntry(t, EntryConfig{
		KeyID:        "key-hard",
		Enclave:      "enc",
		Keyspace:     "prod",
		PlainKey:     fixedPlain(0x02),
		HasPlainKey:  true,
		CipherBlob:   []byte("cipher"),
		UsesLeft:     1,
		MaxUses:      4,
		PlainSoftTTL: time.Millisecond,
		PlainHardTTL: 2 * time.Millisecond,
		DEKValidFor:  time.Minute,
		Clock:        clock,
		Rehydrator:   stub,
	})

	clock.Advance(5 * time.Millisecond)
	res, err := entry.Checkout(context.Background())
	require.NoError(t, err)
	require.Equal(t, fixedPlain(0xBB), res.PlainKey)
	require.Equal(t, uint32(3), entry.UsesLeft())
	require.Equal(t, 1, stub.Calls())
}

func TestEntryRehydrateFailureMarksInvalid(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg)
	stub := &stubRehydrator{err: errors.New("boom")}
	entry := mustEntry(t, EntryConfig{
		KeyID:        "key-fail",
		Enclave:      "enc",
		Keyspace:     "prod",
		PlainKey:     fixedPlain(0x05),
		HasPlainKey:  false,
		CipherBlob:   []byte("cipher"),
		UsesLeft:     0,
		MaxUses:      1,
		PlainSoftTTL: time.Millisecond,
		PlainHardTTL: time.Millisecond,
		DEKValidFor:  time.Minute,
		Clock:        clock,
		Rehydrator:   stub,
		Metrics:      metrics,
	})

	_, err := entry.Checkout(context.Background())
	require.Error(t, err)
	apiErr, ok := apierrors.FromError(err)
	require.True(t, ok)
	require.Equal(t, apierrors.CodeUnlockRequired, apiErr.Code)
	require.Equal(t, StateInvalid, entry.State())

	counter := testutil.ToFloat64(metrics.hardExpiredRejections.WithLabelValues("prod"))
	require.Equal(t, 1.0, counter)
	failCounter := testutil.ToFloat64(metrics.rehydrateFailuresTotal.WithLabelValues("prod"))
	require.Equal(t, 1.0, failCounter)
}

func TestEntryCipherBlobIsReadOnly(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	baseCipher := []byte("cipher-orig")
	stub := &stubRehydrator{plain: fixedPlain(0x33)}
	entry := mustEntry(t, EntryConfig{
		KeyID:       "key-blob",
		Enclave:     "enc",
		Keyspace:    "prod",
		CipherBlob:  append([]byte(nil), baseCipher...),
		HasPlainKey: false,
		Clock:       clock,
		Rehydrator:  stub,
	})
	baseCipher[0] = 'x'

	_, err := entry.Checkout(context.Background())
	require.NoError(t, err)
	require.Equal(t, []byte("cipher-orig"), stub.LastBlob())
}

// --- helpers ---

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type stubRehydrator struct {
	mu    sync.Mutex
	err   error
	plain [32]byte
	calls int
	last  []byte
}

func (s *stubRehydrator) Rehydrate(ctx context.Context, keyID string, blob []byte) ([32]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.last = append([]byte(nil), blob...)
	return s.plain, s.err
}

func (s *stubRehydrator) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *stubRehydrator) LastBlob() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

type recordingNotifier struct {
	mu   sync.Mutex
	keys []string
}

func (n *recordingNotifier) Notify(keyID string) {
	n.mu.Lock()
	n.keys = append(n.keys, keyID)
	n.mu.Unlock()
}

func (n *recordingNotifier) Len() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.keys)
}

func fixedPlain(val byte) [32]byte {
	var out [32]byte
	for i := range out {
		out[i] = val
	}
	return out
}

func mustEntry(t *testing.T, cfg EntryConfig) *Entry {
	t.Helper()
	if cfg.Enclave == "" {
		cfg.Enclave = "enc"
	}
	if cfg.Keyspace == "" {
		cfg.Keyspace = "prod"
	}
	if cfg.KeyID == "" {
		cfg.KeyID = "key"
	}
	if cfg.PlainSoftTTL == 0 {
		cfg.PlainSoftTTL = time.Minute
	}
	if cfg.PlainHardTTL == 0 {
		cfg.PlainHardTTL = 2 * time.Minute
	}
	if cfg.DEKValidFor == 0 {
		cfg.DEKValidFor = time.Hour
	}
	if cfg.Metrics == nil {
		cfg.Metrics = NewMetrics(prometheus.NewRegistry())
	}
	entry, err := NewEntry(cfg)
	require.NoError(t, err)
	return entry
}
