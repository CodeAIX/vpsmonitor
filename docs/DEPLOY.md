# vpsmonitor v0.1.0-beta.2 部署与升级

Hub 和 Agent 镜像支持 Linux `amd64` 与 `arm64`。Agent 通过宿主 Tailscale 主动连接 Hub 的专用端口。网页登录使用独立的入口。部署前准备已授权的 Tailscale、Docker Engine 与 Compose。Hub 的默认桥接模板需要 Docker Engine **28 或更新版本**，以保证回环发布端口的网络隔离。VPS 上的 Agent 需要宿主 `tailscale0` 和 Docker host 网络。[Docker 端口发布说明](https://docs.docker.com/engine/network/port-publishing)

- Hub `ghcr.io/codeaix/vpsmonitor-hub:v0.1.0-beta.2`
- Agent `ghcr.io/codeaix/vpsmonitor-agent:v0.1.0-beta.2`

## 单台 VPS 试装

已有 beta.1 环境时，先为 beta.2 启动一个独立测试 Hub，并给选中的 VPS 单独生成测试 Token。Hub 和 Agent 各用新项目名、目录和卷。这样其它 beta.1 节点仍能使用原 Hub。第 4 节提供正式切换时复用原身份和账本的步骤。

## 1. Hub 安装与预检

在运行 Hub 的 Linux 主机准备安装目录。下载模板和预检脚本，运行宿主检查。脚本使用 Python 3，读取 Tailscale 授权状态和 Docker Engine 版本。

```sh
mkdir -p ~/vpsmonitor-beta2/hub
cd ~/vpsmonitor-beta2/hub
release=https://github.com/CodeAIX/vpsmonitor/releases/download/v0.1.0-beta.2
curl -fL "$release/hub.compose.yml" -o compose.yml
curl -fL "$release/vpsmonitor-preflight" -o vpsmonitor-preflight
python3 vpsmonitor-preflight hub
```

在 Hub 目录创建 `.env`，填入未占用的端口以及本机的实际 Tailnet IPv4。用 `tailscale ip -4` 获取地址。以下为并行试装示例。

```dotenv
COMPOSE_PROJECT_NAME=vpsmonitor-beta2-hub
HUB_PORT=18096
HUB_URL=http://127.0.0.1:18096
AGENT_LOCAL_PORT=28091
AGENT_HUB_URL=http://100.64.0.10:8091
```

`HUB_URL` 用于浏览器入口，正式升级时沿用原来的 URL 和反向代理。`AGENT_HUB_URL` 用于 Agent，只填 Tailnet 地址与端口。Hub 模板把容器内 Agent 端口发布到宿主 `127.0.0.1:AGENT_LOCAL_PORT`，网页代理只转发网页端口。

```sh
docker compose config --quiet
docker compose pull && docker compose up -d
sudo tailscale serve --bg --tcp=8091 tcp://127.0.0.1:28091
sudo tailscale serve status
```

Serve 命令中的外部端口要与 `AGENT_HUB_URL` 一致，后端端口要与 `AGENT_LOCAL_PORT` 一致。使用空闲的 Tailnet 端口。通过 Tailnet 的 Grants 或 ACL 授权选定的 VPS 到 Hub 的这一 TCP 端口，保留你现有的其它规则。Serve 在后台持久运行，适合 Hub 先启动、Tailscale 后恢复的情形。fnOS 的实际冷启动顺序需在试装后验证。[Serve 文档](https://tailscale.com/docs/reference/tailscale-cli/serve) · [Grants 文档](https://tailscale.com/docs/features/access-control/grants)

通过网页入口创建管理员。若 Hub 在远程 NAS，使用你已有的受限 LAN 入口或网页登录代理访问测试 Hub 的网页端口。

## 2. VPS 安装与预检

先在 VPS 安装、登录并授权宿主 Tailscale。在 Agent 的新目录下载脚本，用 Hub 配置中的 Tailnet 地址运行预检。

```sh
mkdir -p ~/vpsmonitor-beta2/agent
cd ~/vpsmonitor-beta2/agent
release=https://github.com/CodeAIX/vpsmonitor/releases/download/v0.1.0-beta.2
curl -fL "$release/vpsmonitor-preflight" -o vpsmonitor-preflight
python3 vpsmonitor-preflight agent http://100.64.0.10:8091
```

预检先检查本机 Tailscale 授权，再用临时 Agent 容器验证 `tailscale0`、实际 TCP 端口和 Hub 专用入口。HTTPS 地址同时验证 TLS。预检不需要 Agent Token，首次鉴权由安装后真正的 Agent 连接验证。若 Tailscale 能 ping 通而预检失败，检查 TCP 端口授权和 Serve 转发。[Tailscale TCP 排障说明](https://tailscale.com/docs/reference/troubleshooting/network-configuration/tcp-connection-two-devices)

在新版 Hub 点 **添加系统**，填名称和主机地址，复制 Docker Compose，保存系统记录。也可从通用 Token 页面复制同一个模板。在 VPS 保存为 `compose.yml`，设置文件权限 `0600`。并行试装时在该目录的 `.env` 中加入 `COMPOSE_PROJECT_NAME=vpsmonitor-beta2-agent`。

```sh
chmod 600 compose.yml
docker compose config --quiet
docker compose pull && docker compose up -d
docker compose ps
```

[下载 Agent 模板](https://github.com/CodeAIX/vpsmonitor/releases/download/v0.1.0-beta.2/agent.compose.yml)也可以，手动在权限为 `0600` 的 `.env` 中填入新版 Hub 公钥 `KEY`、Agent `TOKEN` 和 Tailnet `HUB_URL`。两种入口使用同一个 Compose 服务 `agent` 和挂载 `./agent_data:/var/lib/vpsmonitor-agent`。测试环境和正式环境使用各自的项目名和挂载目录。

## 3. 验收与诊断

- 设备上线后检查 CPU、内存和采样时间，再产生少量网络流量，验证 Hub 的 RX/TX 增长。Agent 会自动选择唯一默认出口。已有明确计量口时保留 `VPSMONITOR_TRAFFIC_INTERFACE`，模板 `.env` 使用 `TRAFFIC_INTERFACE`。
- Agent 的 `/agent health` 检查与 Hub 的连接。检查本地计量时，还应核对 Agent 日志、持久卷中的流量账本更新时间，以及 Hub 最新资源和流量样本。Hub 离线时，保持 Agent 运行，让它继续保存本地样本和账本。
- 流量详情可设置配额、计量方向和结算日。首页按 UTC 结算日显示剩余时间。安装中途开始采集、丢失部分日值或计数重置会影响数据完整性。
- 先验收一次 Hub/Agent 重建和短时断网，随后验证 fnOS 冷启动、Tailnet 入口恢复、Agent 自动重连和补传。试装范围以一台 VPS 开始。

## 4. beta.1 原地升级与回退

正式切换前先核对当前 Hub 的网页登录入口和所有 Agent 的接入地址。beta.2 的网页端口和 Agent 端口分开。升级现有 Hub 时需要协调其它 Agent 的新接入地址，因此单机试装使用独立测试 Hub。

1. 在现有 Hub 的 Compose 文件中保留项目名、网页监听、`APP_URL`、反向代理以及 `/vpsmonitor_data` 的实际挂载。将镜像改为 beta.2，按第 1 节新增 `AGENT_HUB_URL` 和 `127.0.0.1:<本地端口>:8091` 映射，再配置 Serve 和 TCP 授权。原 Hub 数据目录保留设备 ID、公钥、登录与流量设置。
2. 在每台 VPS 原来的 Compose 文件里保留 `KEY`、`TOKEN`、显式计量网卡、服务名和 `/var/lib/vpsmonitor-agent` 的实际挂载源。beta.1 可能使用 `./agent_data`、`./vpsmonitor_agent_data` 或具名卷。把 `HUB_URL` 改成新 Hub 的 Agent 专用 Tailnet 地址，通过预检后把镜像改为 beta.2。升级沿用原 Compose 文件，不套用新安装模板。
3. 两端分别在原项目目录运行 `docker compose config --quiet` 与 `docker compose pull && docker compose up -d`。确认原设备重新在线，流量设置和已累计账本仍在。可以用 `docker inspect --format '{{range .Mounts}}{{println .Source "->" .Destination}}{{end}}' <容器名>` 核对实际挂载。
4. 回退需要把旧镜像标签、旧 Agent 地址和旧入口映射一并恢复。升级前保留 Compose 和 `.env` 副本。如需数据库回退点，在停止 Hub 后保存一致的 `hub_data` 副本。

启动读取 `config.yml` 只新增或更新匹配项，保留现有 Token、指纹、流量设置和清单以外的设备。需要清理设备时在 Hub 内逐项确认。

若仍需卸载旧 Beszel+，按 [beta.1 指南的旧版卸载步骤](https://github.com/CodeAIX/vpsmonitor/releases/download/v0.1.0-beta.1/DEPLOY.md)核对旧容器和独占挂载后操作。开发者从源码构建可查看 [源码部署指南](SOURCE-DEPLOY.md)。
