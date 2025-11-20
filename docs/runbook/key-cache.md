# Key Cache Runbook

## 关键监控指标
- `rehydrate_total{keyspace}`：本地再水合总次数，用于计算命中率。
- `rehydrate_fail_total{keyspace}`：再水合失败计数，连续非零表示 DEK/KMS 异常。
- `rehydrate_latency_ms{keyspace}`：直方图，重点关注 `p95 < 2ms`。
- `singleflight_waiters{keyspace}`：当前等待同一 key 刷新的 goroutine 数；>128 说明刷新阻塞或热点 key 失控。
- `singleflight_wait_timeout_total{keyspace}`：等待预算（默认 3ms）耗尽次数，连续增大需检查 rehydrator 延迟。
- `prefetch_scan_total` / `prefetch_trigger_total{keyspace}` / `prefetch_skipped_total`：后台预刷新扫描频度、触发数量与因 `maxInFlight` 被跳过的 key 数。

## 告警建议
1. `rehydrate_fail_total` 在 5 分钟内递增 > 10：触发 **UNLOCK_REQUIRED** 路径联动检查 KMS、密文 Blob。
2. `singleflight_waiters > 128` 或 `singleflight_wait_timeout_total` 1 分钟内 > 50：检查 rehydrator 是否超时、`signWaitBudget` 是否需要放宽。
3. `prefetch_skipped_total` 持续递增：说明 `maxInFlight` 过小或扫描周期过长，应扩容或缩短 `refreshWindow`。
4. `NEEDS_UNLOCK_rate > 0.5%`：与 Story 2.3 异步解锁流程联动，排查是否有大量 key 进入 INVALID。

## 排障步骤
1. 查询 `singleflight_waiters{keyspace}` 与 `singleflight_wait_timeout_total{keyspace}`，定位热点 key，核对日志 `"refresh wait timeout"`。
2. 检查 `prefetch_trigger_total` 是否停滞；如停滞，确认预刷新器是否仍在运行以及 `refreshWindow/LowWater` 配置。
3. 若 `rehydrate_fail_total` 升高，查看 `key cache invalid` 日志确认 DEK/密文状态，再触发 Story 2.3 的异步解锁脚本。
4. 压测或生产突发时，可暂时增大 `maxInFlight` 并观察 `prefetch_skipped_total` 是否下降。

## 自愈/操作
- `make bench-s4`（示意脚本）或参考 `docs/bench/README.md` 的 S4 场景复现刷新流程，确认 `rehydrate_latency_ms p95 < 2ms`。
- 手动执行预刷新：调用 Key Manager 的 `ForceRefresh(keyID)`，该命令内部复用 `RefreshGroup.Do`，具备单航班保护。
- 如需禁用预刷新器，可在配置中将 `maxInFlight=0`；务必同时收紧告警阈值以防软 TTL 集中触发。

## PromQL/Grafana 示例
- “Soft 过期等待数”面板：
  ```promql
  max_over_time(singleflight_waiters{keyspace="prod"}[1m])
  ```
- “等待超时速率”告警：
  ```promql
  rate(singleflight_wait_timeout_total{keyspace="prod"}[5m])
  ```
  触发条件：> 0.2/s 持续 5 分钟。
- “预刷新覆盖率”观察：
  ```promql
  rate(prefetch_trigger_total{keyspace="prod"}[15m]) / rate(prefetch_scan_total[15m])
  ```
- “再水合 p95”面板：
  ```promql
  histogram_quantile(0.95, sum(rate(rehydrate_latency_ms_bucket{keyspace="prod"}[5m])) by (le))
  ```

Grafana 建议：
1. 创建 “Singleflight Health” dashboard，包含上方四个查询并在图例标注阈值（128 waiters、0.2/s 超时、p95 < 2ms）。
2. 将 `singleflight_waiters` 设为瞬时条形图，直观显示热点 key。
3. 将 `prefetch_trigger_total`、`prefetch_skipped_total` 放在同一 panel，便于观察扫描是否饱和。
