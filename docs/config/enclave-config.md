# Enclave 配置基线（可通过 ConfigMap/ENV 热更新）

建议默认值（根据 SLO 可调整）：
- Enclave：vCPU=12、内存=8GiB（参考 docs/config/nitro-allocator.yaml）
- 父机↔Enclave 长连接：16–32，KeepAlive=30s/10s，健康探测 5s 一次
- PlainKey：`ttl_soft=15m`、`ttl_hard=16m`、`maxUses=1_000_000`
- DEK：`ttl_soft=55m`、`ttl_hard=60m`
- 刷新预算：`signWaitBudget=2–3ms`、`hard_refresh_budget=3–5ms`
- 预刷新：`refreshWindow=2m`、`lowWaterMark=500 uses`、`jitter=±10%`
- 重试：`Retry-After=50–200ms`（带抖动）

建议通过 K8s ConfigMap 暴露为环境变量：
```
SIGN_CONN_POOL_MIN=16
SIGN_CONN_POOL_MAX=32
SIGN_CONN_POOL_ACQUIRE_TIMEOUT=250ms
SIGN_CONN_POOL_DIAL_TIMEOUT=500ms
SIGN_CONN_POOL_HEALTH_INTERVAL=5s
SIGN_CONN_POOL_RETRY_INITIAL=25ms
SIGN_CONN_POOL_RETRY_MAX=200ms
SIGN_CONN_POOL_RETRY_JITTER=0.2
SIGN_CONN_POOL_SERVICE=signer.v1.SignerService
SIGN_TTL_SOFT_PLAIN=15m
SIGN_TTL_HARD_PLAIN=16m
SIGN_TTL_SOFT_DEK=55m
SIGN_TTL_HARD_DEK=60m
SIGN_REHYDRATE_WAIT_BUDGET_MS=3
SIGN_HARD_REFRESH_BUDGET_MS=5
SIGN_REFRESH_WINDOW=2m
SIGN_REFRESH_LOW_WATER=500
SIGN_REFRESH_JITTER=0.1
```

对应 ConfigMap 片段（供父机 Deployment 引用）：

```
apiVersion: v1
kind: ConfigMap
metadata:
  name: signer-conn-pool
data:
  SIGN_CONN_POOL_MIN: "16"
  SIGN_CONN_POOL_MAX: "32"
  SIGN_CONN_POOL_ACQUIRE_TIMEOUT: "250ms"
  SIGN_CONN_POOL_DIAL_TIMEOUT: "500ms"
  SIGN_CONN_POOL_HEALTH_INTERVAL: "5s"
  SIGN_CONN_POOL_RETRY_INITIAL: "25ms"
  SIGN_CONN_POOL_RETRY_MAX: "200ms"
  SIGN_CONN_POOL_RETRY_JITTER: "0.2"
  SIGN_CONN_POOL_SERVICE: "signer.v1.SignerService"
```

> 以上变量由 `internal/infra/enclaveclient.LoadConfigFromEnv` 解析，热更新时通过 ConfigMap reload + SIGHUP 即可生效。

## Enclave 列表配置

入口进程需通过 `SIGNER_ENCLAVES` 指定目标 Enclave 与访问地址，格式示例：

```
SIGNER_ENCLAVES=enclave-a=vsock://3:8001,enclave-b=unix:///var/run/enclave-b.sock,enclave-c=10.0.0.12:9443
```

- `vsock://<cid>:<port>`：适用于父机→Enclave 直连，CID 通常为 3。
- `unix:///path/to/socket`：通过 vsock-proxy 暴露的本地 unix socket。
- `host:port`：常规 TCP (H2) 直连。

`cmd/signer-api` 会读取该变量，依次为连接池注册 Target，并通过 `StickySelector` 按 keyId 做一致性 hash 分发。
