package enclaveclient

import (
	"os"
	"strconv"
	"time"
)

// Config 控制连接池的全局行为。
type Config struct {
	MinConns            int
	MaxConns            int
	AcquireTimeout      time.Duration
	DialTimeout         time.Duration
	KeepaliveTime       time.Duration
	KeepaliveTimeout    time.Duration
	HealthCheckInterval time.Duration
	ServiceName         string
	Backoff             BackoffConfig
}

// BackoffConfig 决定断线重连指数退避参数。
type BackoffConfig struct {
	Initial time.Duration
	Max     time.Duration
	Jitter  float64
}

// DefaultConfig 返回遵循故事要求的安全默认值。
func DefaultConfig() Config {
	return Config{
		MinConns:            16,
		MaxConns:            32,
		AcquireTimeout:      250 * time.Millisecond,
		DialTimeout:         500 * time.Millisecond,
		KeepaliveTime:       30 * time.Second,
		KeepaliveTimeout:    10 * time.Second,
		HealthCheckInterval: 5 * time.Second,
		ServiceName:         "signer.v1.SignerService",
		Backoff: BackoffConfig{
			Initial: 25 * time.Millisecond,
			Max:     200 * time.Millisecond,
			Jitter:  0.2,
		},
	}
}

// LoadConfigFromEnv 解析环境变量，允许通过 ConfigMap 热更新。
func LoadConfigFromEnv() Config {
	cfg := DefaultConfig()
	if v := readInt("SIGN_CONN_POOL_MIN"); v > 0 {
		cfg.MinConns = v
	}
	if v := readInt("SIGN_CONN_POOL_MAX"); v > 0 {
		cfg.MaxConns = v
	}
	if d := readDuration("SIGN_CONN_POOL_ACQUIRE_TIMEOUT"); d > 0 {
		cfg.AcquireTimeout = d
	}
	if d := readDuration("SIGN_CONN_POOL_DIAL_TIMEOUT"); d > 0 {
		cfg.DialTimeout = d
	}
	if d := readDuration("SIGN_CONN_POOL_KEEPALIVE_TIME"); d > 0 {
		cfg.KeepaliveTime = d
	}
	if d := readDuration("SIGN_CONN_POOL_KEEPALIVE_TIMEOUT"); d > 0 {
		cfg.KeepaliveTimeout = d
	}
	if d := readDuration("SIGN_CONN_POOL_HEALTH_INTERVAL"); d > 0 {
		cfg.HealthCheckInterval = d
	}
	if d := readDuration("SIGN_CONN_POOL_RETRY_INITIAL"); d > 0 {
		cfg.Backoff.Initial = d
	}
	if d := readDuration("SIGN_CONN_POOL_RETRY_MAX"); d > 0 {
		cfg.Backoff.Max = d
	}
	if j := readFloat("SIGN_CONN_POOL_RETRY_JITTER"); j >= 0 {
		cfg.Backoff.Jitter = j
	}
	if service := os.Getenv("SIGN_CONN_POOL_SERVICE"); service != "" {
		cfg.ServiceName = service
	}
	if cfg.MaxConns < cfg.MinConns {
		cfg.MaxConns = cfg.MinConns
	}
	return cfg
}

func readInt(key string) int {
	value := os.Getenv(key)
	if value == "" {
		return 0
	}
	v, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return v
}

func readDuration(key string) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return 0
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0
	}
	return d
}

func readFloat(key string) float64 {
	value := os.Getenv(key)
	if value == "" {
		return -1
	}
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return -1
	}
	return v
}
