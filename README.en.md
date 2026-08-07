# RepoMender

RepoMender is a self-hosted engineering automation platform for governed code review, CI diagnosis, issue repair, approval governance, and automation administration. It keeps repository policy, workflow state, approvals, audit history, and evidence in RepoMender while delegating isolated agent execution to [Agent Compose](https://github.com/EasonW3300/agent-compose).

[Main README](README.md) · [Technical architecture](docs/architecture/TECHNICAL-ARCHITECTURE.en.md) · [中文架构](docs/architecture/TECHNICAL-ARCHITECTURE.md) · [User guide](docs/user-guide/USER-GUIDE.en.md) · [中文使用说明](docs/user-guide/USER-GUIDE.md)

## Product boundaries

- Active first-release SCM scope: GitHub.com through a GitHub App.
- Self-hosted, single-tenant deployment.
- Agent Compose/Codex credentials remain in the independent execution control plane.
- GitLab, automatic merge, direct protected-branch writes, GHES, billing, and multi-tenancy are outside the current first-release scope.
- Business modules remain behind feature flags until their sequential acceptance gates pass.

## Delivery status

| Module | Status |
| --- | --- |
| M0-M4 | Gates passed |
| M5 · Code review | MVP and private GitHub/AC smoke acceptance passed |
| M6 · CI diagnosis | Gate passed, including native failure regression and cleanup |
| M7 · Approval governance | Gate passed, including security and PostgreSQL coverage |
| M8 · Issue repair | Gate passed, including two-stage approval and patch safety |
| M9 · Automation administration | Gate passed, including templates, triggers, budget and audit controls |
| M10 · Enterprise delivery hardening | Local gate passed; installation/upgrade, backup/restore, observability, rate limiting, retention, security and load evidence recorded |

## Architecture

```text
Browser
  └─ Caddy Gateway (same-origin HTTPS)
       ├─ Vinext Web
       └─ Go API / Worker
            ├─ PostgreSQL
            ├─ GitHub.com
            └─ Agent Compose
                 └─ Codex Agent sandbox
```

The Go API owns authentication, webhooks, synchronous mutations, health checks, and domain boundaries. The worker claims durable tasks with PostgreSQL leases, runs governed AC executions, persists ordered events/findings/evidence, and crosses approval boundaries before protected actions. PostgreSQL also provides the transactional Outbox, so external notifications are not silently lost after a successful write.

See the [English technical architecture](docs/architecture/TECHNICAL-ARCHITECTURE.en.md) for component boundaries, data flows, trust boundaries, migrations, security controls, and CI policy.

## Quick start

Requirements: Docker with Compose, Node.js 24, and Go 1.26 for direct development.

```bash
cp .env.example .env
docker compose up --build
```

Open <http://localhost:8088>. Health endpoints:

```text
GET /health/live
GET /health/ready
```

On a blank database, open `/login` and create the one-time administrator. Configure OIDC with `REPOMENDER_PUBLIC_URL`, `REPOMENDER_OIDC_ISSUER`, `REPOMENDER_OIDC_CLIENT_ID`, `REPOMENDER_OIDC_CLIENT_SECRET`, and `REPOMENDER_COOKIE_SECURE` when enterprise SSO is required.

## GitHub SCM

Enable the GitHub integration and provide a 32-byte base64 master key:

```text
REPOMENDER_FEATURE_M2_SCM=true
REPOMENDER_MASTER_KEY=<32 random bytes encoded with standard base64>
```

Configure the GitHub App with:

```text
Homepage URL: https://repomender.example.com
Setup URL:    https://repomender.example.com/api/v1/scm/github/callback
Webhook URL:  https://repomender.example.com/webhooks/github
```

Then provide `REPOMENDER_GITHUB_APP_ID`, `REPOMENDER_GITHUB_APP_SLUG`, one of `REPOMENDER_GITHUB_APP_PRIVATE_KEY_B64` or `REPOMENDER_GITHUB_APP_PRIVATE_KEY_FILE`, and `REPOMENDER_GITHUB_WEBHOOK_SECRET`. Webhooks are verified before normalization and deduplicated by provider delivery ID.

## Agent Compose execution

Run AC independently and configure the RepoMender adapter:

```bash
agent-compose up \
  --host http://127.0.0.1:7410 \
  --file deploy/agent-compose.repomender.yml
```

```text
REPOMENDER_FEATURE_M3_AC_EXECUTION=true
REPOMENDER_AC_BASE_URL=http://host.docker.internal:7410
REPOMENDER_AC_AUTH_TOKEN=
REPOMENDER_AC_REQUIRED_VERSION=
REPOMENDER_AC_REQUIRED_DRIVER=docker
REPOMENDER_AC_REQUEST_TIMEOUT=10
```

AC health participates in `/health/ready` when enabled, while `/health/live` remains independent. AC tokens and model credentials are never exposed through RepoMender REST responses or event streams.

## Development and verification

```bash
npm install
npm run verify
make backend-test
make backend-coverage
make ops-test
```

PostgreSQL integration tests require an isolated database:

```bash
cd server
TEST_DATABASE_URL='postgres://...' \
  go test -count=1 -p=1 -tags=integration \
  ./internal/database ./internal/auth ./internal/scm ./internal/tasks \
  ./internal/approvals ./internal/repair ./internal/automations ./internal/retention
```

GitHub Actions runs frontend checks, Go race/vet/coverage, serialized PostgreSQL integration, backup/restore safety checks, Compose overlay validation, server/web container builds, npm audit, and pull-request dependency review. Coverage currently has a repository-wide 35% minimum. The audit is advisory while the existing dependency chain is upgraded in a compatibility-scoped change.

## Repository layout

| Path | Purpose |
| --- | --- |
| `app/` | Vinext web application and UI components |
| `server/` | Go API, worker, domain services, adapters, and migrations |
| `docs/acceptance/` | Module gate evidence |
| `docs/architecture/` | Architecture decisions and technical design |
| `docs/operations/` | Runbooks and operational procedures |
| `deploy/` | OIDC, SCM, and deployment fixtures |
| `.github/workflows/` | CI gates |

## Operations and handoff

Use the [M10 operations runbook](docs/operations/M10-RUNBOOK.md) for installation, upgrade, backup, restore, observability, and retention operations. For a non-secret environment handoff, see [Home development handoff](docs/handoff/HOME-CONTINUATION.md).

## License

RepoMender is licensed under the GNU Affero General Public License v3.0 or later. See [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
