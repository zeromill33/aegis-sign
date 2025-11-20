package keycache

import "context"

// Rehydrator 定义本地再水合接口，实现应使用仍然有效的 DEK 解密密文 Blob。
type Rehydrator interface {
	Rehydrate(ctx context.Context, keyID string, cipherBlob []byte) ([32]byte, error)
}

// RefreshFunc 是单次刷新任务。
type RefreshFunc func(ctx context.Context) error

// RefreshScheduler 负责协调整个 key 的单航班刷新。
type RefreshScheduler interface {
	Go(ctx context.Context, keyspace, keyID string, fn RefreshFunc)
	Do(ctx context.Context, keyspace, keyID string, fn RefreshFunc) error
}

// NoopRehydrator 是占位实现，始终返回全零数据。
type NoopRehydrator struct{}

// Rehydrate 返回零值并报告错误。
func (NoopRehydrator) Rehydrate(context.Context, string, []byte) ([32]byte, error) {
	return [32]byte{}, ErrRehydrateUnsupported
}

// NoopScheduler 是默认刷新协调器，占位实现。
type NoopScheduler struct{}

// Go 直接异步执行刷新函数。
func (NoopScheduler) Go(ctx context.Context, _ string, _ string, fn RefreshFunc) {
	if fn == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	go func() { _ = fn(ctx) }()
}

// Do 直接执行刷新函数，不做任何单航班合并。
func (NoopScheduler) Do(ctx context.Context, _ string, _ string, fn RefreshFunc) error {
	if fn == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return fn(ctx)
}
