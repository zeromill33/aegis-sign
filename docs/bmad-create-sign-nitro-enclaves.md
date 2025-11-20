# BMAD：Nitro Enclaves + EKS 下的 create/sign 核心路径优化方案
**版本**：v1.0  
**作者**：团队内核服务组（Go）  
**目标**：单机（单父机实例）并发 ≥ **500**，签名请求端到端 **p99 < 10 ms**；热路径**不走 KMS**。  
**范围**：仅包含 `create`（生成地址/账户）与 `sign`（签名）两条核心业务路径；本版**不包含**明文导入/导出。

---

## B — Background（背景）

### 现状与约束
- **架构**：Amazon EKS 工作节点作为 **Nitro Enclaves** 父机，私钥相关操作在 Enclave 内执行。Enclave 无网络与持久化，唯一通道为 **AF_VSOCK（vsock）**；父机 CID=3，Enclave 拥有唯一 CID。  
- **语言/栈**：Go（父机 Gateway + Enclave 应用）。椭圆曲线采用 **libsecp256k1**（C，cgo 封装），默认 **RFC6979** 确定性签名。  
- **KMS 策略**：已采用信封加密（DEK 包裹私钥）。**热路径不走 KMS**；仅在 **解锁/轮换** 触发 `Decrypt/GenerateDataKey`，结合 Enclave **Attestation** 限定访问。  
- **租户模型**：当前版本**单租户**。  
- **SLO**：并发 ≥ 500；`sign` 端到端 **p99 < 10 ms**；错误率 < 0.1%。

### 业务边界
- `create`：生成私钥/公钥（和链路地址），立即返回；持久化与包封（密文 Blob）在后台完成，不阻塞响应。  
- `sign`：输入 `keyId + digest(32B)`；Enclave 内完成签名并返回（可选 `recId`）。**Digest 由父机预先构造/哈希**（仅传 32B 以缩短热路径）。

---

## M — Motivation（动机）

1) **延迟目标收敛**：TLS/gRPC 握手、vsock 建连、KMS HSM 调用等易造成 10–100 ms 级延迟，不满足 p99<10 ms。  
2) **吞吐瓶颈**：单线程签名、上下文反复初始化、cgo 频繁跨界会显著降低每核产能。  
3) **抖动长尾**：EKS 默认 CPU 抢占、连接队首阻塞、RNG 阻塞等放大 p99。  
4) **缓存边界**：TTL/次数上限（maxUses）到期瞬间的签名请求如何处置，若误把 KMS 拉进热路径，会击穿 SLO。

> 容量预算（保守）：单次签名计算 0.05–0.30 ms；1 vCPU ≈ 3–10k ops/s。目标并发 500 对应峰值 50k ops/s，建议为 Enclave 预留 **≥12 vCPU** 并发签名（后文给出配置）。

---

## A — Actions（行动方案）

### A1. 体系与链路（最小消息体 + 长连接）
- **父机 API（gRPC/HTTP2 长连接）**
  - `POST /create` → `{algorithm:"secp256k1", label?}` → `{keyId, publicKey, address?}`  
  - `POST /sign` → `{keyId, digest[32], returnRecID?}` → `{signature, recId?}`
- **父机 → Enclave（vsock 上的 gRPC）**
  - **双向流式**优先（或 unary 长连接 + 连接池）。**每 Enclave 维持 16–32 条**长连接，KeepAlive 开启。  
  - **仅传 32B digest** 与最少元数据，避免 JSON/复制。

### A2. 签名引擎与并发模型（vCPU 等量并行 + 微批）
- **Worker = vCPU**：Enclave 预留 `N` vCPU，就起 `N` 个工作线程；每线程：
  - 启动时创建并**复用** `SECP256K1_CONTEXT_SIGN`（禁止重复初始化）。  
  - `runtime.LockOSThread()` 减少迁移抖动。  
  - 从 MPSC 队列取任务，支持**微批**（`maxBatch=32, maxWait=1–2 ms`）；低延迟请求可 `noWait` 直签。  
- **cgo 批量降跨界**：提供 `SignBatch(digests[], priv)` C shim，把多条签名合并一次 cgo 调用。  
- **RNG**：默认 RFC6979；启动自检 `rng_current == nsm-hwrng`，否则拒绝服务。

### A3. 缓存语义（软/硬 TTL + WARM/COOL/INVALID + 单飞刷新）
> 目标：**热路径不走 KMS**；TTL 或用次到期**不阻塞**正常签名。

- **两级缓存（Enclave 内）**
  - **PlainKey（明文私钥）**：仅内存；`ttl_soft=15m`、`ttl_hard=16m`、`maxUses=1_000_000`。  
  - **DEK（明文数据密钥）**：仅内存；`ttl_soft=55m`、`ttl_hard=60m`。  
  - **密文 Blob**：长期存父机；Enclave 内可保留只读副本。
- **状态机**
  - **WARM**：PlainKey+DEK 都在 → 直接签名。  
  - **COOL**：PlainKey 过期/用尽已清零，但 DEK 在 → **本地再水合（rehydrate）**：用 DEK 解 Blob 得 PlainKey，毫秒内恢复到 WARM。  
  - **INVALID**：DEK 也过期 → 需要 KMS 解封（冷路径）。
- **过期策略**
  - **到 soft TTL**：允许签名；后台触发**单飞刷新（singleflight）**，不影响当前请求。
  - **到 hard TTL**：执行“**限时同步刷新**（`refresh_budget=3–5 ms`）**+ 超时快速失败**”。
    - 成功：回到 WARM，继续签名。  
    - 超时/失败：返回 `NEEDS_UNLOCK`（快速），由网关**异步解锁**并提示客户端**短延迟重试**。  
  - **可选**：`grace_ops`（如 0/10）——极少量应急签名，必须审计。
- **低水位与抖动**
  - `low_water_mark = 50_000`（Uses 剩余少于该值触发后台刷新）。  
  - **预热器**周期扫描即将 soft 过期的热点 key，**加入 0–10% 抖动**分散刷新。

#### Go 伪代码（核心）
```go
// 热路径
sig, err := SignFast(ctx, keyId, digest)
// EXPIRED -> 3ms 内刷新成功 or 立即返回 NEEDS_UNLOCK（上层重试/异步解锁）
// STALE -> 放行签名 + 后台单飞刷新

// 单飞刷新
sf.Do(keyId, func() (any, error) { return nil, refresh(keyId) })

// 刷新实现（冷路径）
- 取密文 Blob（父机提供/本地副本）
- KMS+Attestation 解封 DEK（仅解锁/轮换时）
- 用 DEK 本地解密 Blob -> PlainKey
- 替换条目：重置 TTL/Uses；清零临时缓冲
```

### A4. `create` 路径（立即响应 + 后台持久化）
- Enclave 生成密钥对 → 放入内存 `KeyStore`（设定 TTL/Uses）→ **立即**返回 `keyId/publicKey/address`。  
- **后台**：用现有 DEK 包封私钥生成密文 Blob → 通过父机持久化（S3/DynamoDB）。  
- 失败回滚：若包封/持久化失败，仅影响后续解锁；不影响已返回的 `create` 结果（有审计）。

### A5. 父机网关与路由
- **一致性哈希粘性路由**：`keyId -> enclave`，确保缓存命中。  
- **连接池**：对每个 Enclave 维持 **16–32** 条 gRPC/HTTP2 长连接（KeepAlive、MaxConcurrentStreams 调大）。  
- **遇到 `NEEDS_UNLOCK`**：网关**立即异步**调用 `Unlock(keyId)`，并向客户端返回可重试错误（带 `Retry-After: 50–200ms` 抖动）。

### A6. 资源与调度（EKS + Nitro 配置）
- **Nitro allocator（父机）**
  ```yaml
  # /etc/nitro_enclaves/allocator.yaml
  memory_mib: 8192     # 8 GiB 起步（按并发与批量再调）
  cpu_count: 12        # 与 Enclave worker 数一致或略高
  ```
- **EKS 节点/Pod**  
  - 专用节点池（taints/labels）；节点预装 nitro CLI、vsock 代理。  
  - **CPU Manager: static + Guaranteed QoS**（`requests == limits`），为 Gateway/Enclave 分配**独占整核**，降低抖动。  
  - 父机 Pod 资源建议：`cpu=4–6`、`memory=2–4Gi`；Enclave vCPU=12、内存=8Gi（目标并发 500 的基线）。
- **多 Enclave（可选扩容）**  
  - 单父机可并行多个 Enclave，按 `keyId` 分片，横向提升吞吐。

### A7. 可观测性（指标/日志/追踪）
- **指标（Prometheus/OpenTelemetry）**
  - 网关：`sign_latency_ms{phase=queue|rpc|vsock|enclave|total}`、`inflight`、`retry_rate`、`conn_pool_active`。  
  - Enclave：`engine_q_depth`、`batch_size`、`sign_cpu_us`、`rehydrate_latency_ms`、`state_count{WARM|COOL|INVALID}`、`rng_source`。  
  - 刷新：`refresh_total`、`refresh_fail_total`、`refresh_latency_ms`、`NEEDS_UNLOCK_total`、`grace_sign_total`。
- **SLO 报表**：p50/p90/p95/p99；`NEEDS_UNLOCK_rate < 0.5%`。  
- **日志**：EXPIRED 命中、grace 签名、解锁失败均需结构化审计。

### A8. 压测与验收
- **场景**：
  1) 预创建 `N=50k` key 并解锁（预热）。  
  2) 并发 `c=500` 持续 15 分钟，随机 `keyId` 均匀分布；负载仅 32B digest。  
  3) A/B：`N(worker)=8/12/16`、`batch on/off`、实例型（x86/ARM）对比。
- **通过标准**：
  - `sign.total.p99 < 10 ms`；`error_rate < 0.1%`；CPU < 75%；`engine_q_depth_avg < 1`。  
  - `rehydrate_latency_ms.p95 < 2 ms`；`NEEDS_UNLOCK_rate < 0.5%`。

### A9. 运行手册（Runbook）
- **TTL 命中**：监控 `STALE/EXPIRED` 比例升高 → 检查预热器与 singleflight 是否工作；必要时临时上调 `refresh_window` 或加 vCPU。  
- **`NEEDS_UNLOCK` 激增**：检查 KMS/VPC Endpoint、vsock 代理并发；回退到更长 `Retry-After`，同时限流热点 key。  
- **RNG 异常**：若 `rng_current != nsm-hwrng` → 立即下线该 Enclave 并重建。  
- **长尾抖动**：核查 EKS 是否 Guaranteed+CPU static、是否与其它工作负载抢核；观察 GC/系统干扰。

### A10. 风险与缓解
- **EXPIRED 短时拒绝**导致少量重试 → 通过预热和抖动把概率压到极低；客户端做指数退避。  
- **批量签名与 cgo** 潜在崩溃放大 → 隔离进程/熔断恢复；回退单签路径。  
- **节点资源不足** → 节点级 HPA/Autoscaler 结合队列水位作触发。

---

## D — Decisions（决策）

1) **热路径不走 KMS**：仅解锁/轮换时走 `Decrypt/GenerateDataKey`；签名全程本地（Enclave 内）。  
2) **两级缓存 + 软/硬 TTL**：WARM/COOL/INVALID 状态机；COOL 本地再水合；EXPIRED 限时刷新或快速失败。  
3) **单飞刷新**：同 `keyId` 刷新只允许一个在跑，防止雪崩。  
4) **连接与并发**：父机↔Enclave 16–32 长连接；Enclave `worker = vCPU`；支持 `maxBatch=32` 微批。  
5) **最小消息体**：只传 32B digest；父机做 RLP/Keccak 等预处理。  
6) **EKS 硬约束**：Guaranteed QoS + CPU Manager static；Nitro allocator `cpu_count=12`、`memory_mib=8192`（基线）。  
7) **SLO/验收**：并发 500 下 `p99 < 10 ms`；`NEEDS_UNLOCK_rate < 0.5%`；`rehydrate_p95 < 2 ms`。  
8) **可选策略**：`grace_ops=0`（默认严格）；如业务需要可设 `grace_ops=5–10`（须审计）。

---

## 附录 A：关键结构体与伪代码

```go
// Key cache entry（Enclave 内）
type Entry struct {
    KeyID     string
    Priv32    [32]byte
    ExpiresAt time.Time // hard TTL
    SoftAt    time.Time // soft TTL
    UsesLeft  uint32
    GraceLeft uint16
    // 控制域
    mu         sync.RWMutex
    refreshing int32 // 0/1
}

// 状态推断
func (e *Entry) state(now time.Time) State {
    if now.After(e.ExpiresAt) { return EXPIRED }
    if now.After(e.SoftAt)    { return STALE }
    return HOT
}

// 热路径签名（简化）
func (m *Manager) SignFast(ctx context.Context, id string, digest [32]byte) ([]byte, int, error) {
    e := m.get(id)
    if e == nil { return nil, 0, ErrNeedsUnlock }

    switch e.state(time.Now()) {
    case HOT:
        return secp.Sign(e.Priv32, digest)
    case STALE:
        e.tryRefreshAsync() // 单飞后台刷新
        return secp.Sign(e.Priv32, digest)
    case EXPIRED:
        if e.tryRefreshWithin(3 * time.Millisecond) {
            return secp.Sign(e.Priv32, digest)
        }
        if policy.AllowGraceOps && e.GraceLeft > 0 {
            e.GraceLeft--
            audit.Log("grace_sign", id)
            return secp.Sign(e.Priv32, digest)
        }
        return nil, 0, ErrNeedsUnlock
    }
    panic("unreachable")
}
```

---

## 附录 B：配置清单（初始值）

| 项目 | 建议值 | 说明 |
|---|---:|---|
| Enclave vCPU | 12 | 与 worker 数一致 |
| Enclave 内存 | 8 GiB | 可按批量/并发调大 |
| 父机连接池 | 16–32 | 每个 Enclave 的 gRPC 长连接数 |
| `ttl_soft` | 15 m | PlainKey 软过期 |
| `ttl_hard` | 16 m | PlainKey 硬过期 |
| `maxUses` | 1,000,000 | 用次上限 |
| `DEK ttl_soft`/`hard` | 55 m / 60 m | DEK 更长的寿命 |
| `refresh_budget` | 3–5 ms | EXPIRED 限时同步刷新预算 |
| `low_water_mark` | 50,000 | 用次低水位触发刷新 |
| `grace_ops` | 0（默认） | 可选 5–10，需审计 |
| `Retry-After` | 50–200 ms | `NEEDS_UNLOCK` 场景客户端重试间隔 |

---

## 附录 C：验收清单（上线前）
- [ ] 压测并发 500，`p99 < 10 ms`，误差内满足 SLO。  
- [ ] `NEEDS_UNLOCK_rate < 0.5%`；`refresh_fail_total` 接近 0。  
- [ ] EKS 节点启用 **CPU Manager static** 与 **Guaranteed QoS**。  
- [ ] `rng_current == nsm-hwrng` 自检通过。  
- [ ] 灰度开关：`grace_ops`、`maxBatch`、`refresh_budget` 可热更新。  
- [ ] 看板：延迟分段、状态计数（WARM/COOL/INVALID）、刷新成功率。

---

**结语**：通过“**最小消息体 + 长连接**、**vCPU 等量并发 + 微批**、**两级缓存与软/硬 TTL**、**单飞刷新与快速失败**、**EKS 资源硬隔离**”组合拳，既能守住“热路径不走 KMS”的安全边界，又能在单机并发 500 下稳定达到 **p99 < 10 ms** 的性能目标。
