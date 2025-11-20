package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	signerv1 "github.com/aegis-sign/wallet/docs/api/gen/go"
	signerapi "github.com/aegis-sign/wallet/internal/api"
	"github.com/aegis-sign/wallet/internal/infra/enclaveclient"
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

	// HTTP server wiring
	mux := http.NewServeMux()
	signerapi.NewHTTPHandler(backend).Register(mux)
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
	signerv1.RegisterSignerServiceServer(grpcSrv, signerapi.NewGRPCServer(backend))
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
