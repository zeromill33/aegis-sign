package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	signerv1 "github.com/aegis-sign/wallet/docs/api/gen/go"
	"github.com/aegis-sign/wallet/internal/app/backend/keycache"
	signerapi "github.com/aegis-sign/wallet/internal/api"
	"github.com/aegis-sign/wallet/internal/gateway/unlock"
	"github.com/aegis-sign/wallet/internal/infra/enclaveclient"
	"github.com/aegis-sign/wallet/internal/infra/kms"
	"github.com/aegis-sign/wallet/internal/infra/kms/mockkms"
	"google.golang.org/grpc"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	backend, poolCloser, err := configureEnclaveBackend(logger)
	if err != nil {
		logger.Error("failed to configure enclave backend", "error", err)
		os.Exit(1)
	}
	defer poolCloser()

	unlockResponder, unlockDispatcher, unlockCleanup, err := configureUnlockSystem(logger)
	if err != nil {
		logger.Warn("unlock dispatcher disabled", "error", err)
	} else if unlockCleanup != nil {
		defer unlockCleanup()
	}
	if unlockDispatcher != nil {
		keycache.SetUnlockNotifier(unlock.NewDispatcherNotifier(unlockDispatcher))
	} else {
		keycache.SetUnlockNotifier(nil)
	}

	// HTTP server wiring
	mux := http.NewServeMux()
	signerapi.NewHTTPHandler(backend, unlockResponder).Register(mux)
	if unlockDispatcher != nil {
		mux.Handle("/debug/unlock", unlockDispatcher.DebugHandler())
	}
	httpSrv := &http.Server{
		Addr:    envOrDefault("SIGNER_HTTP_ADDR", ":8080"),
		Handler: mux,
	}

	go func() {
		logger.Info("HTTP server listening", "addr", httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server closed unexpectedly", "error", err)
			stop()
		}
	}()

	// gRPC server wiring (primarily for integration tests)
	grpcAddr := envOrDefault("SIGNER_GRPC_ADDR", ":9090")
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		logger.Error("failed to listen for gRPC", "error", err)
		os.Exit(1)
	}
	grpcSrv := grpc.NewServer()
	signerv1.RegisterSignerServiceServer(grpcSrv, signerapi.NewGRPCServer(backend, unlockResponder))
	go func() {
		logger.Info("gRPC server listening", "addr", grpcAddr)
		if err := grpcSrv.Serve(lis); err != nil {
			logger.Error("grpc server closed unexpectedly", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down servers")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown error", "error", err)
	}
	grpcSrv.GracefulStop()
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func configureUnlockSystem(logger *slog.Logger) (*signerapi.UnlockResponder, *unlock.Dispatcher, func(), error) {
	maxQueue := envInt("UNLOCK_MAX_QUEUE", 2048)
	workers := envInt("UNLOCK_WORKERS", 16)
	rateLimit := envFloat("UNLOCK_RATE_LIMIT", 0)
	rateBurst := envInt("UNLOCK_RATE_BURST", 1)
	cfg := unlock.Config{
		MaxQueue:  maxQueue,
		Workers:   workers,
		RateLimit: rateLimit,
		RateBurst: rateBurst,
		Logger:    logger,
	}
	executor, execErr := configureKMSEnclaveExecutor(logger)
	if execErr != nil {
		logger.Warn("unlock executor fallback to noop", "error", execErr)
		executor = unlock.NewNoopExecutor(logger)
	}
	dispatcher, err := unlock.NewDispatcher(cfg, executor)
	if err != nil {
		return nil, nil, nil, err
	}
	responder := signerapi.NewUnlockResponder(signerapi.UnlockResponderConfig{
		Queue:    dispatcher,
		Keyspace: envOrDefault("UNLOCK_KEYSPACE", "default"),
		MinRetry: envDuration("UNLOCK_RETRY_MIN_MS", 50*time.Millisecond),
		MaxRetry: envDuration("UNLOCK_RETRY_MAX_MS", 200*time.Millisecond),
	})
	cleanup := func() { dispatcher.Close() }
	return responder, dispatcher, cleanup, nil
}

func configureKMSEnclaveExecutor(logger *slog.Logger) (unlock.Executor, error) {
	mockKey := os.Getenv("UNLOCK_KMS_MOCK_KEY")
	if strings.TrimSpace(mockKey) == "" {
		return nil, fmt.Errorf("UNLOCK_KMS_MOCK_KEY not set")
	}
	provider := mockkms.NewStaticProvider([]byte(mockKey))
	attestor := mockkms.NewStaticAttestor(nil)
	client, err := kms.NewClient(provider, attestor, kms.Config{Logger: logger})
	if err != nil {
		return nil, err
	}
	executor := unlock.NewKMSEnclaveExecutor(client, logger)
	return executor, nil
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			return parsed
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			return time.Duration(parsed) * time.Millisecond
		}
	}
	return def
}

func configureEnclaveBackend(logger *slog.Logger) (signerapi.Backend, func(), error) {
	targets, err := parseEnclaveTargets(os.Getenv("SIGNER_ENCLAVES"))
	if err != nil {
		return nil, func() {}, fmt.Errorf("failed to parse SIGNER_ENCLAVES: %w", err)
	}
	poolCfg := enclaveclient.LoadConfigFromEnv()
	pool, err := enclaveclient.NewPool(poolCfg, enclaveclient.WithLogger(logger))
	if err != nil {
		return nil, func() {}, err
	}
	for _, target := range targets {
		pool.RegisterTarget(target)
	}
	selector, err := signerapi.NewStickySelector(targetIDs(targets))
	if err != nil {
		pool.Close()
		return nil, func() {}, err
	}
	backend, err := signerapi.NewEnclaveBackend(pool, selector)
	if err != nil {
		pool.Close()
		return nil, func() {}, err
	}
	cleanup := func() { _ = pool.Close() }
	return backend, cleanup, nil
}

func parseEnclaveTargets(raw string) ([]enclaveclient.Target, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("SIGNER_ENCLAVES is required (format id=endpoint,id2=endpoint2)")
	}
	parts := strings.Split(raw, ",")
	var targets []enclaveclient.Target
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, endpoint, found := strings.Cut(part, "=")
		if !found || strings.TrimSpace(id) == "" || strings.TrimSpace(endpoint) == "" {
			return nil, fmt.Errorf("invalid enclave entry: %s", part)
		}
		targets = append(targets, enclaveclient.Target{ID: strings.TrimSpace(id), Endpoint: strings.TrimSpace(endpoint)})
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no enclave targets provided")
	}
	return targets, nil
}

func targetIDs(targets []enclaveclient.Target) []string {
	ids := make([]string, len(targets))
	for i, t := range targets {
		ids[i] = t.ID
	}
	return ids
}
