# vpsmonitor 镜像发布

1. 将变更合入 `main`，确认该提交的完整 CI 通过。准备新的版本标签（如 `v0.1.0-beta.2`），更新发布用 Compose 的默认版本与文档。
2. 为当前 `main` 提交创建并推送 Git 标签。`images.yml` 验证标签格式与来源提交，独立构建 Hub/Agent 的 `linux/amd64,linux/arm64` 镜像，推送到 `ghcr.io/codeaix/vpsmonitor-{hub,agent}:<标签>`。不推送浮动 `latest`。
3. 记录两个 OCI index digest，并验证匿名拉取、版本和新部署。执行容器重建，核对持久卷及 Agent 身份。若软件包是首次发布，先确认其可见性。若需要修复已公开的构建，用新的版本标签。
4. 在 GitHub Release 附上两份无凭据 Compose、部署指南和 `SHA256SUMS`；测试版标为 pre-release。Release 正文记录镜像地址、digest 和验证范围。

工作流仅镜像任务使用 `packages: write` 和 GitHub 运行时令牌，无长期发布令牌。构建阶段使用运行器架构执行工具链并交叉编译 Linux 二进制。镜像任务核对 OCI index 的两个目标架构，并在 amd64 运行器执行镜像版本检查。

外部 Action：[actions/checkout v7.0.1](https://github.com/actions/checkout/releases/tag/v7.0.1)，固定到 `3d3c42e5aac5ba805825da76410c181273ba90b1`（2026-11-07 核对）；`images.yml` 使用 actionlint v1.7.12 和实际 GitHub Actions 运行验证。
