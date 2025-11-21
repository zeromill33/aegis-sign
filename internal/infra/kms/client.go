package kms

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"sync"
	"time"
)

// Provider 定义底层 KMS 的能力（Decrypt/GenerateDataKey）。
type Provider interface {
	Decrypt(ctx context.Context, req DecryptRequest) ([]byte, error)
	GenerateDataKey(ctx context.Context, req GenerateDataKeyRequest) ([]byte, error)
}

// Attestor 负责获取并校验 Nitro Attestation 文档。
type Attestor interface {
	Document(ctx context.Context) ([]byte, error)
	Verify(document []byte) error
}

// Config 控制 retry/attestation 行为。
type Config struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	JitterFactor   float64
	CacheTTL       time.Duration
	Logger         *slog.Logger
}

// DecryptRequest 携带解锁上下文。
type DecryptRequest struct {
	KeyID       string
	Ciphertext  []byte
	Attestation []byte
}

// GenerateDataKeyRequest 用于生成新的 DEK。
type GenerateDataKeyRequest struct {
	KeyID       string
	Attestation []byte
}

// Client 封装所有 KMS 调用逻辑。
type Client struct {
	provider Provider
	attestor Attestor
	cfg      Config

	cacheMu sync.Mutex
	cache   attestationCache

	randMu sync.Mutex
	rnd    *rand.Rand
}

// attestationCache 缓存 document 及过期时间。
type attestationCache struct {
	doc      []byte
	expireAt time.Time
}

// NewClient 构造 Client。
func NewClient(provider Provider, attestor Attestor, cfg Config) (*Client, error) {
	if provider == nil || attestor == nil {
		return nil, errors.New("provider and attestor are required")
	}
	normalized := cfg
	if normalized.MaxAttempts <= 0 {
		normalized.MaxAttempts = 3
	}
	if normalized.InitialBackoff <= 0 {
		normalized.InitialBackoff = 50 * time.Millisecond
	}
	if normalized.MaxBackoff <= 0 {
		normalized.MaxBackoff = time.Second
	}
	if normalized.JitterFactor <= 0 {
		normalized.JitterFactor = 0.2
	}
	if normalized.CacheTTL <= 0 {
		normalized.CacheTTL = 5 * time.Minute
	}
	if normalized.Logger == nil {
		normalized.Logger = slog.Default()
	}
	return &Client{
		provider: provider,
		attestor: attestor,
		cfg:      normalized,
		rnd:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}, nil
}

// Decrypt 调用 provider 并附带 attestation。
func (c *Client) Decrypt(ctx context.Context, keyID string, ciphertext []byte) ([]byte, error) {
	return c.retry(ctx, func(doc []byte) ([]byte, error) {
		return c.provider.Decrypt(ctx, DecryptRequest{KeyID: keyID, Ciphertext: ciphertext, Attestation: doc})
	})
}

// GenerateDataKey 生成新的 DEK。
func (c *Client) GenerateDataKey(ctx context.Context, keyID string) ([]byte, error) {
	return c.retry(ctx, func(doc []byte) ([]byte, error) {
		return c.provider.GenerateDataKey(ctx, GenerateDataKeyRequest{KeyID: keyID, Attestation: doc})
	})
}

func (c *Client) retry(ctx context.Context, fn func([]byte) ([]byte, error)) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= c.cfg.MaxAttempts; attempt++ {
		doc, err := c.attestation(ctx)
		if err != nil {
			lastErr = err
			c.logWarn("fetch attestation failed", attempt, err)
		} else {
			result, execErr := fn(doc)
			if execErr == nil {
				return result, nil
			}
			lastErr = execErr
			c.logWarn("kms call failed", attempt, execErr)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.backoffDuration(attempt)):
		}
	}
	if lastErr == nil {
		lastErr = errors.New("kms retry exhausted")
	}
	return nil, lastErr
}

func (c *Client) attestation(ctx context.Context) ([]byte, error) {
	c.cacheMu.Lock()
	if doc := c.cache.doc; doc != nil && time.Now().Before(c.cache.expireAt) {
		cached := append([]byte(nil), doc...)
		c.cacheMu.Unlock()
		return cached, nil
	}
	c.cacheMu.Unlock()

	doc, err := c.attestor.Document(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.attestor.Verify(doc); err != nil {
		return nil, err
	}
	c.cacheMu.Lock()
	c.cache.doc = append([]byte(nil), doc...)
	c.cache.expireAt = time.Now().Add(c.cfg.CacheTTL)
	c.cacheMu.Unlock()
	return doc, nil
}

func (c *Client) backoffDuration(attempt int) time.Duration {
	delay := c.cfg.InitialBackoff * time.Duration(1<<(attempt-1))
	if delay > c.cfg.MaxBackoff {
		delay = c.cfg.MaxBackoff
	}
	jitter := time.Duration(float64(delay) * c.cfg.JitterFactor)
	if jitter <= 0 {
		return delay
	}
	c.randMu.Lock()
	delta := time.Duration(c.rnd.Int63n(int64(2*jitter)+1)) - jitter
	c.randMu.Unlock()
	delay += delta
	if delay < 0 {
		return 0
	}
	return delay
}

func (c *Client) logWarn(msg string, attempt int, err error) {
	if c.cfg.Logger == nil {
		return
	}
	c.cfg.Logger.Warn(msg, slog.Int("attempt", attempt), slog.Any("err", err))
}
