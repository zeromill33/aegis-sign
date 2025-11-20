package apierrors

import (
	"errors"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
)

// Code 表示统一业务错误码。
type Code string

const (
	CodeInvalidArgument Code = "INVALID_ARGUMENT"
	CodeRetryLater      Code = "RETRY_LATER"
	CodeUnlockRequired  Code = "UNLOCK_REQUIRED"
	CodeInvalidKey      Code = "INVALID_KEY"
)

var httpStatusMap = map[Code]int{
	CodeInvalidArgument: 400,
	CodeRetryLater:      429,
	CodeUnlockRequired:  503,
	CodeInvalidKey:      404,
}

var grpcStatusMap = map[Code]codes.Code{
	CodeInvalidArgument: codes.InvalidArgument,
	CodeRetryLater:      codes.ResourceExhausted,
	CodeUnlockRequired:  codes.Unavailable,
	CodeInvalidKey:      codes.NotFound,
}

// Error 表示带统一错误码的业务错误。
type Error struct {
	Code       Code
	Message    string
	retryAfter time.Duration
}

// New 创建一个新的业务错误。
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// WithRetryAfter 设置 Retry-After 提示，返回自身方便链式调用。
func (e *Error) WithRetryAfter(d time.Duration) *Error {
	e.retryAfter = d
	return e
}

// RetryAfterHint 以秒为单位返回 Retry-After 提示文本。
func (e *Error) RetryAfterHint() string {
	if e == nil || e.retryAfter <= 0 {
		return ""
	}
	seconds := int((e.retryAfter + time.Second - 1) / time.Second)
	if seconds <= 0 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return string(e.Code)
}

// FromError 尝试从通用 error 中解析业务错误。
func FromError(err error) (*Error, bool) {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

// HTTPStatus 返回对应的 HTTP 状态码，未知错误默认 500。
func HTTPStatus(code Code) int {
	if status, ok := httpStatusMap[code]; ok {
		return status
	}
	return 500
}

// GRPCStatus 返回对应的 gRPC code，未知错误默认 Internal。
func GRPCStatus(code Code) codes.Code {
	if status, ok := grpcStatusMap[code]; ok {
		return status
	}
	return codes.Internal
}

// RequiresRetryAfter 标记是否必须携带 Retry-After 头。
func RequiresRetryAfter(code Code) bool {
	return code == CodeRetryLater || code == CodeUnlockRequired
}
