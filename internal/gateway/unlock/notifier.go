package unlock

import (
	"context"

	"github.com/aegis-sign/wallet/internal/app/backend/keycache"
)

// DispatcherNotifier 将 keycache 的 UnlockNotifier 接口映射到 Dispatcher。
type DispatcherNotifier struct {
	dispatcher *Dispatcher
}

// NewDispatcherNotifier 构造基于 Dispatcher 的 UnlockNotifier 实现。
func NewDispatcherNotifier(dispatcher *Dispatcher) keycache.UnlockNotifier {
	return DispatcherNotifier{dispatcher: dispatcher}
}

// NotifyUnlock 将事件入队。
func (d DispatcherNotifier) NotifyUnlock(ctx context.Context, event keycache.UnlockEvent) error {
	if d.dispatcher == nil {
		return nil
	}
	return d.dispatcher.NotifyUnlock(ctx, event)
}

// Ack 当前仅记录结果，dispatcher 内部会在任务完成时更新指标。
func (d DispatcherNotifier) Ack(context.Context, keycache.UnlockResult) {}
