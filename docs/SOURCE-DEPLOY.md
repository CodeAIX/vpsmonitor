# vpsmonitor 1.0.0 源码构建

本文使用 1.0.0 源码在 Linux 容器中构建并验证。正式部署使用带 Tailnet 入口限制的发布镜像。见 [手动安装](DEPLOY.md)。

## 1. Hub

```sh
git clone --branch v1.0.0 https://github.com/CodeAIX/vpsmonitor.git
cd vpsmonitor
VERSION=1.0.0 ./scripts/vpsmonitorctl hub start
```

默认仅监听 `127.0.0.1:8090`。在本机打开该地址并创建管理员。远程访问可设置反向代理，或在 `deploy/.env` 中明确设置所需绑定与对外 URL，再启动：

```dotenv
HUB_BIND=0.0.0.0
HUB_PORT=8090
HUB_URL=https://monitor.example.com
```

示例域名须替换为实际地址。Hub 的新数据保存在 `deploy/hub_data`。不要将旧 Beszel 数据目录挂载到新 Hub。

## 2. 每台 VPS 的 Agent

在 Hub 的“添加系统”或“设置 → 通用 Token”中复制 Docker Compose 配置，在 VPS 的一个新目录保存为 `compose.yml`，执行：

```sh
docker compose up -d --build
docker compose ps
```

配置已包含：宿主网络、原生流量采集、Agent 持久卷、31 天资源缓存，以及主动连接 Hub。默认关闭 Agent SSH 监听，无需为 Agent 新开放入站端口。Hub URL 必须是 VPS 实际可访问的地址，不能使用另一台机器的 localhost。

新 Agent 不需要 vnStat、宿主 vnstatd、UTC 配置或 vnStat 数据库挂载。Linux 宿主网卡计数直接记录到 `/var/lib/vpsmonitor-agent/traffic/`。默认忽略 Docker、veth、回环及常见隧道接口，从最低 metric 的有效 IPv4 默认路由选择唯一出口；支持 bond/VLAN 出口，不把所有接口相加。

IPv6-only、多默认出口等无法明确推断的情况，需要在该 Agent Compose 中将 `VPSMONITOR_TRAFFIC_INTERFACE: auto` 改成实际宿主出口名，例如 `ens7`。隧道接口不作为本版计量口；使用 Tailscale 等复杂路由时需明确选择其底层宿主出口。该参数选择的是宿主接口，而不是普通桥接容器 eth0。安装配置使用 `network_mode: host`；仅添加标记环境变量不能代替宿主网络。

也可在 VPS 克隆本仓库，使用维护脚本。在权限为 0600 的 `deploy/.env` 中填写 Hub 页面提供的 KEY、TOKEN、HUB_URL，可选 TRAFFIC_INTERFACE，然后：

```sh
VERSION=1.0.0 ./scripts/vpsmonitorctl agent start
./scripts/vpsmonitorctl agent doctor
```

KEY 填 Hub 页面提供的公钥。配置文件包含连接凭据，保存在设备本地，不提交 Git。Docker socket 挂载用于容器监控，不需要容器监控时可删除该挂载。

## 3. 确认接入

安装后等待 1–2 分钟：设备在线；流量状态正常；唯一出口自动成为 Hub 的计量接口；日值从安装时的新基线累计。空闲时零流量合法。配额、双向/仅出站与账期起始日可在 Hub 中设置；配置导出导入继续可用于新版自身备份。

CPU、内存、磁盘、负载、网速、Docker、在线状态及流量是默认监控项目。

## 4. 诊断

在源码目录使用 `./scripts/vpsmonitorctl hub doctor` 或 `./scripts/vpsmonitorctl agent doctor` 检查实际容器、持久卷和进程。Agent 另检查宿主网络和账本。安装后在 Hub 确认设备连接、资源样本和流量累计。

## 数据边界

- 第一次观察仅建立基线，不把宿主开机以来的累计流量算成安装后的用量。
- 每 30 秒采样并原子持久化基线和 UTC 日值，日值独立保留最多 400 天，不随资源快照删除。
- 正常采样跨午夜以区间终点归日，存在采样分辨率范围内的边界误差；不保证与云厂商账单完全一致。
- Agent 重启后，内核计数连续且持久卷保留时，可恢复同日累计差额。长时间停采跨日的差额保存为未分配量，相关日期标为不完整，不猜测每天的分配。
- 宿主重启、接口身份/计数复位、时钟回退或存储异常会重新建立基线或暂停采集，并标记异常/不完整。硬断电可能丢失未落盘的最后采样区间。
- Docker/宿主网络变更后应重新核对计量接口。不要用修改配置的方式将两条不同接口的历史视为同一来源。
