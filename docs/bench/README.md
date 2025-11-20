# 压测说明（Performance Gate）

目标：并发 c=500，持续 15 分钟，`p99 < 10ms`、丢弃率 < 0.5%、CPU < 75%、`engine_q_depth_avg < 1`。

## 工具选择
- gRPC：`ghz`（推荐）或自研压测器（支持双向流）
- HTTP：`hey`/`wrk`（仅用于对比）

## 准备
- 服务地址：`$HOST:9090`（gRPC）/ `$HOST`（HTTP）
- 先创建密钥：`grpcurl -plaintext $HOST:9090 signer.v1.SignerService/Create '{}'`
- 生成 32B 摘要（示意）：
```bash
python - <<'PY'
import os, binascii
print(binascii.hexlify(os.urandom(32)).decode())
PY
```

## gRPC 单次 Sign（基线）
```bash
ghz --insecure \
  --proto docs/api/proto/signer.proto \
  --call signer.v1.SignerService.Sign \
  -c 500 -z 15m \
  -d '{"key_id":"k1","digest":"<base64-32B>":""}' \
  $HOST:9090
```
> 注：digest 需传 base64；或扩展 ghz 模板生成 32B 随机摘要（推荐自研压测器）。

## gRPC 双向流 Sign（推荐）
- 使用自研压测器，启用 `maxBatch=32、maxWait=1–2ms` 的微批聚合
- 指标采集：延迟分位、失败/丢弃率、CPU/内存、队列深度

## 报告与归档
- 输出：Markdown 报告 + 原始 CSV/JSON 数据
- 维度：N(worker)=8/12/16、batch on/off、实例家族（x86/ARM）
- 模板：`docs/bench/report-template.md`

## 常见问题
- p99 受连接建连影响：检查长连接与连接池配置（S2/S10）
- NEEDS_UNLOCK_rate 偏高：检查单航班刷新与预刷新参数（S4）
- CPU > 75%：调整 worker 数、批处理策略、对象池与 GC 压力

## S4 单航班刷新专项压测
- 场景：随机 10% key 在 90s 内触发 soft 过期，1% 触发 hard 过期，验证 `singleflight_waiters`、`singleflight_wait_timeout_total` 与 `rehydrate_latency_ms`。
- 建议命令：
```bash
ghz --insecure \
  --proto docs/api/proto/signer.proto \
  --call signer.v1.SignerService.Sign \
  -c 500 -z 15m \
  -d @docs/bench/payloads/s4.json \
  --metadata "refresh_mode=singleflight" \
  $HOST:9090
```
- 验收：`rehydrate_latency_ms{keyspace}` p95 < 2ms；`singleflight_waiters` 稳态 < 128；`singleflight_wait_timeout_total` 每分钟 < 10；`NEEDS_UNLOCK_rate < 0.5%`。
- 模拟建议：将 `SIGN_TTL_SOFT_PLAIN`/`SIGN_TTL_HARD_PLAIN` 临时降到 30s/31s，并把 `SIGN_REHYDRATE_WAIT_BUDGET_MS` 写入 ConfigMap 以覆盖默认 3ms，这样可快速观察 soft/hard 过期触发；压测完成后立即恢复生产值。
- 记录输出：
  ```bash
  ghz ... --format json > docs/bench/reports/s4-$(date +%Y%m%d%H%M).json
  ```
  同时抓取 Prometheus 快照：
  ```bash
  promtool query instant http://prometheus:9090 \
    'singleflight_waiters{keyspace="prod"}' > docs/bench/reports/s4-waiters.txt
  ```
- 指标观察：按照 `docs/runbook/key-cache.md` 的 PromQL 示例在 Grafana 建立 “Singleflight Health” dashboard，将压测期间的 p95、waiters、timeout 等曲线截图归档至 `docs/bench/reports/`。
