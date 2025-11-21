package unlock

import (
	"context"
	"log/slog"

	"github.com/aegis-sign/wallet/internal/app/backend/keycache"
)

// NoopExecutor 是占位执行器，在真实 KMS/Enclave 实现就绪前完成幂等回调。
type NoopExecutor struct {
	logger *slog.Logger
}

// NewNoopExecutor 返回一个不会触发任何外部依赖的执行器。
func NewNoopExecutor(logger *slog.Logger) NoopExecutor {
	return NoopExecutor{logger: logger}
}

// Execute 直接返回成功结果，仅用于占位和演练脚本。
func (n NoopExecutor) Execute(ctx context.Context, payload JobPayload) keycache.UnlockResult {
	if n.logger != nil {
		n.logger.Info("noop unlock executor invoked", "key", payload.Event.KeyID, "reason", payload.Event.Reason)
	}
	return keycache.UnlockResult{
		Keyspace: payload.Event.Keyspace,
		KeyID:    payload.Event.KeyID,
		Reason:   payload.Event.Reason,
		Attempts: payload.Attempt,
		Success:  true,
	}
}
