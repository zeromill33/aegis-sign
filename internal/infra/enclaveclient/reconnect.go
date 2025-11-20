package enclaveclient

import (
	"math/rand"
	"sync"
	"time"
)

// Backoff 在连接中断时计算指数退避等待时间，包含抖动以避免惊群。
type Backoff struct {
	cfg      BackoffConfig
	mu       sync.Mutex
	attempts int
	rand     *rand.Rand
}

// NewBackoff 创建 Backoff。
func NewBackoff(cfg BackoffConfig) *Backoff {
	return &Backoff{
		cfg:  cfg,
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Next 计算下一次等待时长。
func (b *Backoff) Next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.attempts < 0 {
		b.attempts = 0
	}
	base := b.cfg.Initial << b.attempts
	if base <= 0 || base > b.cfg.Max {
		base = b.cfg.Max
	}
	if b.cfg.Jitter > 0 {
		low := 1 - b.cfg.Jitter
		high := 1 + b.cfg.Jitter
		factor := low + b.rand.Float64()*(high-low)
		base = time.Duration(float64(base) * factor)
	}
	if b.attempts < 16 {
		b.attempts++
	}
	if base < b.cfg.Initial {
		base = b.cfg.Initial
	}
	if base > b.cfg.Max {
		base = b.cfg.Max
	}
	return base
}

// Reset 清除历史失败，下一次退避重新从 Initial 开始。
func (b *Backoff) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.attempts = 0
}
