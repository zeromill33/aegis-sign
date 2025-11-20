package keycache

// State 表示 KeyEntry 当前状态。
type State string

const (
	// StateWarm 表示 PlainKey 和 DEK 均可用，可直接签名。
	StateWarm State = "WARM"
	// StateCool 表示 PlainKey 已被清零，但 DEK 仍可用，需要本地再水合恢复。
	StateCool State = "COOL"
	// StateInvalid 表示 PlainKey 与 DEK 均不可用，需要走冷路径解锁。
	StateInvalid State = "INVALID"
)

func (s State) String() string {
	switch s {
	case StateWarm, StateCool, StateInvalid:
		return string(s)
	default:
		return "INVALID"
	}
}
