# RepoMender

RepoMender is an enterprise, self-hosted engineering automation platform for
governed code review, CI diagnosis, and issue repair. It uses
[Agent Compose](https://github.com/EasonW3300/agent-compose) as an independent
execution control plane and keeps repository policy, approvals, audit history,
and business workflow state in RepoMender.

## Current delivery status

The repository follows sequential MVP gates. Modules M0 and M1 provide the
engineering foundation and identity boundary. M2 is implemented behind its
feature flag and is awaiting its mandatory GitHub.com and GitLab.com sandbox
smoke test. Later modules remain disabled until their own acceptance gates pass.

| Module | Status |
| --- | --- |
| M0 · Engineering foundation | Implemented |
| M1 · Identity and access | Implemented |
| M2 · SCM and repositories | Implemented locally; real-provider gate pending |
| M3–M10 | Not started |

## Architecture

```text
Browser
  └─ Gateway :8088
       ├─ Vinext web
       └─ Go API / worker
            └─ PostgreSQL

Agent Compose is connected as a separate control plane in M3.
```

The same Go binary exposes separate commands for the API, worker, migrations,
and database health checks. PostgreSQL migrations are embedded in that binary
and guarded by an advisory lock so API and worker startup are safe to run
concurrently.

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

M2 supports GitHub.com through a GitHub App and GitLab.com through an OAuth
Application. Enable its server routes with `REPOMENDER_FEATURE_M2_SCM=true`,
provide a 32-byte base64 `REPOMENDER_MASTER_KEY`, and configure either or both
provider credential groups documented in `.env.example`. The GitHub private key
can be injected as base64 or mounted as a file. Provider credentials are never
exposed by the REST API; persisted GitLab tokens are encrypted with AES-256-GCM.

Once signed in as an administrator, open `/repositories` to connect providers.
Administrators and maintainers can resynchronize snapshots. All authenticated
roles can inspect connected repositories. Webhooks are accepted at:

```text
POST /webhooks/github
POST /webhooks/gitlab
```

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
Acceptance evidence is recorded under `docs/acceptance/`.

## Configuration

All runtime values use the `REPOMENDER_` prefix. `.env.example` documents local
defaults and optional registry mirrors. Secrets must remain in ignored `.env`
files or an external secret manager.

## License

RepoMender is licensed under the GNU Affero General Public License v3.0 or
later. See [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
