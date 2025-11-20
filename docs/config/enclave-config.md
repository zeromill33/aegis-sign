# Enclave 配置基线（可通过 ConfigMap/ENV 热更新）

建议默认值（根据 SLO 可调整）：
- Enclave：vCPU=12、内存=8GiB（参考 docs/config/nitro-allocator.yaml）
- 父机↔Enclave 长连接：16–32
- PlainKey：`ttl_soft=15m`、`ttl_hard=16m`、`maxUses=1_000_000`
- DEK：`ttl_soft=55m`、`ttl_hard=60m`
- 刷新预算：`signWaitBudget=2–3ms`、`hard_refresh_budget=3–5ms`
- 预刷新：`refreshWindow=2m`、`lowWaterMark=500 uses`、`jitter=±10%`
- 重试：`Retry-After=50–200ms`（带抖动）

建议通过 K8s ConfigMap 暴露为环境变量：
```
SIGN_CONN_POOL_MIN=16
SIGN_CONN_POOL_MAX=32
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

