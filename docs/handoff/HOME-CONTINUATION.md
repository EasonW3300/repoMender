# Home development handoff

Last updated: 2026-07-30

This document is the source of truth for continuing RepoMender on another
computer. It intentionally contains no passwords, tokens, webhook secrets,
OAuth secrets, private keys, session cookies, or model credentials.

## Repository state

- Canonical repository: <https://github.com/EasonW3300/repoMender>
- Active branch: `feature/m2-scm-repositories`
- Base branch: `main`
- Latest completed gates on `main`: M0 and M1
- Current work: M2 implementation is pushed to the active branch.
- M2 real GitHub.com smoke: passed.
- M2 real GitLab.com smoke: pending.
- M3 must not start until M2 is fully accepted, merged, and tagged
  `m2-scm-repositories-mvp`.

Start by reading, in order:

1. [README](../../README.md)
2. [M02 acceptance record](../acceptance/M02.md)
3. [M03-M09 delivery plan](../roadmap/M03-M09.md)
4. The acceptance record for the module currently being implemented.

Do not rely on chat history as project state. Git, acceptance records, and
passing verification commands are authoritative.

## Clone and bootstrap

```bash
git clone git@github.com:EasonW3300/repoMender.git
cd repoMender
git switch feature/m2-scm-repositories
git pull --ff-only

cp .env.example .env
npm ci
cd server && go mod download && cd ..
docker compose up --build
```

Requirements:

- Git
- Docker Desktop with Compose
- Node.js 24
- Go 1.26
- GitHub CLI for repository and Actions operations
- `cloudflared` only when a temporary real-provider HTTPS callback is needed

Verify the transferred workspace before changing code:

```bash
git status --short
git log -1 --oneline
npm run lint
npm run typecheck
npm test
make backend-test
docker compose config --quiet
docker compose -f compose.yaml -f compose.scm-smoke.yaml config --quiet
```

The worktree must be clean before starting the next module. Never commit
generated directories such as `node_modules`, `.vinext`, `dist`, `.wrangler`,
coverage output, or local database data.

## State that is not transferred through Git

The following office-computer state is deliberately excluded:

| Local state | Home-computer action |
| --- | --- |
| `.env` | Recreate from `.env.example`; use new development secrets. |
| `.env.github-compose.yaml` | Recreate from `deploy/github-app/compose.override.example.yaml`. |
| GitHub App PEM private key | Generate a new key or transfer it through an approved secret manager; never commit it. |
| GitHub webhook secret | Set a new random value in GitHub App settings and the home `.env`. |
| GitLab OAuth client secret | Create/store it locally or in an approved secret manager. |
| OIDC client secret | Recreate locally; the committed Dex smoke credentials are test-only. |
| Local administrator password | Bootstrap a new administrator on the new database. |
| PostgreSQL Docker volume | Start with a blank database and rerun migrations. |
| Browser sessions and `gh` login | Authenticate again on the home computer. |
| Cloudflare Quick Tunnel | Start a new tunnel; Quick Tunnel URLs are temporary. |
| Agent Compose model credentials | Configure only in the AC control plane, never in RepoMender. |

Generate local secrets without printing or committing them. Store persistent
credentials in the operating-system keychain or an approved secret manager.

## Reconnect the existing GitHub App

Existing non-secret resources:

- GitHub App name: `RepoMender EasonW3300 Sandbox`
- GitHub App ID: `4434226`
- GitHub App slug: `repomender-easonw3300-sandbox`
- App settings:
  <https://github.com/settings/apps/repomender-easonw3300-sandbox>
- Private sandbox:
  <https://github.com/EasonW3300/repomender-sandbox>
- Installation scope: only `EasonW3300/repomender-sandbox`

On the home computer:

1. Start RepoMender with a blank database.
2. Expose port 8088 using a new HTTPS endpoint.
3. Set `REPOMENDER_PUBLIC_URL` to that origin and enable secure cookies.
4. Update the GitHub App Homepage, Setup, and Webhook URLs to the new origin.
5. Set the same newly generated webhook secret in GitHub and `.env`.
6. Generate or securely obtain a GitHub App PEM key.
7. Mount it using the committed Compose override example.
8. Recreate the API container and verify readiness.
9. Bootstrap/login as the local administrator and reconnect GitHub from
   `/repositories`.
10. Trigger the sandbox workflow and confirm a genuine delivery is persisted.

Changing the GitHub App webhook secret immediately invalidates the old office
configuration. That is expected if the office computer is no longer used.

## Finish M2 before M3

The real GitLab.com acceptance remains the only provider gate blocker:

1. Create a dedicated private GitLab.com sandbox project.
2. Create a GitLab OAuth Application whose callback is
   `/api/v1/scm/gitlab/callback` on the current HTTPS origin.
3. Configure a project webhook to `/webhooks/gitlab`.
4. Connect the provider through RepoMender and synchronize the project.
5. Produce one genuine GitLab delivery.
6. Verify a forged token and a repeated delivery are rejected or deduplicated.
7. Record URLs, delivery identifiers, commands, and results in
   `docs/acceptance/M02.md` without recording secrets.
8. Run `make m2-verify` and the clean-stack smoke test.
9. Push the branch, open/update the PR, wait for all GitHub Actions jobs, merge
   to `main`, and create tag `m2-scm-repositories-mvp`.

Only after all nine steps pass may M3 begin.

## Development discipline for M3-M9

- One module per branch, PR, acceptance record, and version tag.
- Default branch names:
  `feature/m3-ac-execution-adapter` through
  `feature/m9-automation-management`.
- Write failing tests before implementation changes.
- Keep provider-specific code behind adapters.
- Add migrations with rollback and concurrency coverage.
- Preserve RBAC, CSRF, webhook verification, encryption, redaction, and audit
  boundaries introduced by M1 and M2.
- Keep new navigation and APIs behind the module feature flag until its gate
  passes.
- Do not begin the next module with any P0/P1 defect, failed CI job, missing
  real smoke, or incomplete acceptance record.
- Never let an Agent merge automatically or write directly to a protected or
  default branch.
- Never store AC model credentials in RepoMender or pass them into repository
  workspaces.

The detailed scope and gate for every remaining module are in
[M03-M09 delivery plan](../roadmap/M03-M09.md).

## Safe Git workflow

For each module:

```bash
git switch main
git pull --ff-only
git switch -c feature/mX-module-name

# implement and verify the module

git status --short
git add <intentional-files>
git commit -m "feat: add Mx ... MVP"
git push -u origin feature/mX-module-name
```

Open a PR, wait for all checks, and update `docs/acceptance/M0X.md` with remote
evidence. Merge only after the gate passes. Tag the merge commit using the
module tag defined in the roadmap.

## Security check before every push

```bash
git diff --check
git status --short
git ls-files | rg '(^|/)\.env($|\.)|\.pem$|private[-_]?key|credentials'
```

Review any match manually. Test fixtures may contain explicitly documented
non-production values, but real secrets and private keys must never be staged.
