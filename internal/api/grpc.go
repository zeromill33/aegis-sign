package signerapi

import (
	"context"
	"io"
	"strconv"
	"time"

	signerv1 "github.com/aegis-sign/wallet/docs/api/gen/go"
	"github.com/aegis-sign/wallet/pkg/apierrors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// GRPCServer 实现 signer.v1.SignerService。
type GRPCServer struct {
	signerv1.UnimplementedSignerServiceServer
	backend Backend
	unlock  *UnlockResponder
}

// NewGRPCServer 构造 gRPC server。
func NewGRPCServer(backend Backend, unlock *UnlockResponder) *GRPCServer {
	if backend == nil {
		panic("signer backend is required")
	}
	return &GRPCServer{backend: backend, unlock: unlock}
}

// Create 直接透传到 backend。
func (s *GRPCServer) Create(ctx context.Context, req *signerv1.CreateRequest) (*signerv1.CreateResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	resp, err := s.backend.Create(ctx, req)
	if err != nil {
		return nil, s.grpcError(err)
	}
	return resp, nil
}

// Sign 校验 digest 长度并调用 backend。
func (s *GRPCServer) Sign(ctx context.Context, req *signerv1.SignRequest) (*signerv1.SignResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if len(req.GetDigest()) != 32 {
		return nil, status.Error(codes.InvalidArgument, "digest must be 32 bytes")
	}
	resp, err := s.backend.Sign(ctx, req)
	if err != nil {
		s.tryHandleUnlock(ctx, req.GetKeyId(), err)
		return nil, s.grpcError(err)
	}
	return resp, nil
}

// SignStream 支持双向流模式，用于压测和粘性路由。
func (s *GRPCServer) SignStream(stream signerv1.SignerService_SignStreamServer) error {
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if len(req.GetDigest()) != 32 {
			return status.Error(codes.InvalidArgument, "digest must be 32 bytes")
		}
		resp, signErr := s.backend.Sign(stream.Context(), req)
		if signErr != nil {
			s.tryHandleUnlock(stream.Context(), req.GetKeyId(), signErr)
			return s.grpcError(signErr)
		}
		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}

func (s *GRPCServer) grpcError(err error) error {
	if apiErr, ok := apierrors.FromError(err); ok {
		return status.Error(apierrors.GRPCStatus(apiErr.Code), apiErr.Error())
	}
	return status.Error(codes.Internal, "internal error")
}

func (s *GRPCServer) tryHandleUnlock(ctx context.Context, keyID string, err error) {
	if s == nil {
		return
	}
	apiErr, ok := apierrors.FromError(err)
	if !ok || apiErr.Code != apierrors.CodeUnlockRequired {
		return
	}
	meta := UnlockMetadata{RetryAfter: 100 * time.Millisecond}
	if s.unlock != nil {
		meta = s.unlock.Handle(ctx, keyID, err)
	}
	retry := meta.RetryAfter
	if retry <= 0 {
		retry = 100 * time.Millisecond
	}
	md := metadata.Pairs("retry-after-ms", strconv.FormatInt(retry.Milliseconds(), 10))
	if meta.RequestID != "" {
		md.Append("x-unlock-request-id", meta.RequestID)
	}
	_ = grpc.SetHeader(ctx, md)
}
