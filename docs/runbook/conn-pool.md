# 连接池 Runbook

## 1. 快速扩缩容
- 修改 ConfigMap `signer-conn-pool` 的 `SIGN_CONN_POOL_MIN/MAX`，滚动重启父机 Pod。
- 或通过运维接口调用 `Pool.Resize(min,max)`（`internal/infra/enclaveclient` 提供）。
- 验证 `active_conns{enclave}` 与期望一致，确保 `pool_acquire_latency_ms` 下降。

## 2. 健康探测/熔断
- 指标 `grpc_stream_resets_total` 持续上升：检查 Enclave vsock/代理。
- 使用 `Drain(enclaveID)` 摘除异常 Enclave，待排查后重新 `RegisterTarget`。
- `state=degraded` 时观察 `breaker.Timestamp`，冷却 1s 会自动恢复。

## 3. 断线自愈
- 收集日志 `enclave health degraded` 与 `open connection failed`，确认是否在 200ms 内重连。
- 如需人为介入，可执行：
  1. `Drain(enclaveID)`
  2. 修复 vsock/网络
  3. `RegisterTarget` + `EnsureMin()`（重启组件或发送 SIGHUP）。

## 4. 回滚至短连接模式
- 将 `SIGN_CONN_POOL_MIN/MAX` 设置为 1，关闭 `SIGN_CONN_POOL_HEALTH_INTERVAL`（设为 `0s`）。
- 重启父机后即退化为逐请求建连，便于定位问题。

## 5. 告警阈值建议
- `active_conns < MIN*0.8`：连接池枯竭，级别 Warning。
- `pool_acquire_latency_ms_p95 > 0.2`：明显阻塞，级别 Major。
- `grpc_stream_resets_total` 每分钟 > 10：网络或 Enclave 故障。

> Runbook 依赖 `internal/infra/enclaveclient` 暴露的日志与指标，确保 Prometheus 抓取 `/metrics` 并在 Grafana 中预置看板。
