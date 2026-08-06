# M10 operations runbook

## Install and upgrade

1. Copy `.env.example` to a private environment file and replace the database
   password, master key, provider credentials, and AC token.
2. Start PostgreSQL first with `docker compose up -d postgres` and wait for its
   health check.
3. Start API and worker with `docker compose up -d api worker web gateway`.
   Both API and worker run the embedded migrations under a PostgreSQL advisory
   lock, so a concurrent upgrade applies each migration once.
4. Confirm `GET /health/live`, `GET /health/ready`, and `GET /health/version`.
   The version probe reports the image build version.

For an upgrade, take a backup first, deploy the new image, and allow the API or
worker to apply migrations. Roll back the image only when the operator has
verified compatibility; use `repomender migrate down` one migration at a time
for a planned database rollback.

## Backup and restore

```sh
REPOMENDER_DATABASE_URL='postgres://…' \
BACKUP_FILE='/secure/backups/repomender-2026-08-06.dump' \
./scripts/backup.sh
```

Backups use PostgreSQL custom format, `umask 077`, a temporary file, and an
atomic rename. Existing files are protected unless `FORCE=1` is supplied.

Restore is destructive to the named target database and requires the exact
acknowledgement below:

```sh
REPOMENDER_DATABASE_URL='postgres://…' \
BACKUP_FILE='/secure/backups/repomender-2026-08-06.dump' \
CONFIRM_RESTORE=YES ./scripts/restore.sh
```

Stop API and worker before restoring, verify the archive and target URL, then
run migrations and health checks before allowing traffic back in.

## Observability and safety controls

- Scrape `/metrics`; set `REPOMENDER_METRICS_TOKEN` to require a bearer token.
- Forward `traceparent` through the gateway and correlate logs with
  `X-Request-ID`. Set `OTEL_EXPORTER_OTLP_ENDPOINT` when an external collector
  boundary is available.
- Set `REPOMENDER_RATE_LIMIT_PER_MINUTE` for API/webhook traffic. Health and
  metrics probes are exempt so orchestration remains reliable.
- Set `REPOMENDER_RETENTION_DAYS` and inspect the cleanup log. Run
  `repomender retention` for an explicit one-shot cleanup.
- Use `REPOMENDER_COOKIE_SECURE=true` behind HTTPS to enable HSTS and secure
  session cookies.
