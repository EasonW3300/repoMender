# RepoMender User Guide

Version: 2026-08-07

This guide is for a first-time RepoMender user. It takes you from starting the stack to having a GitHub pull request analyzed through a governed Agent Compose execution.

## 1. What RepoMender is

RepoMender is not a chat window and does not replace GitHub. It is an engineering automation control plane:

- GitHub owns source repositories, pull requests, issues, and CI events;
- RepoMender owns tasks, approvals, policy, audit records, evidence, and results;
- Agent Compose runs agents in an isolated environment;
- Codex Agent performs the actual code analysis or repair;
- PostgreSQL stores task and run history.

The normal flow is:

```text
GitHub event
  → RepoMender verifies and deduplicates it
  → task and audit record are created
  → worker claims the task
  → Agent Compose runs in an isolated sandbox
  → logs, findings, and evidence are stored
  → high-impact actions wait for human approval
  → GitHub receives a status, comment, or patch
```

RepoMender is currently self-hosted and single-tenant, with GitHub.com as the first-release SCM scope. The GitLab adapter is preserved but disabled by default. Automatic merge, GHES, multi-tenancy, and billing are outside the current scope.

## 2. Prerequisites

For a local installation:

- Docker Desktop and Docker Compose;
- Git;
- Node.js 24 and Go 1.26 only if you develop or test the project directly.

For complete engineering automation you also need:

- a GitHub App;
- a test GitHub repository;
- an independently running Agent Compose instance;
- Codex credentials configured inside Agent Compose.

Use a dedicated test repository before connecting production code.

## 3. Download and start RepoMender

```bash
git clone https://github.com/EasonW3300/repoMender.git
cd repoMender
cp .env.example .env
```

Edit `.env` and replace the development database password:

```text
REPOMENDER_POSTGRES_PASSWORD=replace-with-a-strong-password
```

For local development, these values are sufficient:

```text
REPOMENDER_PUBLIC_URL=http://localhost:8088
REPOMENDER_COOKIE_SECURE=false
```

Start the services:

```bash
docker compose up -d --build
docker compose ps
```

View logs:

```bash
docker compose logs -f api
docker compose logs -f worker
```

Check health:

```bash
curl http://localhost:8088/health/live
curl http://localhost:8088/health/ready
```

When both checks are healthy, open <http://localhost:8088>.

Stop the stack while preserving data:

```bash
docker compose down
```

Do not use `docker compose down -v` casually; it deletes the PostgreSQL data volume.

## 4. Create the first administrator

Open <http://localhost:8088/login> and select:

```text
First installation? Create administrator
```

Enter an administrator email and a password of at least 12 characters, then sign in.

This is a one-time bootstrap flow. Once the administrator exists, bootstrap is closed. The administrator account is used for system configuration, approvals, and automation administration.

## 5. Start with a basic installation

The default `.env.example` disables the M2-M10 business modules. The first run therefore validates:

1. the Web console loads;
2. the administrator can sign in;
3. `/health/live` and `/health/ready` are healthy;
4. PostgreSQL can persist user data.

At this stage RepoMender cannot analyze a real GitHub pull request or run Codex. A working web page does not mean the AI workflow has been configured.

## 6. Connect GitHub

### 6.1 Create a GitHub App

Create a GitHub App and use the actual RepoMender origin:

```text
Homepage URL:
https://your-repomender-domain

Setup URL:
https://your-repomender-domain/api/v1/scm/github/callback

Webhook URL:
https://your-repomender-domain/webhooks/github
```

Subscribe to:

- Pull request;
- Push;
- Issues;
- Workflow run;
- Check run;
- Check suite.

Grant only the required Contents, Pull requests, Issues, Checks, Commit statuses, Actions, and Metadata permissions, preferably restricted to a test repository.

### 6.2 Configure RepoMender

Generate a 32-byte master key:

```bash
openssl rand -base64 32
```

Put it in `.env`:

```text
REPOMENDER_FEATURE_M2_SCM=true
REPOMENDER_MASTER_KEY=generated-value
REPOMENDER_GITHUB_APP_ID=github-app-id
REPOMENDER_GITHUB_APP_SLUG=github-app-slug
REPOMENDER_GITHUB_WEBHOOK_SECRET=webhook-secret
```

Provide exactly one private-key source:

```text
REPOMENDER_GITHUB_APP_PRIVATE_KEY_B64=base64-private-key
```

or:

```text
REPOMENDER_GITHUB_APP_PRIVATE_KEY_FILE=/run/secrets/github-app.pem
```

Never commit the private key to Git.

Restart after editing `.env`:

```bash
docker compose up -d --build
```

Sign in and open `/repositories`. Install the GitHub App and select the test repository. RepoMender must successfully synchronize the repository before it can process its events.

If GitHub cannot reach your Webhook URL, use an approved HTTPS tunnel or a public HTTPS deployment and update `REPOMENDER_PUBLIC_URL` accordingly.

## 7. Connect Agent Compose

RepoMender does not store Codex credentials. Start Agent Compose separately:

```bash
agent-compose up \
  --host http://127.0.0.1:7410 \
  --file deploy/agent-compose.repomender.yml
```

Complete Codex login, model, and project-secret configuration inside Agent Compose.

Then configure RepoMender:

```text
REPOMENDER_FEATURE_M3_AC_EXECUTION=true
REPOMENDER_AC_BASE_URL=http://host.docker.internal:7410
REPOMENDER_AC_AGENT_NAME=codex
REPOMENDER_AC_REQUIRED_DRIVER=docker
REPOMENDER_AC_REQUEST_TIMEOUT=10
```

If AC requires Bearer authentication:

```text
REPOMENDER_AC_AUTH_TOKEN=ac-token
```

Check the connection:

```bash
docker compose logs -f api worker
curl http://localhost:8088/health/ready
```

If readiness fails after enabling M3, check the AC address, port, token, and Docker network access first.

## 8. Enable business modules in order

The modules have strict dependencies:

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

Example complete configuration:

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

When M5 or later is enabled, also configure:

```text
REPOMENDER_AC_PROJECT_ID=agent-compose-project-id
```

M5/M6/M8/M9 require the GitHub App, M3 AC, and M4 task capabilities. M7 depends on M4 and M6. M10 depends on M9. If dependencies are missing, the API rejects startup and reports the configuration error.

## 9. Run your first real workflow

### 9.1 Code review

1. Create a pull request in the connected repository.
2. GitHub sends a pull-request webhook.
3. RepoMender verifies, deduplicates, and creates a task.
4. The worker claims the task and calls AC.
5. AC analyzes the code in an isolated sandbox.
6. RepoMender stores logs, findings, evidence, and audit records.
7. The result is sent back to GitHub as a status or comment.
8. Open `/reviews` to inspect the result.

### 9.2 CI diagnosis

1. Cause a test workflow in the test repository to fail.
2. Confirm the GitHub App subscribes to Workflow run, Check run, or Check suite events.
3. RepoMender creates a CI diagnosis task.
4. Open `/diagnostics` to inspect the cause, evidence, and recommendations.

### 9.3 Issue repair

1. Create a test issue in GitHub.
2. Create a repair request in RepoMender.
3. The agent generates a repair plan and patch.
4. High-impact actions appear in `/approvals`.
5. An Admin or Maintainer approves or rejects the action.
6. The workflow continues only after approval.

Do not use a production repository for the first issue-repair test. Use a disposable branch and test repository.

## 10. Roles and permissions

| Role | Main use |
| --- | --- |
| Admin | System configuration, administrator operations, approvals, automation administration |
| Maintainer | Task operations, governed execution, and selected high-impact approvals |
| Developer | View and request ordinary engineering tasks |
| Auditor | Read tasks, runs, and audit records without sensitive mutations |

The operating principle is that developers may request analysis, but cannot bypass approval to let an agent change a protected branch.

## 11. Daily operations

```bash
docker compose ps
docker compose logs -f api
docker compose logs -f worker
docker compose up -d --build
docker compose down
```

When M10 is enabled:

- `/health/version` reports the service version;
- `/metrics` exposes metrics;
- `REPOMENDER_METRICS_TOKEN` protects the metrics endpoint;
- `REPOMENDER_RATE_LIMIT_PER_MINUTE` controls API rate limiting;
- `REPOMENDER_RETENTION_DAYS` controls historical data retention.

Backup:

```bash
REPOMENDER_DATABASE_URL='postgres://…' \
BACKUP_FILE='/secure/backups/repomender.dump' \
./scripts/backup.sh
```

Restore is destructive and requires explicit confirmation:

```bash
REPOMENDER_DATABASE_URL='postgres://…' \
BACKUP_FILE='/secure/backups/repomender.dump' \
CONFIRM_RESTORE=YES ./scripts/restore.sh
```

Stop the API and worker before restoring. Verify the archive and target database URL, then rerun migrations and health checks before allowing traffic back in.

## 12. Troubleshooting

### API does not start

```bash
docker compose logs api
```

Check `.env`, the PostgreSQL password, GitHub App configuration, AC configuration, and feature-flag dependencies.

### `/health/live` is healthy but `/health/ready` fails

The process responds, but PostgreSQL or an enabled external dependency is unavailable. Check:

- whether the PostgreSQL container is healthy;
- whether AC is running;
- whether the AC address is reachable from Docker;
- whether the GitHub App configuration is complete.

### GitHub does not create tasks

Check:

- the GitHub App is installed in the target repository;
- the Webhook URL is reachable from the public internet;
- the Webhook secret matches;
- the required events are subscribed;
- M2 is enabled.

### The agent does not run

Check:

- Agent Compose is running;
- `REPOMENDER_AC_BASE_URL` is correct;
- `REPOMENDER_AC_PROJECT_ID` is correct;
- `REPOMENDER_AC_AGENT_NAME` exists;
- Codex credentials are configured in AC.

## 13. Recommended learning path

1. Start the default Compose stack and create the administrator.
2. Connect a test GitHub repository.
3. Enable M2 and verify repository synchronization.
4. Start Agent Compose, then enable M3 and M4.
5. Enable M5 and create a test pull request.
6. Enable M6 and test one failed CI diagnosis.
7. Finally test M7-M10 approvals, repair, automation, and operations.

Related documents:

- [English technical architecture](../architecture/TECHNICAL-ARCHITECTURE.en.md)
- [M10 operations runbook](../operations/M10-RUNBOOK.md)
- [Project review report](../review/PROJECT-REVIEW-2026-08.md)
