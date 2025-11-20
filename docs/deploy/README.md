# 部署说明（EKS + Nitro Enclaves）

本指南落地 S11、并支撑 S1–S5 的服务部署。建议先完成集群与节点准备，再部署应用与配置。

## 1. 集群与节点准备
1) 启用 CPU Manager static（各节点）：
- 修改 Kubelet 配置（示例）：
```
# /var/lib/kubelet/config.yaml
cpuManagerPolicy: static
reservedSystemCPUs: "0-1"  # 示例
```
- 重启 kubelet 并验证：`kubectl describe node <node> | grep cpuManagerPolicy`

2) 安装 Nitro Enclaves 组件（各节点）：
- 安装驱动与工具（参阅 AWS 文档）
- 下发 allocator 配置：将 `docs/config/nitro-allocator.yaml` 拷贝到 `/etc/nitro_enclaves/allocator.yaml`
- 重启服务：`systemctl restart nitro-enclaves-allocator`
- 验证：`systemctl status nitro-enclaves-allocator`

3) 节点标记与污点（可选隔离）：
```
kubectl label node <node> nitro=true
kubectl taint node <node> nitro-only=true:NoSchedule
```

## 2. 应用部署
1) 命名空间与配置：
```
kubectl apply -f docs/k8s/deployment.yaml -n wallet-signer
```
> 注意：Deployment 中 `requests==limits` 以获得 Guaranteed QoS。

2) 镜像配置：
- 将 `yourrepo/signer:latest` 替换为实际镜像地址
- 若启用镜像仓库密钥：添加 `imagePullSecrets`

3) 健康检查：
- `GET /healthz` 与 `GET /livez`
- 初次部署可将探针延迟适当增加，待内部依赖就绪后再收紧

## 3. 配置与热更新
- 配置源：`ConfigMap/signer-config`（详见 docs/config/enclave-config.md）
- 变更方式：更新 ConfigMap 并滚动 Deployment

## 4. 验证
- API：参考 `docs/api/README.md`
- 指标/日志：建议接入 Prometheus/Grafana 与集中式日志

## 5. 回滚
- 建议使用 Deployment 的 `--record` 与 `rollout undo`
- 变更前导出副本：`kubectl get -n wallet-signer deploy signer -o yaml > backup.yaml`

