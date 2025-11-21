package unlock

import (
	"log/slog"
	"time"
)

// Config 控制 Dispatcher 行为。
type Config struct {
	MaxQueue    int
	Workers     int
	RateLimit   float64
	RateBurst   int
	BackoffBase time.Duration
	BackoffMax  time.Duration
	Logger      *slog.Logger
	Metrics     *Metrics
}

func (c *Config) normalize() Config {
	cfg := *c
	if cfg.MaxQueue <= 0 {
		cfg.MaxQueue = 2048
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 16
	}
	if cfg.RateBurst <= 0 {
		cfg.RateBurst = 1
	}
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = 50 * time.Millisecond
	}
	if cfg.BackoffMax <= 0 {
		cfg.BackoffMax = time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg
}
