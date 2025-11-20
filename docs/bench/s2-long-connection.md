# S2 长连接压测手册

目标：证明 `sign` p99 不受建连影响，`pool_acquire_latency_ms` 保持在 200µs 以内，连接池枯竭告警为零。

## 拓扑
- 父机 Gateway 使用 `internal/infra/enclaveclient` 长连接池（`SIGN_CONN_POOL_MIN/MAX=16/32`）。
- Enclave 端运行 `signer.v1.SignerService`，推荐启用双向流 `SignStream`。
- 压测机可与父机同 VPC，保持 1–2 ms RTT。

## 命令
使用 `ghz` 示例：
```bash
export HOST=$GATEWAY_ADDR:9090
seq 0 5000 | head -n 500 | python - <<'PY'
import os,base64
print('\n'.join(base64.b64encode(os.urandom(32)).decode() for _ in range(500)))
PY > /tmp/digests.txt

ghz --insecure \
  --proto docs/api/proto/signer.proto \
  --call signer.v1.SignerService.SignStream \
  --cacert tls/ca.pem \
  --data-file /tmp/digests.txt \
  -c 500 -z 15m \
  -d '{"key_id":"k-hot","digest":"{{.Data}}"}' \
  $HOST
```

## 采集指标
1. Prometheus `active_conns`, `pool_acquire_latency_ms`, `grpc_stream_resets_total`（标签 `enclave_id`）。
2. 应用指标 `sign_latency_ms{phase}`，确认无突增。
3. `pprof` 或 `top` 观察 CPU < 75%。

## 验收
- `sign.total.p99 < 10ms`，且无 TLS/gRPC 建连 spikes。
- `pool_acquire_latency_ms_p99 < 0.2ms`，`active_conns` 稳定在 16–32 范围。
- `grpc_stream_resets_total` 速率 < 0.1/s，如超限需检查 vsock/网络。
- 压测报告（Markdown + ghz CSV）入库 `docs/bench/reports/`。

## 故障注记
- 若观测到 `pool_exhausted_total`，可临时将 `SIGN_CONN_POOL_MAX` 提升到 48 并执行 `pool.Resize` API。
- 连接重置暴增时，使用 `runbook/conn-pool.md` 中的“断线调试”流程排查 vsock/健康探测。
