#!/bin/sh
set -eu

# pg_dump creates a portable custom-format archive; a private temporary file
# prevents a failed dump from replacing the last known-good backup.
umask 077
: "${REPOMENDER_DATABASE_URL:?REPOMENDER_DATABASE_URL is required}"
: "${BACKUP_FILE:?BACKUP_FILE is required}"
case "$BACKUP_FILE" in
  /*) ;;
  *) echo "BACKUP_FILE must be an absolute path" >&2; exit 2 ;;
esac
if [ -e "$BACKUP_FILE" ] && [ "${FORCE:-0}" != "1" ]; then
  echo "backup exists; set FORCE=1 to replace it" >&2
  exit 3
fi
mkdir -p "$(dirname "$BACKUP_FILE")"
temporary="$BACKUP_FILE.tmp.$$"
trap 'rm -f "$temporary"' EXIT HUP INT TERM
pg_dump --format=custom --no-owner --no-privileges --file="$temporary" "$REPOMENDER_DATABASE_URL"
test -s "$temporary"
mv -f "$temporary" "$BACKUP_FILE"
trap - EXIT HUP INT TERM
echo "backup written to $BACKUP_FILE"
