# vpsmonitor beta.4 正式覆盖升级执行单

交给智能网管理助手执行。目标是将正式 fnOS Hub 和十台 Agent 从 beta.3 原位更新到 **v0.1.0-beta.4**。先冷备并升级 Hub，确认 **十台仍运行 beta.3 的 Agent 都在线、实时采样正常、历史游标推进**，再升级首台 Agent。首台完整验收后，其余逐台更新。使用原账号、原 Compose 项目、原服务、原数据卷和原流量设置。每次只停止当前待升级的服务。

本轮复用当前实际可用的入口。按上一轮验收记录，网页登录为 `http://10.10.10.10:8090`，Agent 使用 `http://100.66.66.1:8090`，经 Serve 转至 fnOS 的 `127.0.0.1:28091`，Docker 再映射到 Hub 容器的 `8091`。如果现场已另行完成入口迁移，以升级前实测为准，保留该拓扑。此次版本更新不安排端口迁移、防火墙、Serve 或访问规则调整。

`127.0.0.1:28091:8091` 的前两个字段是宿主机监听地址和端口，末尾是容器端口。若宿主机的 `127.0.0.1:8091` 可用，也可以配成 `127.0.0.1:8091:8091`，同时让 Serve 改指向该端口。正式环境目前用的是 `28091`，更新镜像时应保留实际映射和目标。[Docker 端口发布](https://docs.docker.com/engine/network/port-publishing/) · [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve)

目标镜像如下。以 beta.4 发布页的 **OCI 索引摘要**为准，可直接使用 `镜像名@sha256:…` 固定版本。若当前项目通过版本变量生成镜像引用，可保留模板并改用 beta.4 标签，拉取后核对对应摘要。镜像支持 Linux amd64 与 arm64。[beta.4 发布页](https://github.com/CodeAIX/vpsmonitor/releases/tag/v0.1.0-beta.4)

```text
ghcr.io/codeaix/vpsmonitor-hub:v0.1.0-beta.4
ghcr.io/codeaix/vpsmonitor-agent:v0.1.0-beta.4
```

## 1. 记录基线和回退点

- 确认十台原设备均在线，记录设备 ID、版本、最近资源样本时间、历史同步状态的 `cursor`、`newest` 和 `retention_seconds`。每台选一个升级前可查询的历史窗口。记录计量网卡、流量来源标识、配额、计量方向、结算日及账期累计。
- 确认现有网页登录、Agent URL、宿主回环映射和 Serve 状态。保存原 Compose 文件、覆盖文件、环境文件与引用的本地配置，并记录原 Docker 上下文、项目名、服务名、调用参数、实际挂载、旧镜像 ID 和 OCI 摘要。保留旧镜像在本机，必要时加一个本地回退标签。现有备份目录位于数据卷之外，权限应为 `0700`，完整配置与凭据留在各宿主的私有备份目录中。
- 每台只编辑该服务的镜像引用或版本变量。编辑前后用 `docker compose config --format json` 在受保护目录内比较，确认服务配置差异只涉及镜像。用原项目拉取、核对摘要后才停服务。拉取失败或配置有额外差异时恢复配置，保持旧服务运行。

以下命令在原项目目录下逐段执行。替换占位符并填齐原有的 `-f`、`--env-file` 和 Docker 上下文。不同主机分别填写。任一命令或核对失败即暂停当前阶段。

```bash
DC=(docker compose -p ORIGINAL_PROJECT -f ./ORIGINAL_COMPOSE.yml)
svc=ORIGINAL_SERVICE
backup=/ABSOLUTE/PRIVATE_BACKUP_DIRECTORY
umask 077
mkdir -m 700 -p "$backup"
"${DC[@]}" config --quiet
cid=$("${DC[@]}" ps -q "$svc")
# 确认 cid 只有一个，且是要升级的正式容器。
docker inspect --format '{{.Config.Image}} {{.Image}} {{json .HostConfig.PortBindings}}{{range .Mounts}}{{println}}{{.Source}} -> {{.Destination}}{{end}}' "$cid"
old_id=$(docker inspect --format '{{.Image}}' "$cid")
rollback="vpsmonitor-local-rollback:${svc}-$(date -u +%Y%m%dT%H%M%SZ)"
docker image tag "$old_id" "$rollback"
printf '%s\n' "$old_id" > "$backup/old-image-id"
printf '%s\n' "$rollback" > "$backup/rollback-ref"

# 备份配置并确认只有镜像差异后执行以下命令。
"${DC[@]}" config --quiet
"${DC[@]}" config --images
"${DC[@]}" pull "$svc"
# 对解析出的目标镜像执行 image inspect，和发布页摘要比较后继续。
```

## 2. 先覆盖 Hub，验收十台旧 Agent

1. 停止原 Hub 服务，确认容器已停止且数据卷无写入者。冷备整个 `/vpsmonitor_data` 的实际挂载源，包含 SQLite 数据库、存在的 WAL/SHM、Hub 私钥和现有 `config.yml`。归档放在卷外，校验 SHA-256，解包到受保护的临时目录检查文件与 SQLite `PRAGMA quick_check;`。备份失败时恢复旧镜像配置，启动旧 Hub。
2. 用原 Compose 项目和原卷启动 beta.4。检查容器实际镜像、挂载源、端口映射和重启策略。用原账号打开原网页并确认版本，检查首页流量悬停、原设备、历史和流量设置。确认原 Serve 后端仍可达。
3. **保留十台 beta.3 Agent，逐台验证混跑。** 核对原设备 ID 和 beta.3 版本。每台至少收到两条 Hub 切换后的新资源样本，并确认历史同步状态的 `retention_seconds=604800`、游标推进、可查询切换前的历史。游标和样本时间按实际时间比较，不用容器健康或网页在线灯代替。建议预留约十分钟确认全部推进。某台不符合时暂停 Agent 更新，检查连接、日志和同步状态，必要时回退 Hub。

冷备与重建模板也可用于 Agent。选填与实际挂载一致的 `source_spec`。具名卷先用 `docker volume inspect` 确认已存在。备份辅助镜像须事先拉取。

```bash
source_spec='type=bind,src=/ACTUAL/ORIGINAL/DATA'
# source_spec='type=volume,src=ACTUAL_ORIGINAL_VOLUME'
backup_image=alpine:3.24
"${DC[@]}" stop "$svc"
test "$(docker inspect --format '{{.State.Running}}' "$cid")" = false
docker run --rm --pull never --network none \
  --mount "$source_spec,dst=/data,readonly" "$backup_image" \
  tar -C /data -cpf - . > "$backup/data.tar"
tar -tf "$backup/data.tar" > "$backup/archive-files.txt"
(cd "$backup" && sha256sum data.tar > data.tar.sha256 && sha256sum -c data.tar.sha256)

# 解包核验并确认原配置无误后再执行。
"${DC[@]}" up -d --no-deps --pull never "$svc"
"${DC[@]}" ps "$svc"
```

## 3. 首台通过后逐台覆盖 Agent

每台沿用自己的原项目、服务、`/var/lib/vpsmonitor-agent` 挂载、`KEY`、`TOKEN`、`HUB_URL`、宿主网络及显式计量网卡。停止后归档完整数据卷，核对 `history-v1` 和原流量账本在归档中，再仅替换镜像。新 Agent 会继续读取旧分钟文件，并逐步累积 31 天。不要重建系统记录或执行 `docker compose down -v`。

每台至少核对以下项目，首台全部通过才继续其余九台。

- 原设备 ID 重连，Agent 实际版本为 beta.4，Hub 收到至少两条该台更新后的新资源样本。用 `/agent health` 辅助检查连接。
- 历史状态显示 `retention_seconds=2678400`，同步游标推进，当天已有新增分钟记录，升级前的历史窗口仍能查到。当天 UTC 归档随采样增加，旧分钟缓存可继续读取。刚升级显示 31 天保留上限，实际覆盖由可用样本决定。
- 原流量来源、网卡、配额、方向、结算日与账期保留，产生少量实际网络流量后，原累计继续增加。出站套餐核对 TX，双向套餐核对 RX/TX。
- 留存升级前后镜像摘要、原挂载、样本时间、历史游标、验收结果和回退点。任一节点失败时暂停队列，优先回退当台并复验。UTC 跨天、31 天边界及短时断联已有本地验证。生产验收可用新样本和归档证明同步，无需专门停整个 Hub 模拟断联。

## 4. 回退

优先恢复旧镜像和原 Compose 配置，沿用当前数据卷。单台 Agent 异常仅回退当台；Hub 阶段失败时，十台 Agent 仍在 beta.3，直接回退 Hub。整体有序回退时，先逐台把已升级的 Agent 恢复为 beta.3 并验收，再回退 Hub；若 Hub 已不可用，先恢复 Hub 服务，再处理已升级的 Agent。

恢复原 Compose 与环境配置，确认解析的镜像和备份记录中的旧 ID 相符，再执行 `up -d --no-deps --pull never`。如果原镜像引用已经变动，用已保存的本地回退标签临时覆盖该服务的 `image`。

```bash
rollback=$(cat "$backup/rollback-ref")
test "$(docker image inspect --format '{{.Id}}' "$rollback")" = "$(cat "$backup/old-image-id")"
printf 'services:\n  "%s":\n    image: "%s"\n' "$svc" "$rollback" > "$backup/rollback.compose.yml"
"${DC[@]}" -f "$backup/rollback.compose.yml" config --quiet
"${DC[@]}" -f "$backup/rollback.compose.yml" up -d --no-deps --pull never "$svc"
```

保留 Agent 卷内所有 `.daylog` 文件。beta.3 Agent 只能读取旧分钟文件，回退期间这部分新版归档暂不能补传，重新升级后仍可读取有效窗口内的归档。旧 Hub 的清理窗口较短，整体降级前另做一份当前 Hub 的冷备。回退后用原账号、原入口和原设备 ID 验证版本、历史和新样本。若镜像回退仍无法恢复且证据指向数据卷损坏，先保存故障现场卷，核验原冷备，记录恢复会舍弃的样本范围，再按批准范围恢复到原挂载源。

最终汇报 Hub 和十台 Agent 逐台版本、实时样本、历史状态、流量设置和累计值核对结果，以及冷备与回退点。生产长时间断联和整机重启应单独记录实测范围。
