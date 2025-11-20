package stub

import (
	"context"

	signerv1 "github.com/aegis-sign/wallet/docs/api/gen/go"
	"github.com/aegis-sign/wallet/pkg/apierrors"
)

// Backend 是一个默认的占位实现，提示使用者需要接入真实的 signer 后端。
type Backend struct{}

// New 返回一个占位 backend，实现了 signerapi.Backend 接口。
func New() *Backend { return &Backend{} }

// Create 当前仅返回占位错误，提醒尚未接入真实实现。
func (Backend) Create(context.Context, *signerv1.CreateRequest) (*signerv1.CreateResponse, error) {
	return nil, apierrors.New(apierrors.CodeRetryLater, "stub backend: implement Create")
}

// Sign 当前仅返回占位错误，提醒尚未接入真实实现。
func (Backend) Sign(context.Context, *signerv1.SignRequest) (*signerv1.SignResponse, error) {
	return nil, apierrors.New(apierrors.CodeRetryLater, "stub backend: implement Sign")
}
