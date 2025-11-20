package keycache

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/aegis-sign/wallet/pkg/apierrors"
)

const (
	defaultLowWaterMark  = 50_000
	defaultMaxUses       = 1_000_000
	defaultSoftTTL       = 15 * time.Minute
	defaultHardTTL       = 16 * time.Minute
	defaultDEKValidFor   = 60 * time.Minute
	defaultRefreshBudget = 3 * time.Millisecond
)

// EntryConfig 用于初始化单个 key entry。
type EntryConfig struct {
	KeyID    string
	Enclave  string
	Keyspace string

	PlainKey     [32]byte
	HasPlainKey  bool
	CipherBlob   []byte
	UsesLeft     uint32
	MaxUses      uint32
	LowWaterMark uint32

	PlainSoftTTL time.Duration
	PlainHardTTL time.Duration
	DEKValidFor  time.Duration
	CreatedAt    time.Time

	RefreshBudget time.Duration
	Clock         Clock
	Metrics       *Metrics
	Logger        *slog.Logger
	Rehydrator    Rehydrator
	Refresher     RefreshScheduler
}

// Entry 表示单个 key cache 元素。
type Entry struct {
	keyID    string
	enclave  string
	keyspace string

	cipherBlob []byte

	softWindow    time.Duration
	hardWindow    time.Duration
	maxUses       uint32
	lowWater      uint32
	refreshBudget time.Duration

	clock      Clock
	metrics    *Metrics
	logger     *slog.Logger
	rehydrator Rehydrator
	refresher  RefreshScheduler

	mu            sync.Mutex
	priv32        [32]byte
	hasPlainKey   bool
	usesLeft      uint32
	softTTL       time.Time
	hardTTL       time.Time
	dekValidUntil time.Time
	state         State
}

// CheckoutResult 返回给调用者的 PlainKey 副本以及状态。
type CheckoutResult struct {
	KeyID       string
	State       State
	PlainKey    [32]byte
	HasPlainKey bool
}

// Zero 清零 PlainKey 副本，避免泄漏。
func (r *CheckoutResult) Zero() {
	if r == nil {
		return
	}
	secureZero(r.PlainKey[:])
	r.HasPlainKey = false
}

// NewEntry 根据配置创建 Entry。
func NewEntry(cfg EntryConfig) (*Entry, error) {
	if cfg.KeyID == "" {
		return nil, errors.New("key id is required")
	}
	if cfg.Enclave == "" {
		return nil, errors.New("enclave label is required")
	}
	if cfg.Keyspace == "" {
		return nil, errors.New("keyspace is required")
	}
	if cfg.Clock == nil {
		cfg.Clock = NewRealClock()
	}
	if cfg.PlainSoftTTL <= 0 {
		cfg.PlainSoftTTL = defaultSoftTTL
	}
	if cfg.PlainHardTTL <= 0 {
		cfg.PlainHardTTL = defaultHardTTL
	}
	if cfg.DEKValidFor <= 0 {
		cfg.DEKValidFor = defaultDEKValidFor
	}
	if cfg.RefreshBudget <= 0 {
		cfg.RefreshBudget = defaultRefreshBudget
	}
	if cfg.MaxUses == 0 {
		cfg.MaxUses = defaultMaxUses
	}
	if cfg.UsesLeft == 0 || cfg.UsesLeft > cfg.MaxUses {
		cfg.UsesLeft = cfg.MaxUses
	}
	if cfg.LowWaterMark == 0 {
		cfg.LowWaterMark = defaultLowWaterMark
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Rehydrator == nil {
		cfg.Rehydrator = NoopRehydrator{}
	}
	if cfg.Refresher == nil {
		cfg.Refresher = NoopScheduler{}
	}
	createdAt := cfg.CreatedAt
	if createdAt.IsZero() {
		createdAt = cfg.Clock.Now()
	}
	entry := &Entry{
		keyID:         cfg.KeyID,
		enclave:       cfg.Enclave,
		keyspace:      cfg.Keyspace,
		cipherBlob:    append([]byte(nil), cfg.CipherBlob...),
		softWindow:    cfg.PlainSoftTTL,
		hardWindow:    cfg.PlainHardTTL,
		maxUses:       cfg.MaxUses,
		lowWater:      cfg.LowWaterMark,
		refreshBudget: cfg.RefreshBudget,
		clock:         cfg.Clock,
		metrics:       cfg.Metrics,
		logger:        cfg.Logger,
		rehydrator:    cfg.Rehydrator,
		refresher:     cfg.Refresher,
		hasPlainKey:   cfg.HasPlainKey,
		usesLeft:      cfg.UsesLeft,
		softTTL:       createdAt.Add(cfg.PlainSoftTTL),
		hardTTL:       createdAt.Add(cfg.PlainHardTTL),
		dekValidUntil: createdAt.Add(cfg.DEKValidFor),
		state:         StateCool,
	}
	if cfg.HasPlainKey {
		entry.priv32 = cfg.PlainKey
		entry.state = StateWarm
	} else {
		entry.clearPlainLocked()
	}
	if entry.metrics != nil {
		entry.metrics.updateState(entry.enclave, State(""), entry.state)
	}
	return entry, nil
}

// Checkout 执行一次签名前的检查与状态迁移。
func (e *Entry) Checkout(ctx context.Context) (CheckoutResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		e.mu.Lock()
		result := CheckoutResult{KeyID: e.keyID, State: e.state}
		now := e.clock.Now()

		if err := e.ensureValidLocked(now); err != nil {
			e.mu.Unlock()
			return result, err
		}

		if !e.hasPlainKey || now.After(e.hardTTL) || e.usesLeft == 0 {
			e.mu.Unlock()
			callCtx, cancel := e.refreshContext(ctx)
			err := e.refresher.Do(callCtx, e.keyspace, e.keyID, e.refreshOnce)
			cancel()
			if err != nil {
				if _, ok := apierrors.FromError(err); ok {
					return result, err
				}
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					return result, apierrors.New(apierrors.CodeUnlockRequired, "refresh timeout")
				}
				return result, err
			}
			continue
		}

		e.usesLeft--
		result.State = StateWarm
		result.HasPlainKey = true
		result.PlainKey = e.priv32

		shouldBackground := e.shouldScheduleRefreshLocked(now)
		e.mu.Unlock()

		if shouldBackground {
			e.refresher.Go(context.Background(), e.keyspace, e.keyID, e.refreshOnce)
		}
		return result, nil
	}
}

// State 返回当前状态。
func (e *Entry) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

// UsesLeft 返回剩余可用次数。
func (e *Entry) UsesLeft() uint32 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.usesLeft
}

func (e *Entry) ensureValidLocked(now time.Time) error {
	if now.After(e.dekValidUntil) {
		e.toInvalidLocked("DEK expired")
		return apierrors.New(apierrors.CodeUnlockRequired, "dek expired")
	}
	if e.state == StateInvalid {
		return apierrors.New(apierrors.CodeUnlockRequired, "key invalid")
	}
	return nil
}

func (e *Entry) shouldScheduleRefreshLocked(now time.Time) bool {
	lowWater := e.lowWater
	if e.maxUses <= lowWater {
		lowWater = 0
	}
	if lowWater > 0 && e.usesLeft <= lowWater {
		return true
	}
	return now.After(e.softTTL)
}

func (e *Entry) refreshContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	budget := e.refreshBudget
	if budget <= 0 {
		budget = defaultRefreshBudget
	}
	return context.WithTimeout(parent, budget)
}

func (e *Entry) refreshOnce(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.clock.Now()
	if err := e.ensureValidLocked(now); err != nil {
		return err
	}
	needCool := !e.hasPlainKey || now.After(e.hardTTL) || e.usesLeft == 0
	if !needCool && now.Before(e.softTTL) && e.usesLeft > 0 {
		// 仍然足够新鲜，直接返回。
		return nil
	}
	if needCool {
		e.toCoolLocked("refresh")
	}
	return e.rehydrateLocked(ctx, now)
}

func (e *Entry) shouldPrefetch(now time.Time, window time.Duration, lowWater uint32) bool {
	if window <= 0 {
		window = e.softWindow / 2
	}
	if lowWater == 0 {
		lowWater = e.lowWater
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state != StateWarm {
		return false
	}
	computedLowWater := lowWater
	if e.maxUses <= computedLowWater {
		computedLowWater = 0
	}
	if computedLowWater > 0 && e.usesLeft <= computedLowWater {
		return true
	}
	return now.After(e.softTTL.Add(-window))
}

func (e *Entry) rehydrateLocked(ctx context.Context, now time.Time) error {
	if e.rehydrator == nil {
		e.toInvalidLocked("rehydrator missing")
		return apierrors.New(apierrors.CodeUnlockRequired, "rehydrator missing")
	}
	budget := e.refreshBudget
	if budget <= 0 {
		budget = defaultRefreshBudget
	}
	callCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	start := e.clock.Now()
	plain, err := e.rehydrator.Rehydrate(callCtx, e.keyID, e.cipherBlob)
	duration := e.clock.Now().Sub(start)
	e.metrics.observeRehydrate(e.keyspace, duration.Seconds()*1000, err == nil)
	if err != nil {
		e.metrics.incHardExpired(e.keyspace)
		e.toInvalidLocked(fmt.Sprintf("rehydrate failed: %v", err))
		return apierrors.New(apierrors.CodeUnlockRequired, "rehydrate failed")
	}
	e.priv32 = plain
	e.hasPlainKey = true
	e.usesLeft = e.maxUses
	e.softTTL = now.Add(e.softWindow)
	e.hardTTL = now.Add(e.hardWindow)
	e.transitionLocked(e.state, StateWarm)
	return nil
}

func (e *Entry) toCoolLocked(reason string) {
	if e.state == StateCool {
		return
	}
	e.logger.Info("key cache entering COOL", slog.String("key", e.keyID), slog.String("reason", reason))
	e.clearPlainLocked()
	e.transitionLocked(e.state, StateCool)
}

func (e *Entry) toInvalidLocked(reason string) {
	if e.state == StateInvalid {
		return
	}
	e.logger.Warn("key cache invalid", slog.String("key", e.keyID), slog.String("reason", reason))
	e.clearPlainLocked()
	e.transitionLocked(e.state, StateInvalid)
}

func (e *Entry) transitionLocked(from, to State) {
	if from == to {
		return
	}
	if e.metrics != nil {
		e.metrics.updateState(e.enclave, from, to)
	}
	e.state = to
}

func (e *Entry) clearPlainLocked() {
	secureZero(e.priv32[:])
	e.hasPlainKey = false
	e.usesLeft = 0
}

func secureZero(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
	// 防止编译器优化掉填零。
	subtle.ConstantTimeByteEq(buf[0], buf[0])
	runtime.KeepAlive(buf)
}
