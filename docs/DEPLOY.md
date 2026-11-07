# vpsmonitor v0.1.0-beta.1 安装、更新与卸载旧版

这是首个 Docker 测试版。Hub 和 Agent 都支持 Linux `amd64`、`arm64`，使用 Docker Engine 和 Compose 插件。VPS 上无需编译、安装 vnStat 或挂载其数据库。新版从空数据目录和新身份开始，不导入 Beszel/Beszel+ 的历史、私钥或 Token。

| 组件 | 首个测试版镜像 |
| --- | --- |
| Hub | `ghcr.io/codeaix/vpsmonitor-hub:v0.1.0-beta.1` |
| Agent | `ghcr.io/codeaix/vpsmonitor-agent:v0.1.0-beta.1` |

建议先选一台 VPS 试装 Agent，确认在线、资源采样和流量正常后再扩大范围。新旧 Agent 可并行验收：新版通过 WebSocket 主动连接 Hub，不占用旧 Agent 的入站监听端口。新 Hub 使用独立目录和端口。

## 1. 从零部署 Hub

在运行 Hub 的机器上执行：

```sh
mkdir -p ~/vpsmonitor/hub
cd ~/vpsmonitor/hub
curl -fL https://github.com/CodeAIX/vpsmonitor/releases/download/v0.1.0-beta.1/hub.compose.yml -o compose.yml
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose ps
```

默认绑定 `127.0.0.1:8090`，数据在当前目录的 `hub_data/`。本机可直接打开 `http://127.0.0.1:8090` 创建管理员；远程 Hub 可先用 SSH 端口转发访问，例如从自己的电脑执行 `ssh -L 8090:127.0.0.1:8090 user@hub-host`，保持连接再打开同一地址。

**远端 VPS 必须能访问 Hub。** 在 Hub 所在机器为其配置 HTTPS 反向代理，转发到 `127.0.0.1:8090`，并允许 WebSocket 升级。如果代理和 Hub 不在同一个宿主网络中，将其上游改为代理实际可访问的地址。将下面内容写入 Hub 的 `.env`，再执行 `docker compose up -d`：

```dotenv
HUB_URL=https://monitor.example.com
```

用实际域名替换示例。`HUB_URL` 会写进 Hub 生成的 Agent 安装配置；远端 Agent 不能用 Hub 的 `localhost`。如果 8090 已被旧服务占用，先在 `.env` 设 `HUB_PORT=18095`，同时调整反代上游或 SSH 转发的远端端口。仅在明确需要时才修改 `HUB_BIND`。初次创建管理员前，保持入口仅自己可访问。

## 2. 从零部署 VPS 上的 Agent

1. 在新版 Hub 中点 **添加系统**，填入 VPS 名称与主机地址，使用该条目生成的新 Token。选择 **复制 Docker Compose**，并保存系统记录。也可以用新版 Hub 的通用 Token 自动注册。
2. 在 VPS 上 `mkdir -p ~/vpsmonitor/agent`，进入该目录，将复制的内容保存为 `compose.yml`。该文件包含 Token，设置 `chmod 600 compose.yml`。
3. 核对 `HUB_URL` 是这台 VPS 实际可达的新版地址，再执行：

```sh
cd ~/vpsmonitor/agent
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=50
```

Hub 生成的配置应包含 `image: ghcr.io/codeaix/vpsmonitor-agent:v0.1.0-beta.1`、`network_mode: host` 和持久化数据挂载；不会出现 `build:`。默认关闭 Agent 的 SSH 监听，不需要在 VPS 新增 Agent 入站防火墙规则。Compose 中的 Docker socket 挂载用于容器监控，不需要这项功能可去掉该挂载。

也可下载 [Agent Compose 模板](https://github.com/CodeAIX/vpsmonitor/releases/download/v0.1.0-beta.1/agent.compose.yml) 并保存为 `compose.yml`，自行配置 `.env`：

```dotenv
KEY='填入新版 Hub 公钥'
TOKEN='填入新版 Hub 生成的 Agent Token'
HUB_URL=https://monitor.example.com
# TRAFFIC_INTERFACE=ens7
```

模板方式的数据目录是 `agent_data/`，Hub 生成方式的是 `vpsmonitor_agent_data/`。选定一种方式后继续使用同一个文件和目录，避免意外换卷。`KEY` 是公钥；不要复制 Hub 私钥。`.env` 同样应设权限 `0600`。

## 3. 验收流量与连接

安装后等待 1–2 分钟，在 Hub 确认该设备在线，CPU、内存等开始显示，流量状态正常。再产生少量 VPS 网络流量，等采样刷新后检查 RX/TX 是否增长。

- Agent 默认选择唯一符合条件的 IPv4 默认出口，排除 Docker 网桥、veth、回环和常见隧道。IPv6-only、多出口或自动识别不符时，在复制的 Compose 中把 `VPSMONITOR_TRAFFIC_INTERFACE: auto` 改为实际宿主出口名（如 `ens7`）；下载模板时改 `.env` 的 `TRAFFIC_INTERFACE`。再执行 `docker compose up -d`。
- 首次启动只建立基线，日值从安装后开始。配额、双向/仅出站和账期日在 Hub 的系统流量设置中填写。当前以 UTC 日统计，安装当日的周期可能显示不完整；用量可能与云厂商账单有差异。
- 在 Agent 目录可以运行 `docker compose exec -T "$(docker compose config --services)" /agent health`。它检查连接标记；仍以 Hub 设备在线、资源刷新、实际流量增长作为验收。失败时检查 Hub 地址、TLS/WebSocket 代理和新 Token，再看 Agent 日志。

## 4. 更新和回退 vpsmonitor

等下一个版本发布后，Hub 和 Agent 分别在各自原目录修改**同一份**部署配置：

- 下载模板方式：把 `.env` 中 `VPSMONITOR_VERSION` 设为新发布页给出的完整标签（含 `v`）。保留已有 `KEY`、`TOKEN`、`HUB_URL` 和端口配置。
- Hub 生成的 Agent Compose：只改 `image:` 的版本标签。保留当前目录、服务名、挂载和身份配置。

在每台对应主机执行：

```sh
docker compose config --quiet
docker compose pull && docker compose up -d
docker compose ps
```

建议先更新 Hub 并确认健康，再更新一台 Agent 验证后继续。更新保留持久卷、账本和七天资源缓存。不要用 `down -v` 或清空目录更新。需要回退时先查目标版本的数据库兼容说明；如果新版改动了数据格式，用升级前的备份恢复对应数据目录，再切回旧镜像标签。

## 5. 卸载旧 Beszel+

确认该 VPS 在新版在线且流量增长后，在该 VPS **只处理旧 Agent**。等所有要保留监控的 VPS 都接入新版后，再到旧 Hub 所在机器单独卸载旧 Hub。以下步骤不会迁移旧历史。

先列出容器，按旧镜像和名字确认目标，别选到 `vpsmonitor`：

```sh
docker ps -a --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}'
OLD_CONTAINER=这里替换成已确认的旧容器名
docker inspect --format '{{.Config.Image}}' "$OLD_CONTAINER"
docker inspect --format '{{range .Mounts}}{{println .Type .Name .Source "->" .Destination}}{{end}}' "$OLD_CONTAINER"
```

- **旧 Compose 部署**：在旧项目目录运行 `docker compose ps`（使用原来的 `-f`、项目名及覆盖文件），确认旧服务名。运行 `docker compose stop <旧服务名>`，确认停的是旧服务，再运行 `docker compose rm <旧服务名>`。删除或停用旧的 Compose 配置，避免下次启动时重建它。与其它业务共用项目时，不要对整个项目执行 `down`。
- **旧 `docker run` 部署**：确认上面的目标和挂载后，执行 `docker stop "$OLD_CONTAINER"`，再执行 `docker rm "$OLD_CONTAINER"`。若用 fnOS Docker 界面，按同一容器名和镜像定位，先停用再移除旧容器。
- **丢弃旧数据**：容器移除后，按 `inspect` 输出，仅删除确认由旧 Beszel+ 独占的数据目录或具名卷。具名卷可用 `docker volume rm <已确认的旧数据卷>`；绑定目录可用文件管理器删除该确切目录。不要删除 Docker socket、共享目录或新版的 `hub_data`/`agent_data`/`vpsmonitor_agent_data`。旧 vnStat 无需改动；若还有其他工具依赖它，继续保留。

如果仍需试用一段时间，可先只停旧服务，验收后再删除其数据。不要用 `docker system prune` 或 `docker volume prune` 批量卸载。

开发者若要从源码构建，见 [源码部署指南](SOURCE-DEPLOY.md)。
