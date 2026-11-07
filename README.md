# vpsmonitor

面向 Linux VPS 的独立监控工具，基于 Beszel/Beszel+ 已验证的监控基础独立开发。

- CPU、内存/交换区、磁盘与 IO、负载、实时带宽、在线状态、Docker 和告警。
- **内置原生流量计量**：读取宿主公网出口累计 RX/TX，自动排除 Docker 网桥、veth、回环和常见隧道网口；选择唯一默认出口，支持手动指定。
- **无需 vnStat**：Agent 自主管理基线、UTC 日值和持久化；首次安装从零累计，不计入安装前的开机总量。
- **七天资源离线缓存**，以及独立保留的流量日账本、套餐配额和账期展示。
- 配置与流量设置导入导出，面向新安装；不迁移 Beszel/Beszel+ 历史数据或身份密钥。
- 温度、风扇、电池、Wi-Fi 从默认采集与常用界面中移除，GPU/SMART 不作为默认采集项目。

当前为 **0.1.0-dev 开发版**，尚未发布公共容器镜像。安装配置直接从公开源码构建。支持 Linux amd64/arm64；尚未完成生产 VPS 长期验收。

## 安装

```sh
git clone https://github.com/CodeAIX/vpsmonitor.git
cd vpsmonitor
./scripts/vpsmonitorctl hub start
```

默认打开 `http://127.0.0.1:8090` 创建管理员。在 Hub 中复制完整 Agent Compose 到每台 VPS 的新目录，执行 `docker compose up -d --build`。Agent 采用宿主网络并主动连接 Hub，默认无需新开放 Agent 入站端口；只需保留自己的持久卷，无需配置宿主 vnStat。

完整步骤、远程 Hub 地址、手动网卡选择、更新/诊断命令和计量边界见 [安装维护指南](docs/DEPLOY.md)。旧软件及数据可以在新部署确认后删除，本项目不依赖旧数据迁移。

## 开发

```sh
cd internal/site
npm ci --ignore-scripts
npm run build
sh tests/run.sh
cd ../..
go test -race -tags=testing -p 2 -timeout=20m ./...
```

CI 自行测试和构建，不自动跟随原上游或重放补丁。产品版本独立于内部能力基线。原作者许可和来源见 [NOTICE](NOTICE) 与 [LICENSE](LICENSE)。
