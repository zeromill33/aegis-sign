# Stories：create/sign 核心路径优化（基于最新 BMAD）
**版本**：v1.0  
**范围**：仅 `create` 与 `sign` 核心路径（不含明文导入/导出、多租户）  
**总体目标（SLO）**：单父机并发 ≥ **500**，`sign` 端到端 **p99 < 10 ms**；热路径 **不走 KMS**；错误率 < **0.1%**。

---

## 目录
- [Epic E1：对外接口与最小消息体（只传 32B 摘要，长连接）](#epic-e1)
- [Epic E2：两级缓存 + 软/硬 TTL + 单航班刷新（single-flight）](#epic-e2)
- [Epic E3：签名引擎（libsecp256k1）与批处理](#epic-e3)
- [Epic E4：并发模型（vCPU=worker）](#epic-e4)
- [Epic E5：vsock/gRPC 池化与粘性路由](#epic-e5)
- [Epic E6：资源与调度（EKS + allocator）](#epic-e6)
- [Epic E7：可观测性与 SLO 守护](#epic-e7)
- [Epic E8：压测与验收（Performance Gate）](#epic-e8)
- [Epic E9：过期/失败处理与 Runbook](#epic-e9)
- [Epic E10：安全与合规护栏](#epic-e10)
- [统一定义与附录](#统一定义与附录)

---

## <a id="epic-e1"></a>Epic E1：对外接口与最小消息体（只传 32B 摘要，长连接）

### S1：定义并落地 `create`/`sign` API 合约
**As a** 网关/调用方  
**I want** 使用统一且稳定的 `create`/`sign` 接口  
**so that** 能以最小载荷调用签名并便于后续优化/观测。

- **验收标准（Gherkin）**
  - Given 已部署网关与 Enclave
  - When 调用 `POST /create`
  - Then 返回 `{keyId, publicKey, address?}` 且响应 ≤ **5 ms**（不含后台持久化）
  - And `POST /sign` 仅接受 `digest(32B)`，非 32B 直接 400/INVALID_ARGUMENT
- **技术任务**
  - 定义 Proto/OpenAPI；生成 Go stub；统一错误码（`UNLOCK_REQUIRED` / `RETRY_LATER` / `INVALID_KEY`）
  - 输入校验与安全审计字段（request-id、tenant-id 预留但禁用）
- **可观测性**：`http_grpc_requests_total`、4xx/5xx、`sign_latency_ms{phase}`  
- **依赖**：无  
- **优先级/预估**：Must / 3 SP

### S2：网关→Enclave gRPC/HTTP2 **长连接**（双向流式优先）
- **目标**：消除建连/队首阻塞引入的长尾。
- **验收标准**
  - 每个 Enclave 维持 **16–32** 条长连接；KeepAlive/健康探测开启；断线重连 < **200 ms**
  - `sign` p99 不出现连接建连开销；流复用无 HOL
- **技术任务**
  - 连接池实现（并发安全）；指数退避重连；`MaxConcurrentStreams`、deadline、心跳
- **可观测性**：`active_conns`, `grpc_stream_resets`, `pool_acquire_latency_ms`  
- **依赖**：S1  
- **优先级/预估**：Must / 5 SP

---

## <a id="epic-e2"></a>Epic E2：两级缓存 + 软/硬 TTL + 单航班刷新（single-flight）

### S3：实现 **WARM/COOL/INVALID** 状态机与 TTL/Uses 语义
- **目标**：PlainKey 过期/用尽不触发 KMS，DEK 有效时本地再水合。
- **验收标准**
  - WARM：直接签名；COOL：本地再水合成功率 > **99.5%**；INVALID：热路径不等待 KMS
  - `hard_expired_rejections_total ≈ 0`（除演练）
- **技术任务**
  - Entry：`Priv32, UsesLeft, SoftTTL, HardTTL, DEK.ValidUntil`
  - 到期清零与原子状态切换；密文 Blob 在 Enclave 保持只读副本
- **可观测性**：`key_cache_state{WARM|COOL|INVALID}`  
- **依赖**：无  
- **优先级/预估**：Must / 5 SP

### S4：**单航班刷新**（soft 过期后台刷新，hard 过期限时刷新）
- **目标**：避免羊群效应与击穿；控制 p99。
- **验收标准**
  - soft 过期：允许签名，同时触发 single-flight 后台刷新
  - hard 过期：**≤ 3 ms** 内完成再水合；超时立即返回 `UNLOCK_REQUIRED`
  - `rehydrate_latency_ms p95 < 2 ms`；`NEEDS_UNLOCK_rate < 0.5%`
- **技术任务**
  - per-key single-flight；`signWaitBudget=2–3 ms` 等待在飞刷新
  - 预刷新器：`refreshWindow=2m`，`lowWaterMark=500 uses`，`jitter=±10%`
- **可观测性**：`rehydrate_total/fail/latency_ms`, `singleflight_waiters`  
- **依赖**：S3  
- **优先级/预估**：Must / 8 SP

### S5：**UNLOCK_REQUIRED** 异步解锁路径（冷路径 KMS）
- **目标**：DEK 失效时不阻塞热路径。
- **验收标准**
  - 网关收到 `UNLOCK_REQUIRED` 后**异步**解锁；客户端重试间隔 **50–200 ms**（带抖动）
  - 解锁失败重试与告警齐全；不触发级联超时
- **技术任务**
  - 网关解锁队列；并发/速率限制；幂等
  - KMS+Attestation 客户端封装；重试退避
- **可观测性**：`unlock_bg_rate`, `unlock_fail_total`, `unlock_latency_ms`  
- **依赖**：S2  
- **优先级/预估**：Must / 5 SP

---

## <a id="epic-e3"></a>Epic E3：签名引擎（libsecp256k1）与批处理

### S6：接入 **libsecp256k1**（上下文一次性初始化）
- **验收标准**
  - 进程启动仅初始化一次 `SECP256K1_CONTEXT_SIGN`；线程内复用
  - 单签名计算 **≤ 0.30 ms**（目标机型基线）
- **技术任务**
  - cgo 封装 `Sign`, `RecoverableSign`；编译 `-O3 -march=native`；开启汇编优化
  - RFC6979 默认；可选 recId
- **可观测性**：`sign_cpu_us`, `engine_init_ms`  
- **依赖**：无  
- **优先级/预估**：Must / 5 SP

### S7：**微批**与 C-shim `SignBatch()`
- **验收标准**
  - `maxBatch=32, maxWait=1–2 ms` 时，cgo 跨界次数下降 ≥ **80%**，p95 不退化
- **技术任务**
  - C 层 `SignBatch(digests[], priv)`；Go 层 MPSC 聚合
  - `noWait` 旁路低延迟请求
- **可观测性**：`batch_size_hist`, `cgo_calls_per_sec`  
- **依赖**：S6  
- **优先级/预估**：Should / 8 SP

---

## <a id="epic-e4"></a>Epic E4：并发模型（vCPU=worker）

### S8：Worker 池（N=Enclave vCPU）+ `LockOSThread`
- **验收标准**
  - N=12（基线）时，稳态 CPU < **75%**，`engine_q_depth_avg < 1`
- **技术任务**
  - 每 worker 固定 context，`LockOSThread()`；MPSC 队列；队满快速失败
- **可观测性**：`engine_q_depth`, `worker_busy_ratio`  
- **依赖**：S6  
- **优先级/预估**：Must / 5 SP

---

## <a id="epic-e5"></a>Epic E5：vsock/gRPC 池化与粘性路由

### S9：**一致性哈希**实现 `keyId → enclave` 粘性路由
- **验收标准**
  - 缓存命中率 > **95%**；跨 Enclave 命中率 < **5%**
- **技术任务**
  - Hash ring（虚拟节点、在线变更）；健康摘除/恢复；灰度迁移
- **可观测性**：`sticky_hit_rate`, `cross_enclave_route_total`  
- **依赖**：S2、S3  
- **优先级/预估**：Must / 5 SP

### S10：父机↔Enclave 连接池 **16–32** & KeepAlive
- **验收标准**
  - `pool_exhausted_total ≈ 0`；`pool_acquire_p95 < 200 µs`
- **技术任务**
  - 池大小自适应（基于 inflight）；错误分级重连
- **可观测性**：`active_conns`, `pool_acquire_latency_ms`  
- **依赖**：S2  
- **优先级/预估**：Must / 3 SP

---

## <a id="epic-e6"></a>Epic E6：资源与调度（EKS + allocator）

### S11：Nitro allocator 与 K8s CPU 隔离
- **验收标准**
  - `/etc/nitro_enclaves/allocator.yaml`: `cpu_count=12`, `memory_mib=8192`
  - 关键 Pod **Guaranteed QoS**（`requests==limits`）；启用 **CPU Manager static**
- **技术任务**
  - 节点池 taints/labels；亲和/反亲和；hugepages（如需）
- **可观测性**：节点/Pod 资源利用、抢占告警  
- **依赖**：无  
- **优先级/预估**：Must / 3 SP

---

## <a id="epic-e7"></a>Epic E7：可观测性与 SLO 守护

### S12：指标/日志/追踪 & 看板/告警
- **验收标准**
  - 指标：`sign_latency_ms{phase}`、`key_cache_state`、`rehydrate_*`、`unlock_*`、`rng_source`、`grpc_*`
  - 告警：`p99>10ms`（5 分钟）、`NEEDS_UNLOCK_rate>0.5%`、`rng_current!=nsm-hwrng`
  - Grafana 看板与 OTel trace 贯通
- **技术任务**
  - 指标中间件；日志结构化；看板 JSON 入库
- **依赖**：S1–S5  
- **优先级/预估**：Must / 5 SP

---

## <a id="epic-e8"></a>Epic E8：压测与验收（Performance Gate）

### S13：压测工具与基线报告
- **验收标准**
  - 并发 `c=500` 持续 15 分钟：`p99 < 10 ms`、丢弃率 < **0.5%**、CPU < **75%**、`engine_q_depth_avg < 1`
  - A/B：`N(worker)=8/12/16`、`batch on/off`、实例家族（x86/ARM）报告归档
- **技术任务**
  - 压测器（流式/批量 + digest 生成）；基准脚本与报告模板
- **依赖**：S6–S10  
- **优先级/预估**：Must / 8 SP

---

## <a id="epic-e9"></a>Epic E9：过期/失败处理与 Runbook

### S14：`UNLOCK_REQUIRED`/`RETRY_LATER` 语义 & 网关重试
- **验收标准**
  - 网关本地快速重试 1–2 次（总预算 ≤ **200 ms**）；失败返回 `Retry-After`
  - 无重试风暴（全局速率限制）
- **技术任务**
  - 快速失败路径；指数退避；限流与熔断
- **可观测性**：`retry_total/rate`, `429/503_total`  
- **依赖**：S5  
- **优先级/预估**：Must / 3 SP

### S15：Runbook & 演练（TTL/DEK/RNG/KMS 故障）
- **验收标准**
  - 每月演练：DEK 批量软过期、KMS 不可用、RNG 异常；均在 SLO 内恢复
- **技术任务**
  - Runbook 文档与脚本；演练记录与纠偏项
- **依赖**：S3–S5、S12  
- **优先级/预估**：Should / 3 SP

---

## <a id="epic-e10"></a>Epic E10：安全与合规护栏

### S16：RNG 健康与启动自检（nsm-hwrng）
- **验收标准**
  - 启动阶段未检测到 `rng_current==nsm-hwrng` 则拒绝服务；运行期异常立即告警
- **技术任务**
  - 自检与只读健康端点；故障下线/重建逻辑
- **依赖**：无  
- **优先级/预估**：Must / 2 SP

### S17：KMS 解锁的 Attestation 绑定与 Key Policy
- **验收标准**
  - 仅匹配度量（PCR/measurement）的 Enclave 可调用解封；策略归档
- **技术任务**
  - Attestation 验证与嵌入；KMS Key Policy/条件键配置
- **依赖**：S5  
- **优先级/预估**：Must / 3 SP

---

## 统一定义与附录

### 统一 Definition of Done（DoD）
- 代码合入主干；单元/集成测试通过；指标/日志齐备；看板/Runbook 更新；回滚方案明确；文档可复现。

### 统一性能门槛（适用于相关 Stories）
- `sign.total.p99 < 10 ms`；`rehydrate.p95 < 2 ms`；`NEEDS_UNLOCK_rate < 0.5%`；`engine_q_depth_avg < 1`；CPU < **75%**。

### 配置基线（可通过 ConfigMap/ENV 热更新）
- Enclave vCPU=**12**、内存=**8 GiB**；父机↔Enclave 长连接 **16–32**
- PlainKey：`ttl_soft=15m`、`ttl_hard=16m`、`maxUses=1_000_000`
- DEK：`ttl_soft=55m`、`ttl_hard=60m`
- 刷新预算：`signWaitBudget=2–3 ms`、`hard_refresh_budget=3–5 ms`
- 预刷新：`refreshWindow=2m`、`lowWaterMark=500 uses`、`jitter=±10%`
- 重试：`Retry-After=50–200 ms`（带抖动）

### Sprint 建议（仅按依赖/优先级排布，不代表时间承诺）
- **Sprint A**：S1, S2, S6, S8, S11  
- **Sprint B**：S3, S4, S10, S12  
- **Sprint C**：S5, S9, S14, S16, S17  
- **Sprint D**：S7, S13, S15

---

> 如需导出 **Jira CSV** 或 **禅道导入表**，可在此文档基础上自动生成（含 Epic Link、labels、优先级、预估点数、验收标准）。
