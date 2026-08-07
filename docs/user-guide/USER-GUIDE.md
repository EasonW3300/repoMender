# RepoMender 使用说明书

版本：2026-08-07

这份说明书面向第一次接触 RepoMender 的用户，目标是帮助你从“启动系统”走到“让一个 GitHub Pull Request 被受治理地分析”。

## 1. 先理解 RepoMender

RepoMender 不是一个普通聊天窗口，也不是 GitHub 的替代品。它是一个工程自动化控制平面：

- GitHub 负责代码、Pull Request、Issue 和 CI 事件；
- RepoMender 负责任务、审批、策略、审计、证据和结果；
- Agent Compose 负责隔离运行 Agent；
- Codex Agent 负责实际的代码分析或修复；
- PostgreSQL 负责持久化任务和运行历史。

典型流程如下：

```text
GitHub 事件
  → RepoMender 验证并去重
  → 创建任务和审计记录
  → Worker 领取任务
  → Agent Compose 隔离执行
  → 保存日志、发现和证据
  → 高风险动作等待人工审批
  → 回写 GitHub 状态、评论或补丁
```

RepoMender 当前是自托管、单租户系统，首发 SCM 范围是 GitHub.com。GitLab 适配器保留在代码中，但默认关闭。自动合并、GHES、多租户和计费不属于当前使用范围。

## 2. 使用前准备

本地体验需要：

- Docker Desktop 和 Docker Compose；
- Git；
- Node.js 24、Go 1.26（只有直接开发或运行测试时需要）。

完整工程自动化还需要：

- 一个 GitHub App；
- 一个 GitHub 测试仓库；
- 独立运行的 Agent Compose；
- 在 Agent Compose 中配置的 Codex 凭据。

建议先使用专门的测试仓库，不要一开始连接生产代码库。

## 3. 下载并启动

```bash
git clone https://github.com/EasonW3300/repoMender.git
cd repoMender
cp .env.example .env
```

编辑 `.env`，至少把开发密码改掉：

```text
REPOMENDER_POSTGRES_PASSWORD=请替换为强密码
```

本地首次体验可以保留：

```text
REPOMENDER_PUBLIC_URL=http://localhost:8088
REPOMENDER_COOKIE_SECURE=false
```

启动服务：

```bash
docker compose up -d --build
docker compose ps
```

查看日志：

```bash
docker compose logs -f api
docker compose logs -f worker
```

健康检查：

```bash
curl http://localhost:8088/health/live
curl http://localhost:8088/health/ready
```

两个接口都正常后，打开：<http://localhost:8088>。

停止服务但保留数据：

```bash
docker compose down
```

不要随意使用 `docker compose down -v`，它会删除 PostgreSQL 数据卷。

## 4. 创建第一个管理员

首次打开：<http://localhost:8088/login>

选择：

```text
First installation? Create administrator
```

填写管理员邮箱和至少 12 位密码，然后登录。

这是一次性初始化流程。管理员创建后，bootstrap 页面会关闭。管理员账号用于系统配置、审批和自动化管理。

## 5. 先做一次“基础体验”

默认 `.env.example` 会关闭 M2-M10 业务模块，所以第一次启动主要验证：

1. Web 页面是否能打开；
2. 管理员是否能登录；
3. `/health/live` 和 `/health/ready` 是否正常；
4. PostgreSQL 是否能正常保存用户数据。

此时还不能真正分析 GitHub Pull Request，也不能运行 Codex。不要把“页面能打开”误认为“AI 工作流已经配置完成”。

## 6. 连接 GitHub

### 6.1 创建 GitHub App

在 GitHub 创建 GitHub App，使用实际部署地址配置：

```text
Homepage URL:
https://你的RepoMender域名

Setup URL:
https://你的RepoMender域名/api/v1/scm/github/callback

Webhook URL:
https://你的RepoMender域名/webhooks/github
```

建议订阅这些事件：

- Pull request；
- Push；
- Issues；
- Workflow run；
- Check run；
- Check suite。

建议授予 Contents、Pull requests、Issues、Checks、Commit statuses、Actions 和 Metadata 所需权限。权限应尽量限制在测试仓库。

### 6.2 配置 RepoMender

生成 32 字节主密钥：

```bash
openssl rand -base64 32
```

写入 `.env`：

```text
REPOMENDER_FEATURE_M2_SCM=true
REPOMENDER_MASTER_KEY=上一步生成的值
REPOMENDER_GITHUB_APP_ID=GitHub App ID
REPOMENDER_GITHUB_APP_SLUG=GitHub App slug
REPOMENDER_GITHUB_WEBHOOK_SECRET=Webhook secret
```

GitHub App 私钥二选一：

```text
REPOMENDER_GITHUB_APP_PRIVATE_KEY_B64=私钥的base64内容
```

或者使用挂载文件：

```text
REPOMENDER_GITHUB_APP_PRIVATE_KEY_FILE=/run/secrets/github-app.pem
```

私钥不能同时配置两份，也不能提交到 Git。

修改后重启：

```bash
docker compose up -d --build
```

登录后打开 `/repositories`，安装 GitHub App 并选择测试仓库。同步成功后，RepoMender 才能读取仓库快照和接收相关事件。

如果 GitHub 无法访问你的 Webhook URL，需要使用一个经过批准的 HTTPS Tunnel 或部署到公网 HTTPS 域名，并同步更新 `REPOMENDER_PUBLIC_URL`。

## 7. 连接 Agent Compose

RepoMender 不直接保存 Codex 密钥。先在独立环境启动 Agent Compose：

```bash
agent-compose up \
  --host http://127.0.0.1:7410 \
  --file deploy/agent-compose.repomender.yml
```

在 Agent Compose 中完成 Codex 登录、模型和项目 secret 配置。

然后在 RepoMender `.env` 中配置：

```text
REPOMENDER_FEATURE_M3_AC_EXECUTION=true
REPOMENDER_AC_BASE_URL=http://host.docker.internal:7410
REPOMENDER_AC_AGENT_NAME=codex
REPOMENDER_AC_REQUIRED_DRIVER=docker
REPOMENDER_AC_REQUEST_TIMEOUT=10
```

如果 AC 开启 Bearer 认证：

```text
REPOMENDER_AC_AUTH_TOKEN=AC token
```

确认 AC 可访问：

```bash
docker compose logs -f api worker
curl http://localhost:8088/health/ready
```

如果 M3 启用后 readiness 失败，优先检查 AC 地址、端口、token 和 Docker 网络访问。

## 8. 按顺序开启业务模块

模块有严格依赖关系，推荐按以下顺序开启：

```text
M2 SCM
  → M3 Agent Compose
  → M4 Tasks
  → M5 Code Review
  → M6 CI Diagnosis
  → M7 Approvals
  → M8 Issue Repair
  → M9 Automations
  → M10 Hardening
```

完整配置示例：

```text
REPOMENDER_FEATURE_M2_SCM=true
REPOMENDER_FEATURE_M3_AC_EXECUTION=true
REPOMENDER_FEATURE_M4_TASKS=true
REPOMENDER_FEATURE_M5_CODE_REVIEW=true
REPOMENDER_FEATURE_M6_CI_DIAGNOSIS=true
REPOMENDER_FEATURE_M7_APPROVALS=true
REPOMENDER_FEATURE_M8_ISSUE_REPAIR=true
REPOMENDER_FEATURE_M9_AUTOMATIONS=true
REPOMENDER_FEATURE_M10_HARDENING=true
```

启用 M5 及以上模块时，还需要：

```text
REPOMENDER_AC_PROJECT_ID=Agent Compose 项目 ID
```

M5/M6/M8/M9 需要 GitHub App、M3 AC 和 M4 任务能力；M7 依赖 M4 和 M6；M10 依赖 M9。配置不满足时，API 会拒绝启动并给出错误信息。

## 9. 第一次真实工作流

### 9.1 代码审查

1. 在已连接仓库中创建 Pull Request。
2. GitHub 发送 Pull Request Webhook。
3. RepoMender 验证、去重并创建任务。
4. Worker 领取任务并调用 AC。
5. AC 在隔离沙箱中分析代码。
6. RepoMender 保存日志、发现、证据和审计记录。
7. 结果回写 GitHub 状态或评论。
8. 在 `/reviews` 查看结果。

### 9.2 CI 失败诊断

1. 让测试仓库中的 GitHub Actions 产生一次失败。
2. 确保 GitHub App 订阅 Workflow run、Check run 或 Check suite。
3. RepoMender 创建 CI 诊断任务。
4. 在 `/diagnostics` 查看失败原因、证据和建议。

### 9.3 Issue 修复

1. 在 GitHub 创建一个测试 Issue。
2. 让 RepoMender 创建修复请求。
3. Agent 生成修复计划和补丁。
4. 高风险操作进入 `/approvals`。
5. Admin 或 Maintainer 审批后，流程才能继续。

不要在生产仓库第一次测试 Issue 修复。先使用允许丢弃的测试分支和测试仓库。

## 10. 角色和权限

| 角色 | 主要用途 |
| --- | --- |
| Admin | 系统配置、管理员操作、审批、自动化管理 |
| Maintainer | 维护任务、执行受控操作、审批部分高风险动作 |
| Developer | 查看和发起普通工程任务 |
| Auditor | 查看任务、运行和审计记录，不能执行敏感变更 |

原则是：开发者可以请求分析，但不能绕过审批让 Agent 直接修改受保护分支。

## 11. 日常运维

常用命令：

```bash
docker compose ps
docker compose logs -f api
docker compose logs -f worker
docker compose up -d --build
docker compose down
```

M10 启用后：

- `/health/version` 查看服务版本；
- `/metrics` 查看指标；
- `REPOMENDER_METRICS_TOKEN` 可保护指标端点；
- `REPOMENDER_RATE_LIMIT_PER_MINUTE` 控制 API 限流；
- `REPOMENDER_RETENTION_DAYS` 控制历史数据保留。

备份：

```bash
REPOMENDER_DATABASE_URL='postgres://…' \
BACKUP_FILE='/secure/backups/repomender.dump' \
./scripts/backup.sh
```

恢复是破坏性操作，需要明确确认：

```bash
REPOMENDER_DATABASE_URL='postgres://…' \
BACKUP_FILE='/secure/backups/repomender.dump' \
CONFIRM_RESTORE=YES ./scripts/restore.sh
```

恢复前停止 API 和 Worker，确认备份文件和目标数据库地址，恢复后重新检查迁移和健康状态。

## 12. 故障排查

### API 启动失败

```bash
docker compose logs api
```

重点检查 `.env`、PostgreSQL 密码、GitHub App 配置、AC 配置以及 feature flag 依赖。

### `/health/live` 正常，但 `/health/ready` 失败

说明进程可以响应，但 PostgreSQL 或已启用的外部依赖不可用。检查：

- PostgreSQL 容器是否 healthy；
- AC 是否运行；
- AC 地址是否使用了正确的 Docker 主机地址；
- GitHub App 配置是否完整。

### GitHub 没有创建任务

检查：

- GitHub App 是否安装到目标仓库；
- Webhook URL 是否可从公网访问；
- Webhook secret 是否一致；
- 订阅事件是否正确；
- M2 是否启用。

### Agent 没有执行

检查：

- Agent Compose 是否启动；
- `REPOMENDER_AC_BASE_URL` 是否正确；
- `REPOMENDER_AC_PROJECT_ID` 是否正确；
- `REPOMENDER_AC_AGENT_NAME` 是否存在；
- Codex 凭据是否配置在 AC 中。

## 13. 新手推荐学习路径

1. 只启动默认 Compose 并完成管理员登录。
2. 连接一个测试 GitHub 仓库。
3. 开启 M2，验证仓库同步。
4. 启动 Agent Compose，开启 M3 和 M4。
5. 开启 M5，创建测试 Pull Request。
6. 开启 M6，测试一次 CI 失败诊断。
7. 最后测试 M7-M10 的审批、修复、自动化和运维能力。

相关文档：

- [中文技术架构](../architecture/TECHNICAL-ARCHITECTURE.md)
- [M10 运维手册](../operations/M10-RUNBOOK.md)
- [项目 Review 报告](../review/PROJECT-REVIEW-2026-08.md)
