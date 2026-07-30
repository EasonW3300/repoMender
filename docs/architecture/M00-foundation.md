# M00 engineering foundation

## Process boundaries

- `web` renders the existing RepoMender product interface.
- `gateway` exposes one origin and routes API and health paths to the Go API.
- `api` owns synchronous HTTP delivery and applies pending migrations at startup.
- `worker` is a separately scalable process and will claim transactional outbox
  records in later modules.
- `postgres` is the sole durable store and queue foundation.

## Startup behavior

API and worker both apply embedded migrations before serving work. A PostgreSQL
transaction advisory lock serializes concurrent migration attempts. Applied
versions are stored in `schema_migrations`, and every up migration has a paired
down migration.

The liveness endpoint only proves that the API process can answer requests.
Readiness also pings PostgreSQL and returns HTTP 503 when persistence is
unavailable.

## Deferred boundaries

M0 intentionally has no user, SCM, AC, task, or approval business entities.
Those schemas and handlers are introduced only by their sequential MVP modules.
