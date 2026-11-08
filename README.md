# vpsmonitor

vpsmonitor 是面向 Linux VPS 的监控工具。Hub 提供网页管理，Agent 通过宿主 Tailscale 主动连接。两个 Docker 镜像均支持 Linux amd64 和 arm64。

- CPU、内存、磁盘、负载、实时带宽、Docker、在线状态和告警。
- 读取宿主网络出口的累计 RX/TX，按 UTC 日期保存原生流量账本。支持自然月、自定义账期、配额及仅出站或双向计量。
- Agent 按 UTC 天保存分钟资源样本，滚动保留最多 31 天，Hub 重连后自动补传。
- 网页可导入导出设备配置和流量设置。

正式版为 [v1.0.0](https://github.com/CodeAIX/vpsmonitor/releases/tag/v1.0.0)。Docker 容器默认命名为 `vpsmonitor-hub` 和 `vpsmonitor-agent`。

## 安装

- [手动安装](docs/DEPLOY.md) · 逐步配置 Hub、Tailscale 授权和 VPS Agent。
- [AI 辅助安装](docs/DEPLOY-AI.md) · 可直接交给有主机访问权限的管理助手。
- [架构](docs/ARCHITECTURE.md) · 流量账本、资源归档和补传机制。

## 开发

```sh
cd internal/site
npm ci --ignore-scripts
npm run build
sh tests/run.sh
cd ../..
go test -race -tags=testing -p 2 -timeout=20m ./...
```

开发者可参考 [源码构建](docs/SOURCE-DEPLOY.md) 和 [发布流程](docs/RELEASING.md)。代码来源和许可见 [NOTICE](NOTICE) 与 [LICENSE](LICENSE)。
