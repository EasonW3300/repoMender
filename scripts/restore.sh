#!/bin/sh
set -eu

# Restore is deliberately opt-in because pg_restore --clean removes objects in
# the target database. Operators must name the archive and acknowledge impact.
: "${REPOMENDER_DATABASE_URL:?REPOMENDER_DATABASE_URL is required}"
: "${BACKUP_FILE:?BACKUP_FILE is required}"
if [ "${CONFIRM_RESTORE:-}" != "YES" ]; then
  echo "set CONFIRM_RESTORE=YES to restore the named backup" >&2
  exit 2
fi
case "$BACKUP_FILE" in
  /*) ;;
  *) echo "BACKUP_FILE must be an absolute path" >&2; exit 2 ;;
esac
test -s "$BACKUP_FILE"
pg_restore --clean --if-exists --no-owner --no-privileges --exit-on-error \
  --dbname="$REPOMENDER_DATABASE_URL" "$BACKUP_FILE"
echo "backup restored from $BACKUP_FILE"
