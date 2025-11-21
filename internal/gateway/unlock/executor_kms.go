package unlock

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/aegis-sign/wallet/internal/app/backend/keycache"
	kmspkg "github.com/aegis-sign/wallet/internal/infra/kms"
)

// KMSEnclaveExecutor 使用 kms.Client 生成/刷新 DEK，并返回执行结果。
type KMSEnclaveExecutor struct {
	client *kmspkg.Client
	logger *slog.Logger
}

// NewKMSEnclaveExecutor 构造执行器。
func NewKMSEnclaveExecutor(client *kmspkg.Client, logger *slog.Logger) KMSEnclaveExecutor {
	if logger == nil {
		logger = slog.Default()
	}
	return KMSEnclaveExecutor{client: client, logger: logger}
}

// Execute 调用 KMS 生成数据密钥（代替解锁流程），供集成演练验证。
func (e KMSEnclaveExecutor) Execute(ctx context.Context, payload JobPayload) keycache.UnlockResult {
	result := keycache.UnlockResult{
		Keyspace: payload.Event.Keyspace,
		KeyID:    payload.Event.KeyID,
		Reason:   payload.Event.Reason,
		Attempts: payload.Attempt,
	}
	if e.client == nil {
		result.Err = errors.New("kms client not configured")
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now()
	_, err := e.client.GenerateDataKey(ctx, payload.Event.KeyID)
	if err != nil {
		result.Err = err
		if e.logger != nil {
			e.logger.Warn("kms unlock failed", "key", payload.Event.KeyID, "err", err)
		}
		return result
	}
	result.Success = true
	if e.logger != nil {
		e.logger.Info("kms unlock success", "key", payload.Event.KeyID, "latency_ms", time.Since(start).Milliseconds())
	}
	return result
}
