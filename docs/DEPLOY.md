# vpsmonitor v0.1.0-beta.4 部署与升级

Hub 和 Agent 镜像支持 Linux `amd64` 与 `arm64`。Agent 通过宿主 Tailscale 主动连接 Hub 的专用端口。网页登录使用独立的入口。部署前准备已授权的 Tailscale、Docker Engine 与 Compose。Hub 的默认桥接模板需要 Docker Engine **28 或更新版本**，以保证回环发布端口的网络隔离。VPS 上的 Agent 需要宿主 `tailscale0` 和 Docker host 网络。[Docker 端口发布说明](https://docs.docker.com/engine/network/port-publishing)

- Hub `ghcr.io/codeaix/vpsmonitor-hub:v0.1.0-beta.4`
- Agent `ghcr.io/codeaix/vpsmonitor-agent:v0.1.0-beta.4`

## 已有 beta.3：在原项目覆盖升级

按 [beta.4 正式覆盖升级执行单](https://github.com/CodeAIX/vpsmonitor/releases/download/v0.1.0-beta.4/UPGRADE-beta.4.md) 操作。先备份并更新 Hub，确认所有旧 Agent 的实时样本及历史游标继续推进，再逐台更新 Agent。保留现有 Compose 项目、挂载、身份、凭据和入口，旧缓存可以继续读取。新版 Agent 逐步累积到滚动 31 天。

如果仍在用 beta.2，可按 [beta.3 原位升级指南](https://github.com/CodeAIX/vpsmonitor/releases/download/v0.1.0-beta.3/DEPLOY.md) 先升级到 beta.3。

### fnOS 重启恢复验收

先确认 Docker 容器重启策略与原项目一致，Agent 本地映射只在回环监听，Serve 已用 `--bg` 保存到宿主 Tailscale 服务。选维护窗口冷启动，分别确认 Docker、Tailscale 已授权、Serve 转发恢复和 Agent 新样本；验证实际 Agent 到 Tailnet 入口的 TCP 连通，并从获准与未获准的 LAN 客户端各检查一次网页登录限制。若 Tailscale 晚于 Docker，Hub 的 Agent 后端仍应在回环地址启动；等 Tailnet 恢复后再验证 Agent 自动连接和短时离线数据补传。该检查需要在正式 fnOS 上执行，不能用容器重建代替。

## 1. Hub 安装与预检

在运行 Hub 的 Linux 主机准备安装目录。下载模板和预检脚本，运行宿主检查。脚本使用 Python 3，读取 Tailscale 授权状态和 Docker Engine 版本。

```sh
mkdir -p ~/vpsmonitor/hub
cd ~/vpsmonitor/hub
release=https://github.com/CodeAIX/vpsmonitor/releases/download/v0.1.0-beta.4
curl -fL "$release/hub.compose.yml" -o compose.yml
curl -fL "$release/vpsmonitor-preflight" -o vpsmonitor-preflight
python3 vpsmonitor-preflight hub
```

在 Hub 目录创建 `.env`，填入未占用的端口以及本机的实际 Tailnet IPv4。用 `tailscale ip -4` 获取地址。下面是新装示例，请换成实际访问地址。

```dotenv
COMPOSE_PROJECT_NAME=vpsmonitor-hub
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
mkdir -p ~/vpsmonitor/agent
cd ~/vpsmonitor/agent
release=https://github.com/CodeAIX/vpsmonitor/releases/download/v0.1.0-beta.4
curl -fL "$release/vpsmonitor-preflight" -o vpsmonitor-preflight
python3 vpsmonitor-preflight agent http://100.64.0.10:8091
```

预检先检查本机 Tailscale 授权，再用临时 Agent 容器验证 `tailscale0`、实际 TCP 端口和 Hub 专用入口。HTTPS 地址同时验证 TLS。预检不需要 Agent Token，首次鉴权由安装后真正的 Agent 连接验证。若 fnOS 上 CLI 或 daemon socket 不在默认位置，可使用 `--tailscale-bin /path/to/tailscale --tailscale-socket /path/to/tailscaled.sock`；`--timeout 90` 限制预检总时长，超时后的临时容器清理最多另等 5 秒。可用 `--image` 指向已经验证并导入的本地镜像。若 Tailscale 能 ping 通而预检失败，检查 TCP 端口授权和 Serve 转发。[Tailscale TCP 排障说明](https://tailscale.com/docs/reference/troubleshooting/network-configuration/tcp-connection-two-devices)

在新版 Hub 点 **添加系统**，填名称和主机地址，复制 Docker Compose，保存系统记录。也可从通用 Token 页面复制同一个模板。在 VPS 保存为 `compose.yml`，设置文件权限 `0600`。并行试装时在该目录的 `.env` 中加入 `COMPOSE_PROJECT_NAME=vpsmonitor-agent`。

```sh
chmod 600 compose.yml
docker compose config --quiet
docker compose pull && docker compose up -d
docker compose ps
```

[下载 Agent 模板](https://github.com/CodeAIX/vpsmonitor/releases/download/v0.1.0-beta.4/agent.compose.yml)也可以，手动在权限为 `0600` 的 `.env` 中填入新版 Hub 公钥 `KEY`、Agent `TOKEN` 和 Tailnet `HUB_URL`。两种入口使用同一个 Compose 服务 `agent` 和挂载 `./agent_data:/var/lib/vpsmonitor-agent`。测试环境和正式环境使用各自的项目名和挂载目录。

## 3. 验收与诊断

- 设备上线后检查 CPU、内存和采样时间，再产生少量网络流量，验证 Hub 的 RX/TX 增长。Agent 会自动选择唯一默认出口。已有明确计量口时保留 `VPSMONITOR_TRAFFIC_INTERFACE`，模板 `.env` 使用 `TRAFFIC_INTERFACE`。
- Agent 的 `/agent health` 检查与 Hub 的连接。检查本地计量时，还应核对 Agent 日志、持久卷中的流量账本更新时间，以及 Hub 最新资源和流量样本。Hub 离线时，保持 Agent 运行，让它继续保存本地样本和账本。
- 流量详情可设置配额、计量方向和结算日。首页按 UTC 结算日显示剩余时间。安装中途开始采集、丢失部分日值或计数重置会影响数据完整性。
- 先验收一次 Hub/Agent 重建和短时断网，随后验证 fnOS 冷启动、Tailnet 入口恢复、Agent 自动重连和补传。试装范围以一台 VPS 开始。

## 4. 一次性导入和启动同步

普通迁移使用网页 **设置 → YAML 配置** 的导入预览；可以同时导入流量设置。若仍在使用启动 `config.yml`，先在同一页面的高级选项选择 **预览启动同步**，核对新增、更新、未列出的保留项及归属变化。可导出带稳定设备 ID 的同 Hub 配置。ID 优先匹配；没有 ID 的条目按名称、主机和端口匹配。遗漏 `users` 时保留已有归属；显式给出非空列表时替换归属；空列表、未知用户或未知 ID 会阻止同步。同步保留现有 Token、指纹、流量设置和未列出的设备。预览不修改数据；正式同步在 Hub 重启读取 `config.yml` 时发生。

若从 beta.1 跨版升级，需要先按 [beta.2 指南](https://github.com/CodeAIX/vpsmonitor/releases/download/v0.1.0-beta.2/DEPLOY.md)核对入口拆分及旧卷路径；beta.2 已经拆分入口的部署可直接覆盖。

若仍需卸载旧 Beszel+，按 [beta.1 指南的旧版卸载步骤](https://github.com/CodeAIX/vpsmonitor/releases/download/v0.1.0-beta.1/DEPLOY.md)核对旧容器和独占挂载后操作。开发者从源码构建可查看 [源码部署指南](SOURCE-DEPLOY.md)。
