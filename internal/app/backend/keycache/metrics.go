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
	}
	reg.MustRegister(m.stateGauge, m.hardExpiredRejections, m.rehydrateLatency, m.rehydrateFailuresTotal)
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
	if !success {
		m.rehydrateFailuresTotal.WithLabelValues(keyspace).Inc()
	}
}

func labelForState(s State) string {
	switch s {
	case StateWarm, StateCool, StateInvalid:
		return string(s)
	default:
		return ""
	}
}
