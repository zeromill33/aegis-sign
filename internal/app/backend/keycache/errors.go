package keycache

import "errors"

var (
	// ErrRehydrateUnsupported 表示未配置本地再水合器。
	ErrRehydrateUnsupported = errors.New("rehydrator not configured")
)
