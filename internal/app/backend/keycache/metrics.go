package keycache

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics 收敛 key cache 相关指标。
type Metrics struct {
	stateGauge             *prometheus.GaugeVec
	hardExpiredRejections  *prometheus.CounterVec
	rehydrateLatency       *prometheus.HistogramVec
	rehydrateFailuresTotal *prometheus.CounterVec
	rehydrateTotal         *prometheus.CounterVec
	singleflightWaiters    *prometheus.GaugeVec
	singleflightTimeouts   *prometheus.CounterVec
	prefetchScans          prometheus.Counter
	prefetchSkipped        prometheus.Counter
	prefetchTriggers       *prometheus.CounterVec
}

// NewMetrics 构造指标集合，reg 为空时默认使用全局注册器。
func NewMetrics(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	m := &Metrics{
		stateGauge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "key_cache_state",
			Help: "Number of key cache entries in each state",
		}, []string{"enclave", "state"}),
		hardExpiredRejections: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hard_expired_rejections_total",
			Help: "Number of requests rejected due to hard expiration",
		}, []string{"keyspace"}),
		rehydrateLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "rehydrate_latency_ms",
			Help:    "Latency of local rehydrate operations (milliseconds)",
			Buckets: []float64{0.05, 0.1, 0.2, 0.5, 1, 2, 3, 5, 7.5, 10},
		}, []string{"keyspace"}),
		rehydrateFailuresTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rehydrate_fail_total",
			Help: "Number of failed local rehydrate attempts",
		}, []string{"keyspace"}),
		rehydrateTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rehydrate_total",
			Help: "Number of local rehydrate attempts",
		}, []string{"keyspace"}),
		singleflightWaiters: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "singleflight_waiters",
			Help: "Number of goroutines waiting on key refresh singleflight",
		}, []string{"keyspace"}),
		singleflightTimeouts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "singleflight_wait_timeout_total",
			Help: "Number of refresh wait budget expirations",
		}, []string{"keyspace"}),
		prefetchScans: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "prefetch_scan_total",
			Help: "Number of key cache prefetch scans",
		}),
		prefetchSkipped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "prefetch_skipped_total",
			Help: "Number of keys skipped due to max in-flight",
		}),
		prefetchTriggers: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "prefetch_trigger_total",
			Help: "Number of keys scheduled by the background prefetcher",
		}, []string{"keyspace"}),
	}
	reg.MustRegister(
		m.stateGauge,
		m.hardExpiredRejections,
		m.rehydrateLatency,
		m.rehydrateFailuresTotal,
		m.rehydrateTotal,
		m.singleflightWaiters,
		m.singleflightTimeouts,
		m.prefetchScans,
		m.prefetchSkipped,
		m.prefetchTriggers,
	)
	return m
}

func (m *Metrics) updateState(enclave string, from, to State) {
	if m == nil || enclave == "" {
		return
	}
	if label := labelForState(from); label != "" {
		m.stateGauge.WithLabelValues(enclave, label).Dec()
	}
	if label := labelForState(to); label != "" {
		m.stateGauge.WithLabelValues(enclave, label).Inc()
	}
}

func (m *Metrics) incHardExpired(keyspace string) {
	if m == nil || keyspace == "" {
		return
	}
	m.hardExpiredRejections.WithLabelValues(keyspace).Inc()
}

func (m *Metrics) observeRehydrate(keyspace string, ms float64, success bool) {
	if m == nil || keyspace == "" {
		return
	}
	m.rehydrateLatency.WithLabelValues(keyspace).Observe(ms)
	m.rehydrateTotal.WithLabelValues(keyspace).Inc()
	if !success {
		m.rehydrateFailuresTotal.WithLabelValues(keyspace).Inc()
	}
}

func (m *Metrics) addWaiter(keyspace string) func() {
	if m == nil || keyspace == "" {
		return func() {}
	}
	g := m.singleflightWaiters.WithLabelValues(keyspace)
	g.Inc()
	return func() { g.Dec() }
}

func (m *Metrics) incWaitTimeout(keyspace string) {
	if m == nil || keyspace == "" {
		return
	}
	m.singleflightTimeouts.WithLabelValues(keyspace).Inc()
}

func (m *Metrics) incPrefetchScan() {
	if m == nil {
		return
	}
	m.prefetchScans.Inc()
}

func (m *Metrics) incPrefetchSkipped() {
	if m == nil {
		return
	}
	m.prefetchSkipped.Inc()
}

func (m *Metrics) incPrefetchTrigger(keyspace string) {
	if m == nil || keyspace == "" {
		return
	}
	m.prefetchTriggers.WithLabelValues(keyspace).Inc()
}

func labelForState(s State) string {
	switch s {
	case StateWarm, StateCool, StateInvalid:
		return string(s)
	default:
		return ""
	}
}
