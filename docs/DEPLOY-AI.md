# vpsmonitor 1.0.0 AI 辅助安装

把本页交给有目标主机操作权限的管理助手，并提供 Hub 主机、VPS 清单、SSH 主机指纹、期望的网页登录地址和 Tailnet 设备信息。Agent 通信使用已获授权的宿主 Tailscale。凭据通过用户已有的密钥管理渠道提供。

执行人使用 [v1.0.0 发布页](https://github.com/CodeAIX/vpsmonitor/releases/tag/v1.0.0) 的模板、预检脚本和校验和，按 [手动安装](https://github.com/CodeAIX/vpsmonitor/releases/download/v1.0.0/DEPLOY.md) 的命令部署。已有 vpsmonitor 实例时，先查明当前项目和数据挂载，使用对应的覆盖升级执行单。

## 1. 清点与预检

- 通过已核验的管理通路逐台确认主机、架构、Docker Engine、Compose、Python 3、Tailscale 授权和当前端口占用。Docker Hub 宿主使用 Engine 28 或以上，VPS 具有可用的 `tailscale0`。
- 记录 Hub 网页地址、专用 Tailnet 入口、仅回环的 Serve 后端和各宿主数据目录。用现有访问规则授权选定的 Agent 节点访问专用 TCP 端口。
- 下载正式资产并核对 SHA-256。按发布页摘要固定镜像，提前拉取。保存实际 Compose 项目和服务名，默认容器为 `vpsmonitor-hub` 和 `vpsmonitor-agent`。

## 2. 安装 Hub

- 按模板设置网页地址、Agent 入口、数据挂载和回环端口。运行 `python3 vpsmonitor-preflight hub` 并验证 Compose 配置。
- 启动 Hub，配置专用 Serve 转发。检查浏览器入口、Agent 后端、TLS 或 LAN 访问策略。由用户创建管理员账号，或通过约定的私密渠道交付。
- 记录真实端口、Compose 项目及持久卷。从每台目标 VPS 执行 `python3 vpsmonitor-preflight agent <Hub 的 Tailnet HTTP(S) 地址>`。预检通过后再发放注册配置。

## 3. 安装 Agent

- 先选一台 VPS。在 Hub 添加系统或使用通用 Token，保存对应 Agent Compose 和连接凭据。只在目标机器保存权限为 `0600` 的配置。
- 运行前核对宿主网络、原生流量接口和持久卷。唯一 IPv4 默认出口可用 `auto`，有多个候选或运营商要求时填明确的宿主网卡。
- 启动首台后检查真实版本、原设备状态、两条不同分钟的资源样本和历史游标。验证同一计量接口的账本与 Hub 日累计，按用户设定应用配额、计量方向和结算日。首台通过后，逐台安装其余 Agent。
- 每台验证失败时暂停该台，记录出错的预检或采集层级。保留已启动实例的目录及日志，针对失败阶段恢复。

## 4. 验收记录

汇报各主机的实际镜像版本与摘要、容器名、挂载、网页与 Tailnet 入口、两条资源样本、历史同步和流量验证结果。报告可列接口、配额和计量方向，凭据和完整环境配置保留在宿主私有目录。把尚未实测的跨日、长时间断联和整机冷启动与当前验收分开记录。
