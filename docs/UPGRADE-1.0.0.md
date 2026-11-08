# vpsmonitor beta.4 → 1.0.0 覆盖升级执行单

用于已正常运行 beta.4 的部署。目标是沿用原 Compose 项目、服务、账号、设备、Hub 私钥、Agent 凭据、数据挂载、资源历史、流量账本和用户设置。先更新 Hub，等全部旧 Agent 通过验收，再更新一台 Agent。首台通过后逐台处理其余设备。

## 1. 记录现状并准备镜像

- 在实际安装目录记录 Docker 上下文、Compose 项目、服务名、有序 `-f` 参数及环境文件。记录实际容器名、镜像 ID 和摘要、原挂载路径、网络、端口、重启策略及 Serve 状态。网页版和 Tailnet Agent URL 以实测为准，保留当前 Serve 后端和映射。例如当前已使用宿主回环 8091，则继续使用 8091。
- 保存可查询的原历史窗口和设备 ID。逐台记录版本、最新实时样本、历史 `cursor`、`newest`、`retention_seconds`、计量接口、账本来源、账期、累计用量、配额、方向及结算日。使用原账号验证网页登录。
- 拉取 [1.0.0 发布页](https://github.com/CodeAIX/vpsmonitor/releases/tag/v1.0.0) 指定的镜像并核对 OCI 摘要。可使用 `镜像名@sha256:…` 固定引用。镜像名是 `ghcr.io/codeaix/vpsmonitor-hub:v1.0.0` 和 `ghcr.io/codeaix/vpsmonitor-agent:v1.0.0`。
- 检查 `vpsmonitor-hub` 或 `vpsmonitor-agent` 是否已被另一个容器占用。确认为本次升级目标或该名称空闲后继续。若同宿主有多个同类实例，按原角色选择各自唯一的名字。Compose 原项目与服务键保持原值。

## 2. 冷备并重建当前服务

以下步骤在每个现有项目目录分别执行。用实际 Docker 上下文、项目名、Compose 文件和环境文件替换示例。先完成 Hub，再用于 Agent。

```bash
DC=(docker compose -p ORIGINAL_PROJECT -f ./ORIGINAL_COMPOSE.yml)
svc=ORIGINAL_SERVICE
backup=/ABSOLUTE/PRIVATE/BACKUP_OUTSIDE_THE_DATA_MOUNT
umask 077
mkdir -m 700 -p "$backup"
cid=$("${DC[@]}" ps -q "$svc")
test -n "$cid"
docker inspect --format '{{.Name}} {{.Image}} {{json .HostConfig.PortBindings}}{{range .Mounts}}{{println}}{{.Source}} -> {{.Destination}}{{end}}' "$cid"
old_id=$(docker inspect --format '{{.Image}}' "$cid")
rollback="vpsmonitor-local-rollback:${svc}-$(date -u +%Y%m%dT%H%M%SZ)"
docker image tag "$old_id" "$rollback"
printf '%s\n' "$old_id" > "$backup/old-image-id"
printf '%s\n' "$rollback" > "$backup/rollback-ref"
"${DC[@]}" config --format json > "$backup/compose-before.json"
```

把原 Compose 文件、环境文件和调用参数另存于私有备份。编辑实际生效的配置，只修改该服务的镜像引用和 `container_name`。用 `docker compose config --format json` 私下对比前后模型，再执行 `docker compose pull <服务>` 并核对摘要。留存完整模型时按凭据文件保护。按实际挂载填写下面的 `source_spec`，具名卷先用 `docker volume inspect` 确认存在。辅助镜像须提前可用。

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
# 解包核验原数据文件，Hub 副本执行 SQLite PRAGMA quick_check。
"${DC[@]}" up -d --no-deps --pull never "$svc"
"${DC[@]}" ps "$svc"
```

冷备要求整个挂载，无其他写入者。Hub 包含 SQLite、存在的 WAL/SHM、身份和配置。Agent 包含 `history-v1`、`.daylog`、旧分钟文件和流量账本。任一命令或核对失败即暂停，按第 5 节恢复当前服务。禁止使用 `down -v`。

## 3. Hub 验收

- 容器名为 `vpsmonitor-hub`，实际镜像和网页产品版本为 1.0.0。原挂载、网页登录入口、原账号、Hub 身份、Serve 和 Agent URL 全部通过核对。
- 保持所有 Agent 运行 beta.4。每台必须以原设备 ID 重连，至少两条切换后的独立分钟资源样本，保留上限仍是 `2678400` 秒，历史游标相对基线推进。继续查询升级前选定的历史窗口。核对原流量设置与账本来源。全部通过才开始更新 Agent。
- 使用两个已经固定时间戳的有效归档样本核对 Hub 原始分钟记录。实时尾部可能在检查过程中继续写入，验收目标为这两个样本及相对基线的推进。

## 4. Agent 逐台验收

首台使用相同的冷备及切换步骤。容器名为 `vpsmonitor-agent`。保留 `KEY`、`TOKEN`、`HUB_URL`、网卡、宿主网络和数据挂载。

- 核对实际镜像、二进制版本、设备 ID 和连接。Hub 至少收到两条不同分钟的新资源记录，历史保留上限 `2678400`，当天归档继续增长。固定两条已落盘分钟，比较 Hub 已入库的对应原始记录，另复查旧历史窗口。
- 核对原接口、来源、方向、配额、单位、UTC 结算日及账期。流量后的 Hub 日值应追上同一来源的账本快照。仅出站核对 TX，双向核对 RX 与 TX。观察到的增长可包含业务流量。
- 首台通过才逐台继续。报告每台版本、两条样本、游标、账本累计和回退点。任何设备失败先暂停队列。

## 5. 回退

单台失败时只回退当前 Agent。Hub 阶段失败时回退 Hub。需要整体回退且 Hub 仍可用时，先逐台回退 Agent，再回退 Hub。使用原数据挂载。

1. 恢复该服务原 Compose 和环境配置，包括原 `container_name`（若原来没有则移除）。确认原镜像解析到备份的旧 ID。必要时在原有 `-f` 列表末尾加临时覆盖，仅指定已保存的回退镜像标签。
2. 用同一项目执行 `docker compose up -d --no-deps --pull never <服务>`。确认原入口、设备身份、历史、流量和新样本。备份保留独立于容器和镜像。
3. 只有查明数据损坏才考虑恢复冷备。先封存当前卷，记录恢复会覆盖的样本和设置，取得用户明确授权再恢复。资源缓存和流量账本随数据卷保留。

## 6. 清理旧版残留

所有设备验收通过并达到现场约定的回退保留期后执行。保留一份已验证的冷备与配置，单独记录备份位置。

- 检查原 Compose 项目的全部容器。正常原位重建已替换旧容器。只删除经标签、镜像和挂载确认为本次旧服务且已经停止的残留容器，以及明确属于本次任务的临时探针、临时覆盖文件和已完成的升级任务。永久服务与持久卷继续使用。
- 按 `docker ps -a`、`docker inspect` 和镜像引用确认未被任何容器使用，再以确切的仓库标签、摘要引用和本次本地回退标签逐个执行 `docker image rm`。根据实际清单清理旧测试镜像。保留正在使用的 1.0.0 镜像和其他服务共享镜像。
- 保留 `hub_data`、`agent_data`、SQLite、身份文件、当前配置、资源归档、账本及正式 Serve。旧格式的分钟文件仍属于历史数据，由缓存保留策略自动回收。避免批量 prune、`down -v`、重置 Serve 或删除数据目录。清理后再确认网页登录、十台在线和新样本正常。
