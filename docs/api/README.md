# API 说明（create/sign 核心路径）

目标：最小消息体、稳定长连接、统一错误语义；满足 p99 < 10ms 的 SLO（`/create` 响应预算 ≤5ms，不含后台持久化）。

- 协议：HTTP/1.1 + JSON（OpenAPI）与 gRPC/HTTP2（推荐）
- 路由：
  - HTTP：`POST /create`、`POST /sign`
  - gRPC：`signer.v1.SignerService/Create`、`/Sign`、`/SignStream`（双向流）
- 摘要：`digest` 必须是 32 字节，可选 hex64/base64 表达
- 错误码映射：
  - INVALID_ARGUMENT → 400 / gRPC `InvalidArgument`
  - RETRY_LATER → 429 / gRPC `ResourceExhausted`（强制附带 `Retry-After`）
  - UNLOCK_REQUIRED → 503 / gRPC `Unavailable`（强制附带 `Retry-After`）
  - INVALID_KEY → 404/409 / gRPC `NotFound`（keyId 不存在/状态不允许）

## OpenAPI
- 规范文件：`docs/api/openapi.yaml`
- 示例：
```bash
curl -sS -X POST "$HOST/create" -H 'Content-Type: application/json' \
  -d '{"curve":"secp256k1"}'

curl -sS -X POST "$HOST/sign" -H 'Content-Type: application/json' \
  -d '{"keyId":"k1","digest":"0123456789abcdef...", "encoding":"hex"}'
```

## gRPC（推荐）
- Proto：`docs/api/proto/signer.proto`
- 示例：
```bash
# Create
grpcurl -plaintext localhost:9090 signer.v1.SignerService/Create '{}'

# Sign（单次）
grpcurl -plaintext -d '{"key_id":"k1","digest":"AAAAAAAA..."}' \
  localhost:9090 signer.v1.SignerService/Sign

# Sign（双向流，示意）
# 使用 ghz 或自研客户端进行流式压测参见 docs/bench/README.md
```

## 字段与约束
- `digest`：32 字节摘要（Keccak256/SHA256 等由调用方保证）；传输编码：`hex`（默认）或 `base64`，对应的 schema 参见 `HexDigest`/`Base64Digest`
- `keyId`：来源于 `/create` 响应，示例可参考 `docs/api/examples/create.json`
- 响应：`signature`（DER 或 64B raw，可配置），`recId` 可选
- 审计头部：`x-request-id`、`x-tenant-id` 默认禁用，开启时需在 OpenAPI/Proto 中同步

## 可观测字段（建议）
- 请求头预留：`x-request-id`、`x-tenant-id`（默认禁用，仅审计场景开启）
- 指标：`http_grpc_requests_total`、`sign_latency_ms{phase}`、`http_grpc_requests_latency_bucket`、4xx/5xx 分布
- `/create` 快路径需输出 histogram 以验证 ≤5ms SLA
- 详见 `docs/api/observability.md`

## SLO 备注
- 端到端 p99 < 10ms（单父机并发≥500）；热路径不走 KMS；错误率 < 0.1%
- `/create` 静态响应预算 ≤5ms；若触发后台持久化/审计，需异步处理并在日志输出 `create_async_persist_latency`

## Go Stub / 校验
- Proto：`docs/api/proto/signer.proto`
- 生成（手工维护）Go stub：`docs/api/gen/go/signer`，`go test ./...` 会校验 schema、错误码映射与 digest 验证逻辑
- 运行 `make test` 或 `go test ./...` 可完成 API 合约回归

## Retry 语义
- `Retry-After` 必填于 RETRY_LATER 与 UNLOCK_REQUIRED，单位秒或 HTTP 日期
- 建议客户端退避范围 50–200ms（带抖动），并在 `429` 情况下本地重试不超过 2 次
