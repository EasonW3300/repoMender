# RepoMender

English README: [README.en.md](README.en.md). Technical architecture:
[中文](docs/architecture/TECHNICAL-ARCHITECTURE.md) ·
[English](docs/architecture/TECHNICAL-ARCHITECTURE.en.md).

RepoMender is an enterprise, self-hosted engineering automation platform for
governed code review, CI diagnosis, and issue repair. It uses
[Agent Compose](https://github.com/EasonW3300/agent-compose) as an independent
execution control plane and keeps repository policy, approvals, audit history,
and business workflow state in RepoMender.

## Product scope

RepoMender is designed for engineering teams that need AI-assisted repository
automation without giving an Agent unrestricted access to source control. Its
target workflow is:

```text
SCM event
  → verified and deduplicated webhook
  → persistent task and run
  → Agent Compose governed execution
  → structured findings and evidence
  → human approval where required
  → GitHub status, comment, or draft patch
```

The active first-release scope supports GitHub.com in a self-hosted,
single-tenant deployment. Codex is the mandatory Agent provider for acceptance.
GitLab code already implemented in M2 is preserved behind
`REPOMENDER_FEATURE_GITLAB=false` for later development, but it is not exposed
or required by the current M2-M9 gates. Automatic merge, direct writes to
protected branches, multi-tenancy, billing, and GHES are outside the
first-release scope.

## Current delivery status

The repository follows sequential MVP gates. Modules M0 and M1 provide the
engineering foundation and identity boundary. M2 is implemented behind its
feature flag and its required real GitHub.com smoke test has passed. GitLab is
deferred without deleting its adapter or regression coverage. Later modules
remain disabled until their own acceptance gates pass.

| Module | Status |
| --- | --- |
| M0 · Engineering foundation | Gate passed |
| M1 · Identity and access | Gate passed |
| M2 · SCM and repositories | GitHub-only scope implemented and real smoke passed; remote CI/merge pending |
| M3 · Agent Compose execution adapter | Gate passed (`m3-ac-execution-adapter-mvp`) |
| M4 · Task and audit core | Gate passed (`m4-task-audit-core-mvp`) |
| M5 · Code review | MVP implementation and real private GitHub/AC smoke passed; merged with the M6 delivery PR |
| M6 · CI diagnosis | Gate passed (`m6-ci-diagnosis-mvp`); local/real acceptance, native failure run, PR, merge, and cleanup passed |
| M7 · Approval and governance | Gate passed (`m7-approval-governance-mvp`); local PostgreSQL/Compose acceptance, security tests, repository CI, PR, and merge passed |
| M8 · Issue repair | Gate passed (`m8-issue-repair-mvp`); two-stage approval workflow, patch safety validation, PostgreSQL/Compose acceptance, repository CI, PR, and merge passed |
| M9 · Automation and administration | Gate passed (`m9-automation-management-mvp`); versioned templates, admin API/UI, webhook trigger governance, PostgreSQL concurrency/rollback coverage, and Compose verification complete |
| M10 · Enterprise delivery hardening | Local gate passed on `feature/m10-enterprise-hardening`; installation/upgrade, backup/restore, observability, rate limiting, retention, security baseline, and load-test evidence recorded |

The business modules must be delivered in the order M5 → M6 → M7 → M8. Each
module remains hidden behind a feature flag until its automated checks,
clean-stack E2E test, security checks, and real-provider smoke test pass.

## Architecture

```text
Browser
  └─ Gateway (same-origin HTTPS)
       ├─ Vinext web
       └─ Go API / worker
            ├─ PostgreSQL
            ├─ GitHub.com
            └─ Agent Compose daemon (M3)
                 └─ Codex Agent sandbox
```

The same Go binary exposes separate commands for the API, worker, migrations,
and database health checks. PostgreSQL migrations are embedded in that binary
and guarded by an advisory lock so API and worker startup are safe to run
concurrently. PostgreSQL also provides the transactional Outbox and MVP job
queue, avoiding a Redis or message-broker dependency.

SCM and Agent Compose integrations are defined behind provider-neutral adapter
boundaries. Business logic must not depend directly on GitHub or Agent provider
SDKs. The preserved GitLab adapter follows the same boundary while deferred.

## Start the stack

Requirements:

- Docker with Compose
- Node.js 24 and Go 1.26 for direct development

```bash
cp .env.example .env
docker compose up --build
```

Then open `http://localhost:8088`. Health endpoints are available at:

```text
GET /health/live
GET /health/ready
```

On a blank database, open `http://localhost:8088/login`, choose the one-time
administrator setup, then sign in. Configure enterprise OIDC in `.env` with:

```text
REPOMENDER_PUBLIC_URL
REPOMENDER_OIDC_ISSUER
REPOMENDER_OIDC_CLIENT_ID
REPOMENDER_OIDC_CLIENT_SECRET
REPOMENDER_COOKIE_SECURE
```

The OIDC implementation uses Authorization Code with PKCE. The local
administrator remains available as an emergency fallback when discovery or
token exchange fails.

## Configure GitHub SCM

M2's active scope supports GitHub.com through a GitHub App. Enable its server
routes and provide the encryption key:

```text
REPOMENDER_FEATURE_M2_SCM=true
REPOMENDER_MASTER_KEY=<32 random bytes encoded with standard base64>
```

Create a GitHub App with these URLs, replacing `https://repomender.example.com`
with the externally reachable RepoMender origin:

```text
Homepage URL: https://repomender.example.com
Setup URL:    https://repomender.example.com/api/v1/scm/github/callback
Webhook URL:  https://repomender.example.com/webhooks/github
```

Enable webhook SSL verification and configure:

```text
REPOMENDER_GITHUB_APP_ID
REPOMENDER_GITHUB_APP_SLUG
REPOMENDER_GITHUB_APP_PRIVATE_KEY_B64
REPOMENDER_GITHUB_APP_PRIVATE_KEY_FILE
REPOMENDER_GITHUB_WEBHOOK_SECRET
```

Supply exactly one private-key source: base64 content or a mounted PEM file.
Recommended repository permissions are read/write access to contents, checks,
commit statuses, issues, and pull requests, plus read access to actions and
metadata. Subscribe to push, pull request, issues, workflow run, check run, and
check suite events.

The real GitHub M2 smoke test uses the private
[repomender-sandbox](https://github.com/EasonW3300/repomender-sandbox)
repository. The GitHub App is restricted to that repository.

GitLab support is intentionally dormant. Its adapter, database compatibility,
OAuth implementation, webhook verification, and protocol fixtures remain in
the repository. `REPOMENDER_FEATURE_GITLAB` defaults to `false`; do not enable
it in the current release. A later scoped module can restore real-provider
acceptance without rebuilding the M2 foundation.

Once signed in as an administrator, open `/repositories` to connect GitHub.
Administrators and maintainers can resynchronize snapshots. All authenticated
roles can inspect connected repositories. Webhooks are accepted at:

```text
POST /webhooks/github
```

Provider credentials are never exposed by the REST API. Persisted credentials
use AES-256-GCM. Webhooks are verified before normalization, deduplicated by
provider delivery ID, and published through the transactional Outbox.

## Configure Agent Compose execution

M3 remains disabled until its acceptance gate passes. Apply the non-sensitive
Codex project template to an independently running AC daemon:

```bash
agent-compose up \
  --host http://127.0.0.1:7410 \
  --file deploy/agent-compose.repomender.yml
```

Codex authentication and model credentials must be configured only in Agent
Compose. Then configure RepoMender:

```text
REPOMENDER_FEATURE_M3_AC_EXECUTION=true
REPOMENDER_AC_BASE_URL=http://host.docker.internal:7410
REPOMENDER_AC_AUTH_TOKEN=
REPOMENDER_AC_REQUIRED_VERSION=
REPOMENDER_AC_REQUIRED_DRIVER=docker
REPOMENDER_AC_REQUEST_TIMEOUT=10
REPOMENDER_AC_SENSITIVE_PATTERNS=
```

When enabled, AC health and compatibility participate in `/health/ready` while
`/health/live` remains independent. Administrators and maintainers can open
the deliberately unlinked internal route `/runs/diagnostic` to start one
immutable-commit smoke run, inspect ordered SSE output, or request
cancellation. The route does not create M4 task or audit records.

Stop the stack without deleting its database:

```bash
docker compose down
```

## Development and verification

```bash
npm install
make m2-verify
```

Run the PostgreSQL integration test against an isolated database:

```bash
cd server
TEST_DATABASE_URL='postgres://...' go test -p=1 -tags=integration ./internal/database ./internal/auth ./internal/scm
```

The CI workflow repeats frontend validation, Go race and static checks,
PostgreSQL migration integration tests, and Compose configuration validation.
Acceptance evidence is recorded in:

- [M00 engineering foundation](docs/acceptance/M00.md)
- [M01 identity and access](docs/acceptance/M01.md)
- [M02 SCM and repositories](docs/acceptance/M02.md)

M2's clean-stack fixture smoke environment is defined in
`compose.scm-smoke.yaml`. It validates GitHub and keeps the deferred GitLab
protocol under regression coverage without placing real provider credentials
or private keys in the repository.

## Configuration

All runtime values use the `REPOMENDER_` prefix. `.env.example` documents local
defaults and optional registry mirrors. Secrets must remain in ignored `.env`
files or an external secret manager. Temporary HTTPS tunnels are suitable only
for provider smoke tests; production callbacks and webhooks require a stable
HTTPS origin.

## Repository layout

| Path | Purpose |
| --- | --- |
| `app/` | Vinext web application and UI components |
| `server/` | Go API, worker, domain services, adapters, and migrations |
| `docs/acceptance/` | Module gate evidence and remaining blockers |
| `docs/architecture/` | Architecture decisions and module design |
| `deploy/` | Local OIDC and SCM smoke-test fixtures |
| `.github/workflows/` | Repository CI gates |

## Continue on another computer

For a complete non-secret environment handoff, follow
[Home development handoff](docs/handoff/HOME-CONTINUATION.md). It lists the
authoritative branch and gate state, files intentionally excluded from Git,
steps to reconnect the GitHub App, the remaining M2 blocker, and the strict
M3-M9 delivery workflow.

The ready-to-paste [Codex continuation prompt](docs/handoff/CODEX-START-PROMPT.md)
ensures a new Codex session reads repository evidence before continuing.

## License

RepoMender is licensed under the GNU Affero General Public License v3.0 or
later. See [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
