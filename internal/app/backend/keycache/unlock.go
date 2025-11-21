package keycache

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/aegis-sign/wallet/pkg/apierrors"
)

// UnlockNotifier 描述异步解锁通道，Story 2.2/2.3 通过它通知/确认解锁任务。
type UnlockNotifier interface {
	// NotifyUnlock 将 key 加入后台解锁队列。
	NotifyUnlock(ctx context.Context, event UnlockEvent) error
	// Ack 由后台解锁完成后调用，用于回传执行结果以便统计指标。
	Ack(ctx context.Context, result UnlockResult)
}

// UnlockEvent 记录一次解锁请求的上下文。
type UnlockEvent struct {
	Keyspace      string
	KeyID         string
	Reason        string
	RefreshBudget time.Duration
	RequestID     string
}

// UnlockResult 由后台解锁完成后回传，用于统计/自愈。
type UnlockResult struct {
	Keyspace  string
	KeyID     string
	Reason    string
	RequestID string

	Attempts int
	Success  bool
	Err      error
}

var (
	notifierMu           sync.RWMutex
	globalUnlockNotifier UnlockNotifier = noopUnlockNotifier{}
)

// SetUnlockNotifier 设置默认 UnlockNotifier，便于网关注入 Dispatcher。
func SetUnlockNotifier(n UnlockNotifier) {
	notifierMu.Lock()
	defer notifierMu.Unlock()
	if n == nil {
		globalUnlockNotifier = noopUnlockNotifier{}
		return
	}
	globalUnlockNotifier = n
}

func defaultUnlockNotifier() UnlockNotifier {
	notifierMu.RLock()
	defer notifierMu.RUnlock()
	return globalUnlockNotifier
}

type noopUnlockNotifier struct{}

func (noopUnlockNotifier) NotifyUnlock(context.Context, UnlockEvent) error { return nil }

func (noopUnlockNotifier) Ack(context.Context, UnlockResult) {}

// UnlockRequiredError 包装 apierrors.CodeUnlockRequired 以携带额外上下文。
type UnlockRequiredError struct {
	apiErr        *apierrors.Error
	reason        string
	refreshBudget time.Duration
}

// NewUnlockRequiredError 根据原因/预算构造错误。
func NewUnlockRequiredError(reason string, budget time.Duration) *UnlockRequiredError {
	if budget <= 0 {
		budget = defaultRefreshBudget
	}
	return &UnlockRequiredError{
		apiErr:        apierrors.New(apierrors.CodeUnlockRequired, reason),
		reason:        reason,
		refreshBudget: budget,
	}
}

// Error 实现 error 接口。
func (e *UnlockRequiredError) Error() string {
	if e == nil {
		return "unlock required"
	}
	return e.apiErr.Error()
}

// Unwrap 暴露内部 api 错误以兼容旧逻辑。
func (e *UnlockRequiredError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.apiErr
}

// Reason 返回触发解锁的原因。
func (e *UnlockRequiredError) Reason() string {
	if e == nil || e.reason == "" {
		return "unlock required"
	}
	return e.reason
}

// RefreshBudget 返回触发刷新时的预算。
func (e *UnlockRequiredError) RefreshBudget() time.Duration {
	if e == nil {
		return 0
	}
	return e.refreshBudget
}

// AsUnlockRequired 尝试解析 UnlockRequiredError。
func AsUnlockRequired(err error) (*UnlockRequiredError, bool) {
	var target *UnlockRequiredError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}
