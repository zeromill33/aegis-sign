# Stories Backlog（优先顺序）

优先交付范围：代码开发、相关 YAML 配置、部署文档、压测。高可用/看板/演练延后。

## 第一批（Now）
- 1.1 API 合约与最小消息体 — docs/stories/1.1.api-contract.md
- 1.2 gRPC 长连接与连接池基础 — docs/stories/1.2.grpc-long-connections.md
- 2.1 Key 缓存状态机（WARM/COOL/INVALID） — docs/stories/2.1.key-cache-state-machine.md
- 2.2 单航班刷新（single-flight） — docs/stories/2.2.singleflight-refresh.md
- 2.3 异步解锁路径（UNLOCK_REQUIRED） — docs/stories/2.3.async-unlock-path.md
- 3.1 libsecp256k1 接入与基准 — docs/stories/3.1.libsecp256k1-integration.md
- 4.1 Worker 池（vCPU=worker）+ LockOSThread — docs/stories/4.1.worker-pool-lockosthread.md
- 5.1 粘性路由（keyId→enclave 一致性哈希） — docs/stories/5.1.sticky-routing.md
- 5.2 父机↔Enclave 连接池 16–32 — docs/stories/5.2.conn-pool.md
- 6.1 Nitro allocator 与 EKS 资源隔离（含 YAML/部署文档） — docs/stories/6.1.eks-nitro-allocator-and-cpu-isolation.md
- 8.1 压测工具与基线报告（Performance Gate） — docs/stories/8.1.perf-gate-benchmark.md

## 下一批（Later）
- 7.x 可观测性与看板（E7 S12）
- 9.x 失败处理与 Runbook（E9 S14/S15）
- 10.x 安全合规护栏（E10 S16/S17）

说明：每个 Story 包含 AC、任务清单、测试与交付物要求。完成后统一 DoD：代码合入、测试通过、文档可复现、可观测项补齐、回滚方案明确。
