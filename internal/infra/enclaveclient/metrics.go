package enclaveclient

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics 暴露 active_conns / grpc_stream_resets / pool_acquire_latency_ms。
type Metrics struct {
	activeConns    *prometheus.GaugeVec
	streamResets   *prometheus.CounterVec
	acquireLatency *prometheus.HistogramVec
}

// NewMetrics 在注册器中注册三类指标。
func NewMetrics(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	m := &Metrics{
		activeConns: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "signer",
			Subsystem: "enclave_pool",
			Name:      "active_conns",
			Help:      "Number of established gRPC connections per enclave",
		}, []string{"enclave_id"}),
		streamResets: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "signer",
			Subsystem: "enclave_pool",
			Name:      "grpc_stream_resets_total",
			Help:      "Total number of gRPC stream reset events",
		}, []string{"enclave_id"}),
		acquireLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "signer",
			Subsystem: "enclave_pool",
			Name:      "pool_acquire_latency_ms",
			Help:      "Time spent waiting for a pooled connection in milliseconds",
			Buckets:   []float64{0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 20, 50, 100, 200, 500},
		}, []string{"enclave_id"}),
	}
	reg.MustRegister(m.activeConns, m.streamResets, m.acquireLatency)
	return m
}

func (m *Metrics) setActive(enclaveID string, value float64) {
	m.activeConns.WithLabelValues(enclaveID).Set(value)
}

func (m *Metrics) incStreamReset(enclaveID string) {
	m.streamResets.WithLabelValues(enclaveID).Inc()
}

func (m *Metrics) observeAcquire(enclaveID string, duration time.Duration) {
	m.acquireLatency.WithLabelValues(enclaveID).Observe(duration.Seconds() * 1000)
}
