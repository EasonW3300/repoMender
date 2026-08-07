# Project Review and Test Strategy — 2026-08

## Scope

This review covers the current M10 `main` baseline across the Go API, PostgreSQL migrations and stores, worker queue, frontend build/render path, Compose overlays, container definitions, and backup/restore scripts.

## Findings and disposition

### Fixed in this change

- `decodeJSON` accepted a valid JSON document followed by another JSON document. The decoder now requires EOF after the first document, and tests cover both rejection of a trailing document and acceptance of trailing whitespace.
- The worker queue unit test used fixed sleeps to observe completion. It now synchronizes through completion and shutdown channels, making the regression deterministic.
- Migration tests checked only equal counts. They now also verify every `*.up.sql` has a matching `*.down.sql` and that migration filenames are strictly ordered.
- Backup/restore guard rails previously had syntax coverage only. A shell regression test now verifies relative paths, missing restore confirmation, missing archives, and refusal to overwrite an existing backup before database tools are invoked.

### Remaining risks tracked for follow-up

- Go package coverage is uneven. The repository-wide baseline is 37.5%; task, automation, retention, database, and HTTP packages still rely heavily on integration or endpoint tests. CI now enforces a 35% repository-wide floor and uploads the profile so package-level trends remain visible.
- Frontend tests validate server-rendered routes and source-level wiring, but do not yet drive browser interactions, API failure states, or accessibility snapshots. The UI also retains a visual `ReviewDetail` fallback and static list-page data for design review. Converting those paths to browser-level API contract tests is the next frontend test milestone.
- PostgreSQL integration tests share one CI database and are intentionally serialized. They cover all current integration packages, but a future parallel matrix should use isolated databases or schemas before enabling concurrency.
- The M10 telemetry layer currently validates W3C trace propagation and request correlation at the HTTP boundary; it is not an OpenTelemetry exporter. That is an observability roadmap item, not a CI failure.
- `npm audit` currently reports 18 advisories, including 13 high-severity findings in the locked Next/Vite/React Server/sharp toolchain. The new CI audit is intentionally advisory until those packages can be upgraded in a compatibility-scoped change; `npm audit fix --force` is not part of this review because it requests breaking dependency changes.

## Verification layers

| Layer | Coverage |
| --- | --- |
| Frontend unit/build regression | ESLint, TypeScript, Vinext production build, SSR route rendering and source wiring assertions |
| Go unit/race regression | `go test -race -count=1 ./...`, state machines, security middleware, adapters, M10 controls, deterministic worker queue test |
| Go static/coverage gate | `go vet ./...`, atomic repository coverage profile, 35% minimum |
| PostgreSQL integration | Repeatable migrations, auth, SCM, tasks/leases, approvals, repairs, automations, retention |
| Operations regression | Shell syntax checks and destructive-operation guard-rail checks for backup/restore |
| Deployment regression | Base, SCM, and M6–M10 Compose configuration plus server/web container builds in GitHub Actions |

## CI policy

`.github/workflows/ci.yml` keeps pull requests and `main` pushes covered by the same required categories: frontend quality, dependency audit, Go race/static/coverage checks, serialized PostgreSQL integration, operations safety, Compose validation, container builds, and pull-request dependency review. Concurrency cancellation and job timeouts prevent stale or hung runs from consuming the runner indefinitely.
