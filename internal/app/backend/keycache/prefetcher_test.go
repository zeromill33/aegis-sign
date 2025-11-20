package keycache

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

type entrySlice []*Entry

func (s entrySlice) Range(fn func(*Entry) bool) {
	for _, e := range s {
		if !fn(e) {
			return
		}
	}
}

func TestPrefetcherTriggersExpectedKeys(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	metrics := NewMetrics(prometheus.NewRegistry())
	sched := &recordingScheduler{}

	aging := mustEntry(t, EntryConfig{
		KeyID:        "key-aging",
		Enclave:      "enc",
		Keyspace:     "prod",
		PlainKey:     fixedPlain(0x01),
		HasPlainKey:  true,
		CipherBlob:   []byte("cipher"),
		MaxUses:      1000,
		UsesLeft:     900,
		PlainSoftTTL: time.Minute,
		PlainHardTTL: 2 * time.Minute,
		Clock:        clock,
		CreatedAt:    clock.Now().Add(-59 * time.Second),
	})

	lowWater := mustEntry(t, EntryConfig{
		KeyID:        "key-low",
		Enclave:      "enc",
		Keyspace:     "prod",
		PlainKey:     fixedPlain(0x02),
		HasPlainKey:  true,
		CipherBlob:   []byte("cipher"),
		MaxUses:      1000,
		UsesLeft:     40,
		LowWaterMark: 100,
		PlainSoftTTL: time.Hour,
		PlainHardTTL: 2 * time.Hour,
		Clock:        clock,
	})

	entries := entrySlice{aging, lowWater}
	p := NewPrefetcher(PrefetcherConfig{
		Iterator:      entries,
		Scheduler:     sched,
		Clock:         clock,
		Metrics:       metrics,
		RefreshWindow: time.Minute,
		LowWater:      80,
		Interval:      time.Minute,
		MaxInFlight:   10,
	})

	p.RunOnce(context.Background())

	require.Equal(t, 2, sched.GoCalls())
	require.Equal(t, 1.0, testutil.ToFloat64(metrics.prefetchScans))
	require.Equal(t, 2.0, testutil.ToFloat64(metrics.prefetchTriggers.WithLabelValues("prod")))
}

func TestPrefetcherWithRefreshGroupAndRehydrator(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	metrics := NewMetrics(prometheus.NewRegistry())
	group := NewRefreshGroup(metrics, nil)
	stub := &stubRehydrator{plain: fixedPlain(0x44)}
	entry := mustEntry(t, EntryConfig{
		KeyID:        "key-prefetch-refresh",
		Enclave:      "enc",
		Keyspace:     "prod",
		PlainKey:     fixedPlain(0x10),
		HasPlainKey:  true,
		CipherBlob:   []byte("cipher"),
		PlainSoftTTL: time.Millisecond,
		PlainHardTTL: time.Minute,
		Clock:        clock,
		Rehydrator:   stub,
		Metrics:      metrics,
		Refresher:    group,
	})
	clock.Advance(2 * time.Millisecond)
	p := NewPrefetcher(PrefetcherConfig{
		Iterator:      entrySlice{entry},
		Scheduler:     group,
		Clock:         clock,
		Metrics:       metrics,
		RefreshWindow: time.Millisecond,
		MaxInFlight:   1,
	})
	p.RunOnce(context.Background())
	require.Eventually(t, func() bool {
		return stub.Calls() == 1
	}, time.Second, 5*time.Millisecond)
	require.Equal(t, 1.0, testutil.ToFloat64(metrics.prefetchScans))
	require.Equal(t, 1.0, testutil.ToFloat64(metrics.prefetchTriggers.WithLabelValues("prod")))
	require.Equal(t, 1.0, testutil.ToFloat64(metrics.rehydrateTotal.WithLabelValues("prod")))
}
