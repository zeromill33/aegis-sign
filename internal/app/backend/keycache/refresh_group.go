package keycache

import (
	"context"
	"log/slog"

	"golang.org/x/sync/singleflight"
)

// RefreshGroup 基于 singleflight 保证每个 key 仅有一个在飞刷新。
type RefreshGroup struct {
	metrics *Metrics
	logger  *slog.Logger
	group   singleflight.Group
}

// NewRefreshGroup 创建刷新协调器。
func NewRefreshGroup(metrics *Metrics, logger *slog.Logger) *RefreshGroup {
	if logger == nil {
		logger = slog.Default()
	}
	return &RefreshGroup{
		metrics: metrics,
		logger:  logger,
	}
}

// Go 异步触发刷新。
func (g *RefreshGroup) Go(ctx context.Context, keyspace, keyID string, fn RefreshFunc) {
	if g == nil {
		var sched NoopScheduler
		sched.Go(ctx, keyspace, keyID, fn)
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		defer cancel()
		if err := g.Do(ctx, keyspace, keyID, fn); err != nil && g.logger != nil {
			g.logger.Warn("refresh async failed", slog.String("key", keyID), slog.String("keyspace", keyspace), slog.Any("err", err))
		}
	}()
}

// Do 合并相同 key 的刷新操作。
func (g *RefreshGroup) Do(ctx context.Context, keyspace, keyID string, fn RefreshFunc) error {
	if g == nil {
		var sched NoopScheduler
		return sched.Do(ctx, keyspace, keyID, fn)
	}
	if fn == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	done := g.metrics.addWaiter(keyspace)
	defer done()

	resultCh := g.group.DoChan(keyID, func() (interface{}, error) {
		return nil, fn(ctx)
	})

	select {
	case <-ctx.Done():
		if g.metrics != nil {
			g.metrics.incWaitTimeout(keyspace)
		}
		if g.logger != nil {
			g.logger.Warn("refresh wait timeout", slog.String("key", keyID), slog.String("keyspace", keyspace), slog.String("reason", ctx.Err().Error()))
		}
		return ctx.Err()
	case res := <-resultCh:
		return res.Err
	}
}
