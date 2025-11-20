package enclaveclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	signerv1 "github.com/aegis-sign/wallet/docs/api/gen/go"
	"github.com/mdlayher/vsock"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
)

// ErrTargetNotFound 表示请求的 Enclave 目标不存在。
var ErrTargetNotFound = errors.New("enclave target not registered")

// ErrPoolDraining 表示池正在摘除/排空。
var ErrPoolDraining = errors.New("enclave pool is draining")

// ErrAcquireTimeout 表示在指定时间内未获取到连接。
var ErrAcquireTimeout = errors.New("acquire enclave connection timeout")

// Dialer 允许自定义 vsock/unix socket 拨号逻辑。
type Dialer func(ctx context.Context, target Target, cfg Config) (*grpc.ClientConn, error)

// Target 描述单个 Enclave 的访问终端。
type Target struct {
	ID       string
	Endpoint string
	Metadata map[string]string
}

// Pool 管理父机→Enclave 的长连接池。
type Pool struct {
	ctx    context.Context
	cancel context.CancelFunc

	dialer  Dialer
	metrics *Metrics
	logger  *slog.Logger

	cfg atomic.Value // Config

	mu      sync.RWMutex
	targets map[string]*enclavePool
}

// Option 允许自定义 Pool 行为。
type Option func(*Pool)

// WithDialer 自定义拨号器。
func WithDialer(d Dialer) Option {
	return func(p *Pool) { p.dialer = d }
}

// WithLogger 注入 slog Logger。
func WithLogger(l *slog.Logger) Option {
	return func(p *Pool) { p.logger = l }
}

// WithRegisterer 指定 Prometheus 注册器。
func WithRegisterer(reg prometheus.Registerer) Option {
	return func(p *Pool) { p.metrics = NewMetrics(reg) }
}

// NewPool 根据配置创建连接池并预热最小连接数。
func NewPool(cfg Config, opts ...Option) (*Pool, error) {
	if cfg.MinConns <= 0 || cfg.MaxConns <= 0 {
		return nil, fmt.Errorf("invalid pool size: min=%d max=%d", cfg.MinConns, cfg.MaxConns)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pool{
		ctx:     ctx,
		cancel:  cancel,
		targets: make(map[string]*enclavePool),
		logger:  slog.Default(),
	}
	p.cfg.Store(cfg)
	p.dialer = defaultDialer
	for _, opt := range opts {
		opt(p)
	}
	if p.dialer == nil {
		p.dialer = defaultDialer
	}
	if p.metrics == nil {
		p.metrics = NewMetrics(nil)
	}
	return p, nil
}

// Close 停止所有后台任务并关闭连接。
func (p *Pool) Close() error {
	p.cancel()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, ep := range p.targets {
		_ = ep.close()
	}
	p.targets = map[string]*enclavePool{}
	return nil
}

// Config 返回当前配置副本。
func (p *Pool) Config() Config {
	return p.cfg.Load().(Config)
}

// UpdateConfig 热更新配置（min/max/backoff 等）。
func (p *Pool) UpdateConfig(cfg Config) {
	if cfg.MaxConns < cfg.MinConns {
		cfg.MaxConns = cfg.MinConns
	}
	p.cfg.Store(cfg)
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, ep := range p.targets {
		ep.updateCapacity(cfg.MaxConns)
		go ep.ensureMin(cfg.MinConns)
	}
}

// RegisterTarget 新增/更新 Enclave 目标。
func (p *Pool) RegisterTarget(target Target) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if target.ID == "" {
		return
	}
	if existing, ok := p.targets[target.ID]; ok {
		existing.updateTarget(target)
		return
	}
	ep := newEnclavePool(p, target)
	p.targets[target.ID] = ep
	go ep.ensureMin(p.Config().MinConns)
}

// RemoveTarget 移除 Enclave，关闭所有连接。
func (p *Pool) RemoveTarget(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ep, ok := p.targets[id]; ok {
		_ = ep.close()
		delete(p.targets, id)
	}
}

// Acquire 借用一条长连接。
func (p *Pool) Acquire(ctx context.Context, enclaveID string) (*Lease, error) {
	p.mu.RLock()
	ep := p.targets[enclaveID]
	p.mu.RUnlock()
	if ep == nil {
		return nil, ErrTargetNotFound
	}
	return ep.acquire(ctx)
}

// Drain 触发目标摘除，释放所有连接。
func (p *Pool) Drain(enclaveID string) error {
	p.mu.RLock()
	ep := p.targets[enclaveID]
	p.mu.RUnlock()
	if ep == nil {
		return ErrTargetNotFound
	}
	return ep.drain()
}

// Resize 全局更新最小/最大连接数。
func (p *Pool) Resize(min, max int) {
	cfg := p.Config()
	cfg.MinConns = min
	cfg.MaxConns = max
	p.UpdateConfig(cfg)
}

// Lease 表示从池中借出的连接句柄。
type Lease struct {
	conn     *connWrapper
	released atomic.Bool
}

// Conn 返回底层 *grpc.ClientConn。
func (l *Lease) Conn() *grpc.ClientConn {
	if l == nil || l.conn == nil {
		return nil
	}
	return l.conn.conn
}

// Client 返回 SignerServiceClient，便于直接发起 RPC。
func (l *Lease) Client() signerv1.SignerServiceClient {
	return signerv1.NewSignerServiceClient(l.Conn())
}

// Release 将连接归还池中；若 err!=nil 则标记为需重建。
func (l *Lease) Release(err error) {
	if l == nil || l.conn == nil {
		return
	}
	if l.released.Swap(true) {
		return
	}
	l.conn.pool.release(l.conn, err)
	l.conn = nil
}

// connWrapper 包装单条 gRPC 连接及其监控协程。
type connWrapper struct {
	conn      *grpc.ClientConn
	pool      *enclavePool
	cancel    context.CancelFunc
	target    Target
	unhealthy atomic.Bool
}

func (cw *connWrapper) close() {
	if cw.cancel != nil {
		cw.cancel()
	}
	_ = cw.conn.Close()
}

func (cw *connWrapper) start() {
	ctx, cancel := context.WithCancel(cw.pool.parent.ctx)
	cw.cancel = cancel
	go cw.watchConnectivity(ctx)
	go cw.healthProbe(ctx)
}

func (cw *connWrapper) watchConnectivity(ctx context.Context) {
	backoff := NewBackoff(cw.pool.parent.Config().Backoff)
	for {
		state := cw.conn.GetState()
		if state == connectivity.Shutdown {
			return
		}
		if !cw.conn.WaitForStateChange(ctx, state) {
			return
		}
		newState := cw.conn.GetState()
		if newState == connectivity.TransientFailure {
			cw.pool.parent.metrics.incStreamReset(cw.target.ID)
			cw.pool.breaker.Failure()
			delay := backoff.Next()
			select {
			case <-time.After(delay):
				cw.conn.ResetConnectBackoff()
			case <-ctx.Done():
				return
			}
		} else if newState == connectivity.Ready {
			backoff.Reset()
			cw.pool.breaker.Success()
		}
	}
}

func (cw *connWrapper) healthProbe(ctx context.Context) {
	cfg := cw.pool.parent.Config()
	interval := cfg.HealthCheckInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	client := healthpb.NewHealthClient(cw.conn)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cfg = cw.pool.parent.Config()
			if newInterval := cfg.HealthCheckInterval; newInterval != interval {
				if newInterval <= 0 {
					newInterval = 5 * time.Second
				}
				interval = newInterval
				ticker.Reset(interval)
			}
			probeCtx, cancel := context.WithTimeout(ctx, cfg.AcquireTimeout)
			resp, err := client.Check(probeCtx, &healthpb.HealthCheckRequest{Service: cfg.ServiceName})
			cancel()
			if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
				cw.unhealthy.Store(true)
				cw.pool.breaker.Failure()
				cw.pool.parent.logger.Warn("enclave health degraded", "enclave", cw.target.ID, "err", err)
			} else {
				cw.pool.breaker.Success()
			}
		}
	}
}

// enclavePool 管理单个 target 的连接集合。
type enclavePool struct {
	parent *Pool
	target Target

	mu      sync.Mutex
	conns   chan *connWrapper
	total   int
	breaker *circuitBreaker
	closed  bool
}

func newEnclavePool(parent *Pool, target Target) *enclavePool {
	cfg := parent.Config()
	return &enclavePool{
		parent:  parent,
		target:  target,
		conns:   make(chan *connWrapper, cfg.MaxConns),
		breaker: newCircuitBreaker(3, time.Second),
	}
}

func (ep *enclavePool) updateTarget(t Target) {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	if ep.closed {
		return
	}
	ep.target = t
}

func (ep *enclavePool) updateCapacity(max int) {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	if ep.closed {
		return
	}
	if cap(ep.conns) == max {
		return
	}
	newCh := make(chan *connWrapper, max)
	for {
		select {
		case conn := <-ep.conns:
			newCh <- conn
		default:
			goto done
		}
	}
done:
	ep.conns = newCh
}

func (ep *enclavePool) ensureMin(min int) {
	ctx := ep.parent.ctx
	for {
		ep.mu.Lock()
		total := ep.total
		ep.mu.Unlock()
		if total >= min {
			return
		}
		if err := ep.maybeOpen(ctx); err != nil {
			ep.parent.logger.Warn("prewarm connection failed", "enclave", ep.target.ID, "err", err)
			select {
			case <-time.After(200 * time.Millisecond):
			case <-ctx.Done():
				return
			}
			continue
		}
	}
}

func (ep *enclavePool) acquire(ctx context.Context) (*Lease, error) {
	if !ep.breaker.Allow() {
		return nil, ErrPoolDraining
	}
	cfg := ep.parent.Config()
	start := time.Now()
	acquireCtx := ctx
	var cancel context.CancelFunc
	if cfg.AcquireTimeout > 0 {
		acquireCtx, cancel = context.WithTimeout(ctx, cfg.AcquireTimeout)
		defer cancel()
	}
	for {
		select {
		case conn := <-ep.conns:
			if conn == nil {
				continue
			}
			if conn.unhealthy.Load() {
				conn.close()
				ep.decrement()
				go ep.maybeOpen(ep.parent.ctx)
				continue
			}
			ep.parent.metrics.observeAcquire(ep.target.ID, time.Since(start))
			return &Lease{conn: conn}, nil
		default:
			if err := ep.maybeOpen(ctx); err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				ep.parent.logger.Warn("open connection failed", "enclave", ep.target.ID, "err", err)
			}
		}
		select {
		case conn := <-ep.conns:
			if conn == nil {
				continue
			}
			if conn.unhealthy.Load() {
				conn.close()
				ep.decrement()
				go ep.maybeOpen(ep.parent.ctx)
				continue
			}
			ep.parent.metrics.observeAcquire(ep.target.ID, time.Since(start))
			return &Lease{conn: conn}, nil
		case <-acquireCtx.Done():
			return nil, errors.Join(ErrAcquireTimeout, acquireCtx.Err())
		}
	}
}

func (ep *enclavePool) maybeOpen(ctx context.Context) error {
	ep.mu.Lock()
	cfg := ep.parent.Config()
	if ep.total >= cfg.MaxConns {
		ep.mu.Unlock()
		return nil
	}
	ep.total++
	ep.mu.Unlock()
	if err := ep.openConnection(ctx); err != nil {
		ep.decrement()
		return err
	}
	return nil
}

func (ep *enclavePool) openConnection(ctx context.Context) error {
	cfg := ep.parent.Config()
	dialCtx, cancel := context.WithTimeout(ctx, cfg.DialTimeout)
	defer cancel()
	conn, err := ep.parent.dialer(dialCtx, ep.target, cfg)
	if err != nil {
		return err
	}
	wrapper := &connWrapper{conn: conn, pool: ep, target: ep.target}
	wrapper.start()
	select {
	case ep.conns <- wrapper:
		if cfg.MaxConns > 0 {
			ep.parent.metrics.setActive(ep.target.ID, float64(ep.total))
		}
		return nil
	case <-ep.parent.ctx.Done():
		wrapper.close()
		ep.decrement()
		return ep.parent.ctx.Err()
	}
}

func (ep *enclavePool) release(conn *connWrapper, err error) {
	if err != nil {
		conn.unhealthy.Store(true)
	}
	if conn.unhealthy.Load() {
		conn.close()
		ep.decrement()
		go ep.maybeOpen(ep.parent.ctx)
		return
	}
	ep.mu.Lock()
	if ep.closed {
		ep.mu.Unlock()
		conn.close()
		ep.decrement()
		return
	}
	select {
	case ep.conns <- conn:
		// connection returned
	default:
		ep.mu.Unlock()
		conn.close()
		ep.decrement()
		return
	}
	ep.mu.Unlock()
}

func (ep *enclavePool) decrement() {
	ep.mu.Lock()
	if ep.total > 0 {
		ep.total--
	}
	ep.mu.Unlock()
	if ep.parent != nil {
		ep.parent.metrics.setActive(ep.target.ID, float64(ep.total))
	}
}

func (ep *enclavePool) drain() error {
	ep.breaker.Drain()
	return ep.close()
}

func (ep *enclavePool) close() error {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	if ep.closed {
		return nil
	}
	ep.closed = true
	for {
		select {
		case conn := <-ep.conns:
			if conn != nil {
				conn.close()
			}
		default:
			goto done
		}
	}
done:
	ep.total = 0
	return nil
}

// defaultDialer 使用 gRPC keepalive 配置并启用双向流。
func defaultDialer(ctx context.Context, target Target, cfg Config) (*grpc.ClientConn, error) {
	params := keepalive.ClientParameters{
		Time:                cfg.KeepaliveTime,
		Timeout:             cfg.KeepaliveTimeout,
		PermitWithoutStream: true,
	}
	methodTimeout := cfg.AcquireTimeout
	if methodTimeout <= 0 {
		methodTimeout = 2 * time.Second
	}
	serviceConfig := fmt.Sprintf(`{"methodConfig":[{"name":[{"service":"%s"}],"timeout":"%s"}]}`, cfg.ServiceName, methodTimeout.String())
	dopts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(params),
		grpc.WithDefaultServiceConfig(serviceConfig),
		grpc.WithContextDialer(func(ctx context.Context, endpoint string) (net.Conn, error) {
			return dialEndpoint(ctx, endpoint)
		}),
		grpc.WithBlock(),
	}
	return grpc.DialContext(ctx, target.Endpoint, dopts...)
}

func dialEndpoint(ctx context.Context, endpoint string) (net.Conn, error) {
	switch {
	case strings.HasPrefix(endpoint, "unix://"):
		return (&net.Dialer{}).DialContext(ctx, "unix", strings.TrimPrefix(endpoint, "unix://"))
	case strings.HasPrefix(endpoint, "unix:"):
		return (&net.Dialer{}).DialContext(ctx, "unix", strings.TrimPrefix(endpoint, "unix:"))
	case strings.HasPrefix(endpoint, "vsock://"):
		return dialVsock(ctx, strings.TrimPrefix(endpoint, "vsock://"))
	case strings.HasPrefix(endpoint, "vsock:"):
		return dialVsock(ctx, strings.TrimPrefix(endpoint, "vsock:"))
	default:
		return (&net.Dialer{}).DialContext(ctx, "tcp", endpoint)
	}
}

func dialVsock(ctx context.Context, target string) (net.Conn, error) {
	parts := strings.Split(target, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid vsock endpoint: %s", target)
	}
	cid, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid vsock cid: %w", err)
	}
	port, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid vsock port: %w", err)
	}
	type dialResult struct {
		conn net.Conn
		err  error
	}
	resultCh := make(chan dialResult, 1)
	go func() {
		conn, dialErr := vsock.Dial(uint32(cid), uint32(port), nil)
		resultCh <- dialResult{conn: conn, err: dialErr}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-resultCh:
		return res.conn, res.err
	}
}
