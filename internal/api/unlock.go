package signerapi

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/aegis-sign/wallet/internal/app/backend/keycache"
)

// UnlockQueue 抽象后端解锁队列，供 HTTP/gRPC handler 注入。
type UnlockQueue interface {
	NotifyUnlock(ctx context.Context, event keycache.UnlockEvent) error
}

// UnlockResponderConfig 配置 UnlockResponder 行为。
type UnlockResponderConfig struct {
	Queue    UnlockQueue
	Keyspace string
	MinRetry time.Duration
	MaxRetry time.Duration
}

// UnlockMetadata 表示一次解锁响应所需的元数据。
type UnlockMetadata struct {
	RequestID  string
	RetryAfter time.Duration
}

// UnlockResponder 负责将 UNLOCK_REQUIRED 错误放入后台队列并生成客户端提示。
type UnlockResponder struct {
	queue    UnlockQueue
	keyspace string
	minRetry time.Duration
	maxRetry time.Duration
	rng      *rand.Rand
	seq      atomic.Uint64
}

// NewUnlockResponder 构造 UnlockResponder，若 Queue 为空则退化为仅生成 Retry-After。
func NewUnlockResponder(cfg UnlockResponderConfig) *UnlockResponder {
	min := cfg.MinRetry
	max := cfg.MaxRetry
	if min <= 0 {
		min = 50 * time.Millisecond
	}
	if max <= 0 {
		max = 200 * time.Millisecond
	}
	if max < min {
		max = min
	}
	keyspace := cfg.Keyspace
	if keyspace == "" {
		keyspace = "default"
	}
	return &UnlockResponder{
		queue:    cfg.Queue,
		keyspace: keyspace,
		minRetry: min,
		maxRetry: max,
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Handle 处理 UNLOCK_REQUIRED 错误，返回客户端需要的 request id 与 Retry-After。
func (r *UnlockResponder) Handle(ctx context.Context, keyID string, unlockErr error) UnlockMetadata {
	if ctx == nil {
		ctx = context.Background()
	}
	retryAfter := r.randomRetry()
	reason, refreshBudget := extractUnlockReason(unlockErr)
	requestID := r.nextRequestID(keyID)
	if r.queue != nil && keyID != "" {
		event := keycache.UnlockEvent{
			Keyspace:      r.keyspace,
			KeyID:         keyID,
			Reason:        reason,
			RefreshBudget: refreshBudget,
			RequestID:     requestID,
		}
		_ = r.queue.NotifyUnlock(ctx, event)
	}
	return UnlockMetadata{RequestID: requestID, RetryAfter: retryAfter}
}

func (r *UnlockResponder) randomRetry() time.Duration {
	if r == nil {
		return 100 * time.Millisecond
	}
	if r.maxRetry == r.minRetry {
		return r.minRetry
	}
	span := r.maxRetry - r.minRetry
	if span <= 0 {
		return r.minRetry
	}
	offset := time.Duration(r.rng.Int63n(int64(span)))
	return r.minRetry + offset
}

func (r *UnlockResponder) nextRequestID(keyID string) string {
	if r == nil {
		return ""
	}
	seq := r.seq.Add(1)
	if keyID == "" {
		keyID = "unknown"
	}
	return fmt.Sprintf("unlock-%d-%s", seq, keyID)
}

func extractUnlockReason(err error) (string, time.Duration) {
	var unlockErr *keycache.UnlockRequiredError
	if errors.As(err, &unlockErr) {
		return unlockErr.Reason(), unlockErr.RefreshBudget()
	}
	if err == nil {
		return "unlock required", 0
	}
	return err.Error(), 0
}
