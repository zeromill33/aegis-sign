package signerapi

import (
	"context"
	"errors"
	"hash/fnv"
	"sync/atomic"
	"time"

	signerv1 "github.com/aegis-sign/wallet/docs/api/gen/go"
	"github.com/aegis-sign/wallet/internal/infra/enclaveclient"
)

// TargetSelector 决定 key/create 请求映射到哪个 Enclave。
type TargetSelector interface {
	SelectForCreate(ctx context.Context, req *signerv1.CreateRequest) (string, error)
	SelectForSign(ctx context.Context, req *signerv1.SignRequest) (string, error)
}

// StaticTargetSelector 始终返回同一个 Enclave（便于单 Enclave 基线环境）。
type StaticTargetSelector struct {
	TargetID string
}

func (s StaticTargetSelector) SelectForCreate(context.Context, *signerv1.CreateRequest) (string, error) {
	if s.TargetID == "" {
		return "", errors.New("static selector requires target id")
	}
	return s.TargetID, nil
}

func (s StaticTargetSelector) SelectForSign(_ context.Context, req *signerv1.SignRequest) (string, error) {
	if req == nil {
		return "", errors.New("nil sign request")
	}
	if s.TargetID == "" {
		return "", errors.New("static selector requires target id")
	}
	return s.TargetID, nil
}

// EnclaveBackend 通过 enclaveclient.Pool 复用长连接。
type EnclaveBackend struct {
	pool        *enclaveclient.Pool
	selector    TargetSelector
	callTimeout time.Duration
}

// 默认 RPC 超时时间，覆盖 handler 级别 deadline。
const defaultCallTimeout = 2 * time.Second

// EnclaveBackendOption 定义可选参数。
type EnclaveBackendOption func(*EnclaveBackend)

// WithCallTimeout 自定义单次 RPC 超时时间。
func WithCallTimeout(d time.Duration) EnclaveBackendOption {
	return func(b *EnclaveBackend) {
		if d > 0 {
			b.callTimeout = d
		}
	}
}

// NewEnclaveBackend 构造依赖连接池的 Backend 实现。
func NewEnclaveBackend(pool *enclaveclient.Pool, selector TargetSelector, opts ...EnclaveBackendOption) (*EnclaveBackend, error) {
	if pool == nil {
		return nil, errors.New("enclave pool is required")
	}
	if selector == nil {
		return nil, errors.New("target selector is required")
	}
	backend := &EnclaveBackend{
		pool:        pool,
		selector:    selector,
		callTimeout: defaultCallTimeout,
	}
	for _, opt := range opts {
		opt(backend)
	}
	return backend, nil
}

// Create 通过长连接在 Enclave 端创建 key。
func (b *EnclaveBackend) Create(ctx context.Context, req *signerv1.CreateRequest) (_ *signerv1.CreateResponse, err error) {
	target, err := b.selector.SelectForCreate(ctx, req)
	if err != nil {
		return nil, err
	}
	lease, err := b.pool.Acquire(ctx, target)
	if err != nil {
		return nil, err
	}
	defer func() { lease.Release(err) }()
	callCtx, cancel := context.WithTimeout(ctx, b.callTimeout)
	defer cancel()
	resp, err := lease.Client().Create(callCtx, req)
	return resp, err
}

// Sign 通过复用的长连接执行签名。
func (b *EnclaveBackend) Sign(ctx context.Context, req *signerv1.SignRequest) (_ *signerv1.SignResponse, err error) {
	target, err := b.selector.SelectForSign(ctx, req)
	if err != nil {
		return nil, err
	}
	lease, err := b.pool.Acquire(ctx, target)
	if err != nil {
		return nil, err
	}
	defer func() { lease.Release(err) }()
	callCtx, cancel := context.WithTimeout(ctx, b.callTimeout)
	defer cancel()
	client := lease.Client()
	stream, err := client.SignStream(callCtx)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(req); err != nil {
		return nil, err
	}
	resp, err := stream.Recv()
	if closeErr := stream.CloseSend(); closeErr != nil && err == nil {
		err = closeErr
	}
	return resp, err
}

// StickySelector 根据 keyId 做一致性路由，Create 请求使用轮询方式均衡分发。
type StickySelector struct {
	targetIDs []string
	rr        atomic.Uint64
}

// NewStickySelector 构造一致性路由选择器。
func NewStickySelector(targetIDs []string) (TargetSelector, error) {
	if len(targetIDs) == 0 {
		return nil, errors.New("at least one enclave target is required")
	}
	ids := make([]string, len(targetIDs))
	copy(ids, targetIDs)
	return &StickySelector{targetIDs: ids}, nil
}

// SelectForCreate 使用轮询，避免 create 请求扎堆。
func (s *StickySelector) SelectForCreate(context.Context, *signerv1.CreateRequest) (string, error) {
	idx := int(s.rr.Add(1)-1) % len(s.targetIDs)
	return s.targetIDs[idx], nil
}

// SelectForSign 根据 keyId 做一致性 hash，保障缓存粘性路由。
func (s *StickySelector) SelectForSign(_ context.Context, req *signerv1.SignRequest) (string, error) {
	if len(s.targetIDs) == 0 {
		return "", errors.New("no enclave targets configured")
	}
	key := req.GetKeyId()
	if key == "" {
		return s.targetIDs[0], nil
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	idx := int(h.Sum32()) % len(s.targetIDs)
	return s.targetIDs[idx], nil
}
