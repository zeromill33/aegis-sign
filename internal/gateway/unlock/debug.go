package unlock

import (
	"encoding/json"
	"net/http"
	"time"
)

// DebugHandler 返回 /debug/unlock 所需的 handler。
func (d *Dispatcher) DebugHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snapshot := d.snapshot()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snapshot)
	})
}

type debugSnapshot struct {
	QueueDepth int       `json:"queueDepth"`
	InFlight   int       `json:"inFlight"`
	Workers    int       `json:"workers"`
	RateLimit  float64   `json:"rateLimit"`
	Keys       []string  `json:"keys"`
	Timestamp  time.Time `json:"timestamp"`
}

func (d *Dispatcher) snapshot() debugSnapshot {
	snap := debugSnapshot{Workers: d.cfg.Workers, Timestamp: time.Now()}
	d.mu.Lock()
	snap.InFlight = len(d.states)
	snap.Keys = make([]string, 0, len(d.states))
	for key := range d.states {
		snap.Keys = append(snap.Keys, key)
	}
	d.mu.Unlock()
	snap.QueueDepth = len(d.queue)
	if limiter := d.limiter.Load(); limiter != nil {
		snap.RateLimit = float64(limiter.Limit())
	}
	return snap
}
