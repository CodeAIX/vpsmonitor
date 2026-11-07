# vpsmonitor

面向 Linux VPS 的独立监控工具，基于 Beszel/Beszel+ 已验证的监控基础独立开发。

- CPU、内存/交换区、磁盘与 IO、负载、实时带宽、在线状态、Docker 和告警。
- **内置原生流量计量**：读取宿主公网出口累计 RX/TX，自动排除 Docker 网桥、veth、回环和常见隧道网口；选择唯一默认出口，支持手动指定。
- **无需 vnStat**：Agent 自主管理基线、UTC 日值和持久化；首次安装从零累计，不计入安装前的开机总量。
- **七天资源离线缓存**，以及独立保留的流量日账本、套餐配额和账期展示。
- 配置与流量设置导入导出，面向新安装；不迁移 Beszel/Beszel+ 历史数据或身份密钥。
- 温度、风扇、电池、Wi-Fi 从默认采集与常用界面中移除，GPU/SMART 不作为默认采集项目。

首个 Docker 测试版：[v0.1.0-beta.1](https://github.com/CodeAIX/vpsmonitor/releases/tag/v0.1.0-beta.1)。Hub 和 Agent 均支持 Linux amd64/arm64，测试版尚未完成生产 VPS 长期验收。

## 安装

```sh
mkdir -p ~/vpsmonitor/hub
cd ~/vpsmonitor/hub
curl -fL https://github.com/CodeAIX/vpsmonitor/releases/download/v0.1.0-beta.1/hub.compose.yml -o compose.yml
docker compose pull && docker compose up -d
```

默认打开 `http://127.0.0.1:8090` 创建管理员。为远端 VPS 配置可达的 Hub 地址后，从 Hub 复制 Agent Compose 到 VPS 的新目录，执行 `docker compose pull && docker compose up -d`。Agent 使用宿主网络并主动连接 Hub，无需开放 Agent 入站端口或配置 vnStat。

从零安装、更新、回退与卸载旧 Beszel+ 的步骤见 [安装维护指南](docs/DEPLOY.md)。开发者可使用 [源码部署指南](docs/SOURCE-DEPLOY.md)；[开发版验证记录](docs/VALIDATION.md) 保留原生计量和断线补传的早期验收证据。

## 开发

```sh
cd internal/site
npm ci --ignore-scripts
npm run build
sh tests/run.sh
cd ../..
go test -race -tags=testing -p 2 -timeout=20m ./...
```

CI 自行测试和构建，不自动跟随原上游或重放补丁。产品版本独立于内部能力基线。见 [独立开发方案](docs/ARCHITECTURE.md) 和 [发布流程](docs/RELEASING.md)。原作者许可和来源见 [NOTICE](NOTICE) 与 [LICENSE](LICENSE)。
