package unlock

import "github.com/prometheus/client_golang/prometheus"

// Metrics 记录异步解锁的关键指标。
type Metrics struct {
	queueDepth     prometheus.Gauge
	backgroundRate *prometheus.CounterVec
	failTotal      *prometheus.CounterVec
	latency        *prometheus.HistogramVec
	retryTotal     *prometheus.CounterVec
}

// NewMetrics 构造 Metrics，reg 为空则注册到默认注册器。
func NewMetrics(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	m := &Metrics{
		queueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "unlock_queue_depth",
			Help: "Number of keys pending unlock",
		}),
		backgroundRate: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "unlock_bg_rate",
			Help: "Background unlock attempts started",
		}, []string{"keyspace", "reason"}),
		failTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "unlock_fail_total",
			Help: "Number of unlock attempts failed",
		}, []string{"keyspace", "reason"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "unlock_latency_ms",
			Help:    "Latency of unlock attempts in milliseconds",
			Buckets: []float64{10, 25, 50, 75, 100, 250, 500, 750, 1000, 2000},
		}, []string{"keyspace"}),
		retryTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "unlock_retry_total",
			Help: "Number of unlock retries scheduled",
		}, []string{"keyspace", "reason"}),
	}
	reg.MustRegister(m.queueDepth, m.backgroundRate, m.failTotal, m.latency, m.retryTotal)
	return m
}

func (m *Metrics) incQueueDepth() {
	if m == nil {
		return
	}
	m.queueDepth.Inc()
}

func (m *Metrics) decQueueDepth() {
	if m == nil {
		return
	}
	m.queueDepth.Dec()
}

func (m *Metrics) incBackground(keyspace, reason string) {
	if m == nil {
		return
	}
	m.backgroundRate.WithLabelValues(labelOrUnknown(keyspace), labelOrUnknown(reason)).Inc()
}

func (m *Metrics) incFail(keyspace, reason string) {
	if m == nil {
		return
	}
	m.failTotal.WithLabelValues(labelOrUnknown(keyspace), labelOrUnknown(reason)).Inc()
}

func (m *Metrics) observeLatency(keyspace string, durMs float64) {
	if m == nil {
		return
	}
	m.latency.WithLabelValues(labelOrUnknown(keyspace)).Observe(durMs)
}

func (m *Metrics) incRetry(keyspace, reason string) {
	if m == nil {
		return
	}
	m.retryTotal.WithLabelValues(labelOrUnknown(keyspace), labelOrUnknown(reason)).Inc()
}

func labelOrUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
