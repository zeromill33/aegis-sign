package keycache

import "context"

// Rehydrator 定义本地再水合接口，实现应使用仍然有效的 DEK 解密密文 Blob。
type Rehydrator interface {
	Rehydrate(ctx context.Context, keyID string, cipherBlob []byte) ([32]byte, error)
}

// StaleNotifier 用于在 soft TTL / low water mark 时通知后台刷新器。
type StaleNotifier interface {
	Notify(keyID string)
}

// StaleNotifierFunc 允许直接传入函数。
type StaleNotifierFunc func(keyID string)

// Notify 调用函数本身。
func (f StaleNotifierFunc) Notify(keyID string) {
	if f != nil {
		f(keyID)
	}
}

// NoopRehydrator 是占位实现，始终返回全零数据。
type NoopRehydrator struct{}

// Rehydrate 返回零值并报告错误。
func (NoopRehydrator) Rehydrate(context.Context, string, []byte) ([32]byte, error) {
	return [32]byte{}, ErrRehydrateUnsupported
}
