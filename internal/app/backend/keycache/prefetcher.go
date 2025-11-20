package keycache

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"time"
)

// EntryIterator 提供遍历缓存条目的能力。
type EntryIterator interface {
	Range(func(*Entry) bool)
}

// PrefetcherConfig 定义预刷新器参数。
type PrefetcherConfig struct {
	Iterator      EntryIterator
	Scheduler     RefreshScheduler
	Clock         Clock
	Metrics       *Metrics
	Logger        *slog.Logger
	RefreshWindow time.Duration
	LowWater      uint32
	JitterPercent float64
	Interval      time.Duration
	MaxInFlight   int
}

// Prefetcher 使用 refresh window + jitter 周期扫描 key cache。
type Prefetcher struct {
	cfg    PrefetcherConfig
	rand   *rand.Rand
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewPrefetcher 创建预刷新器。
func NewPrefetcher(cfg PrefetcherConfig) *Prefetcher {
	if cfg.Clock == nil {
		cfg.Clock = NewRealClock()
	}
	if cfg.Scheduler == nil {
		cfg.Scheduler = NoopScheduler{}
	}
	if cfg.RefreshWindow <= 0 {
		cfg.RefreshWindow = 2 * time.Minute
	}
	if cfg.Interval <= 0 {
		cfg.Interval = cfg.RefreshWindow / 2
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	if cfg.JitterPercent <= 0 {
		cfg.JitterPercent = 0.1
	}
	if cfg.MaxInFlight <= 0 {
		cfg.MaxInFlight = 32
	}
	return &Prefetcher{
		cfg:  cfg,
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Start 启动后台扫描，直到 ctx 结束。
func (p *Prefetcher) Start(ctx context.Context) {
	if ctx == nil || p == nil {
		return
	}
	p.Stop()
	p.ctx, p.cancel = context.WithCancel(ctx)
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		timer := time.NewTimer(p.nextInterval())
		defer timer.Stop()
		for {
			select {
			case <-p.ctx.Done():
				return
			case <-timer.C:
				p.RunOnce(p.ctx)
				timer.Reset(p.nextInterval())
			}
		}
	}()
}

// Stop 停止后台扫描。
func (p *Prefetcher) Stop() {
	if p == nil {
		return
	}
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	p.cancel = nil
	p.ctx = nil
}

// RunOnce 扫描所有条目并触发预刷新。
func (p *Prefetcher) RunOnce(ctx context.Context) {
	if p == nil || p.cfg.Iterator == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if p.cfg.Metrics != nil {
		p.cfg.Metrics.incPrefetchScan()
	}
	now := p.cfg.Clock.Now()
	triggered := 0
	p.cfg.Iterator.Range(func(e *Entry) bool {
		if e == nil {
			return true
		}
		if p.cfg.MaxInFlight > 0 && triggered >= p.cfg.MaxInFlight {
			if p.cfg.Metrics != nil {
				p.cfg.Metrics.incPrefetchSkipped()
			}
			return false
		}
		if !e.shouldPrefetch(now, p.cfg.RefreshWindow, p.cfg.LowWater) {
			return true
		}
		triggered++
		if p.cfg.Metrics != nil {
			p.cfg.Metrics.incPrefetchTrigger(e.keyspace)
		}
		p.cfg.Scheduler.Go(context.Background(), e.keyspace, e.keyID, e.refreshOnce)
		if p.cfg.Logger != nil {
			p.cfg.Logger.Debug("prefetch refresh scheduled", slog.String("key", e.keyID), slog.String("keyspace", e.keyspace))
		}
		return true
	})
}

func (p *Prefetcher) nextInterval() time.Duration {
	if p == nil {
		return time.Minute
	}
	base := float64(p.cfg.Interval)
	jitter := base * p.cfg.JitterPercent
	delta := (p.rand.Float64()*2 - 1) * jitter
	return time.Duration(base + delta)
}
