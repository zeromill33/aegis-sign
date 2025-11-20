package signerapi

import (
	"context"

	signerv1 "github.com/aegis-sign/wallet/docs/api/gen/go"
)

// Backend 定义业务层接口，HTTP/gRPC handler 通过它与实际 signer 交互。
type Backend interface {
	Create(ctx context.Context, req *signerv1.CreateRequest) (*signerv1.CreateResponse, error)
	Sign(ctx context.Context, req *signerv1.SignRequest) (*signerv1.SignResponse, error)
}
