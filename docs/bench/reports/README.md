# 压测报告归档（S4/Unlock）

- S4：运行 `ghz`/Prometheus 查询后，请将结果命名为 `s4-<timestamp>.json`、`s4-waiters-<timestamp>.txt` 存放于此，并在 PR 中附带关键截图（Grafana dashboard）。
- Unlock Drill：`make unlock-drill` 会生成/更新 `unlock-drill.md`；请附带 `ghz` 输出、`/debug/unlock` 截图与 `retry-after-ms` 指标趋势。
