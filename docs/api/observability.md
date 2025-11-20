# Observability Spec

- 必须暴露以下指标：
  - `http_grpc_requests_total{route="/create"}` 计数器
  - `sign_latency_ms{phase="create"}` 直方图，用于 `/create` handler 端到端时间（需确保 ≤5ms）
  - `sign_latency_ms{phase="sign"}` 直方图（延用 sign pipeline）
  - `http_grpc_requests_latency_bucket{route="/create"}` 直方图（对齐网关指标）
- `Retry-After` 设置成功写入 header 后，应同步记录 `retry_after_seconds` gauge 方便观测退避窗口。
- 推荐伪代码：

```go
latency := metrics.CreateLatency.Start()
resp := handler(ctx, req)
latency.Observe()
metrics.RequestsTotal.WithLabelValues("/create", "200").Inc()
```

- 日志需包含：`keyId`, `request_id`, `tenant_id(可空)`, `status_code`, `err_code`。
- 指标/日志均须以 request-id 为关联键，便于 trace。
