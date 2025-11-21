package unlock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aegis-sign/wallet/internal/app/backend/keycache"
	"golang.org/x/time/rate"
)

var (
	// ErrQueueFull 当队列无可用 slot 时返回。
	ErrQueueFull = errors.New("unlock dispatcher queue full")
	// ErrRateLimited 表示命中速率限制。
	ErrRateLimited = errors.New("unlock dispatcher rate limited")
)

const maxAttempts = 3

// Executor 执行具体的解锁操作（KMS + Enclave 回写）。
type Executor interface {
	Execute(ctx context.Context, payload JobPayload) keycache.UnlockResult
}

// JobPayload 传递队列上下文给 Executor。
type JobPayload struct {
	Event     keycache.UnlockEvent
	RequestID string
	Attempt   int
}

// Dispatcher 负责接收 Unlock 通知、排队并调度执行。
type Dispatcher struct {
	cfg      Config
	executor Executor

	queue   chan *job
	stopCh  chan struct{}
	metrics *Metrics

	limiter atomic.Pointer[rate.Limiter]
	logger  *slog.Logger

	seq atomic.Uint64

	mu     sync.Mutex
	states map[string]*jobState

	wg sync.WaitGroup

	randMu sync.Mutex
	rnd    *rand.Rand
}

// job 是队列中的元素。
type job struct {
	event     keycache.UnlockEvent
	requestID string
}

// jobState 用于记录重试状态。
type jobState struct {
	job      *job
	attempts int
}

// NewDispatcher 创建并启动后台 worker。
func NewDispatcher(cfg Config, executor Executor) (*Dispatcher, error) {
	if executor == nil {
		return nil, errors.New("executor is required")
	}
	normalized := cfg.normalize()
	d := &Dispatcher{
		cfg:      normalized,
		executor: executor,
		queue:    make(chan *job, normalized.MaxQueue),
		stopCh:   make(chan struct{}),
		metrics:  normalized.Metrics,
		logger:   normalized.Logger,
		states:   make(map[string]*jobState),
		rnd:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	if d.metrics == nil {
		d.metrics = NewMetrics(nil)
	}
	if normalized.RateLimit > 0 {
		burst := normalized.RateBurst
		limiter := rate.NewLimiter(rate.Limit(normalized.RateLimit), burst)
		d.limiter.Store(limiter)
	}
	d.start()
	return d, nil
}

// NotifyUnlock 实现 keycache.UnlockNotifier，将 key 放入队列。
func (d *Dispatcher) NotifyUnlock(ctx context.Context, event keycache.UnlockEvent) error {
	if event.KeyID == "" {
		return errors.New("key id is required for unlock")
	}
	if event.RequestID == "" {
		event.RequestID = d.nextRequestID(event.KeyID)
	}
	if limiter := d.limiter.Load(); limiter != nil && !limiter.Allow() {
		return ErrRateLimited
	}
	d.mu.Lock()
	if state, ok := d.states[event.KeyID]; ok {
		state.job.event.Reason = event.Reason
		d.mu.Unlock()
		return nil
	}
	job := &job{event: event, requestID: event.RequestID}
	state := &jobState{job: job}
	d.states[event.KeyID] = state
	d.mu.Unlock()

	select {
	case d.queue <- job:
		d.metrics.incQueueDepth()
		d.metrics.incBackground(event.Keyspace, event.Reason)
		if d.logger != nil {
			d.logger.Info("unlock enqueued", slog.String("key", event.KeyID), slog.String("reason", event.Reason), slog.String("unlock_request_id", event.RequestID))
		}
		return nil
	default:
		d.mu.Lock()
		delete(d.states, event.KeyID)
		d.mu.Unlock()
		return ErrQueueFull
	}
}

// Ack 记录执行结果，供指标/Runbook 使用。
func (d *Dispatcher) Ack(ctx context.Context, result keycache.UnlockResult) {
	// 留作后续扩展（如回传到 key cache 或记录审计日志）。
}

// Close 停止 worker 并等待队列清空。
func (d *Dispatcher) Close() {
	close(d.stopCh)
	d.wg.Wait()
}

// UpdateRateLimit 热更新速率限制。
func (d *Dispatcher) UpdateRateLimit(rateValue float64) {
	if rateValue <= 0 {
		d.limiter.Store(nil)
		return
	}
	limiter := rate.NewLimiter(rate.Limit(rateValue), d.cfg.RateBurst)
	d.limiter.Store(limiter)
}

func (d *Dispatcher) start() {
	for i := 0; i < d.cfg.Workers; i++ {
		d.wg.Add(1)
		go d.workerLoop(i)
	}
}

func (d *Dispatcher) workerLoop(id int) {
	defer d.wg.Done()
	for {
		select {
		case <-d.stopCh:
			return
		case job := <-d.queue:
			if job == nil {
				continue
			}
			d.handleJob(job)
		}
	}
}

func (d *Dispatcher) handleJob(job *job) {
	state := d.markInFlight(job.event.KeyID)
	if state == nil {
		return
	}
	attempt := state.attempts
	payload := JobPayload{Event: job.event, RequestID: job.requestID, Attempt: attempt}
	start := time.Now()
	result := d.executor.Execute(context.Background(), payload)
	if result.KeyID == "" {
		result.KeyID = job.event.KeyID
	}
	if result.Keyspace == "" {
		result.Keyspace = job.event.Keyspace
	}
	if result.Reason == "" {
		result.Reason = job.event.Reason
	}
	if result.RequestID == "" {
		result.RequestID = job.requestID
	}
	result.Attempts = attempt
	d.metrics.observeLatency(job.event.Keyspace, float64(time.Since(start).Milliseconds()))

	if result.Success {
		d.finishJob(job.event.KeyID)
		d.Ack(context.Background(), result)
		return
	}

	if attempt >= maxAttempts {
		d.metrics.incFail(job.event.Keyspace, job.event.Reason)
		d.finishJob(job.event.KeyID)
		d.Ack(context.Background(), result)
		if d.logger != nil {
			d.logger.Warn("unlock failed permanently", slog.String("key", job.event.KeyID), slog.String("reason", job.event.Reason), slog.String("unlock_request_id", job.requestID))
		}
		return
	}

	delay := d.backoffDelay(attempt)
	d.metrics.incRetry(job.event.Keyspace, job.event.Reason)
	if d.logger != nil {
		d.logger.Info("unlock retry scheduled", slog.String("key", job.event.KeyID), slog.Int("attempt", attempt+1), slog.Duration("delay", delay), slog.String("unlock_request_id", job.requestID))
	}
	time.AfterFunc(delay, func() {
		select {
		case <-d.stopCh:
			return
		case d.queue <- job:
		}
	})
}

func (d *Dispatcher) markInFlight(key string) *jobState {
	d.mu.Lock()
	defer d.mu.Unlock()
	state := d.states[key]
	if state == nil {
		return nil
	}
	state.attempts++
	return state
}

func (d *Dispatcher) finishJob(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.states[key]; ok {
		delete(d.states, key)
		d.metrics.decQueueDepth()
	}
}

func (d *Dispatcher) nextRequestID(keyID string) string {
	seq := d.seq.Add(1)
	return fmt.Sprintf("unlock-%d-%s", seq, keyID)
}

func (d *Dispatcher) backoffDelay(attempt int) time.Duration {
	base := d.cfg.BackoffBase
	delay := base * time.Duration(1<<(attempt-1))
	if delay > d.cfg.BackoffMax {
		delay = d.cfg.BackoffMax
	}
	return d.jitter(delay, 0.2)
}

func (d *Dispatcher) jitter(dur time.Duration, factor float64) time.Duration {
	if factor <= 0 {
		return dur
	}
	maxJitter := time.Duration(float64(dur) * factor)
	if maxJitter <= 0 {
		return dur
	}
	d.randMu.Lock()
	delta := time.Duration(d.rnd.Int63n(int64(2*maxJitter+1))) - maxJitter
	d.randMu.Unlock()
	candidate := dur + delta
	if candidate < 0 {
		return 0
	}
	return candidate
}
