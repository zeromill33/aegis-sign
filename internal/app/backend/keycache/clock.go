package keycache

import "time"

// Clock 用于可测试的时间来源。
type Clock interface {
	Now() time.Time
}

// realClock 使用 time.Now。
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// NewRealClock 返回默认时钟实现。
func NewRealClock() Clock { return realClock{} }
