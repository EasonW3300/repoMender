#!/bin/sh
set -eu

# Exercise destructive-operation guard rails without connecting to PostgreSQL.
# Every command below must fail before pg_dump or pg_restore can run.
root_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
temporary_dir=$(mktemp -d)
trap 'rm -rf "$temporary_dir"' EXIT HUP INT TERM

assert_failure() {
  if "$@"; then
    echo "expected command to fail: $*" >&2
    exit 1
  fi
}

assert_failure env REPOMENDER_DATABASE_URL=unused BACKUP_FILE=relative.dump \
  "$root_dir/scripts/backup.sh"
assert_failure env REPOMENDER_DATABASE_URL=unused BACKUP_FILE="$temporary_dir/missing.dump" \
  "$root_dir/scripts/restore.sh"
assert_failure env REPOMENDER_DATABASE_URL=unused BACKUP_FILE="$temporary_dir/missing.dump" \
  CONFIRM_RESTORE=YES "$root_dir/scripts/restore.sh"

existing="$temporary_dir/existing.dump"
printf '%s' 'known-good-placeholder' > "$existing"
assert_failure env REPOMENDER_DATABASE_URL=unused BACKUP_FILE="$existing" \
  "$root_dir/scripts/backup.sh"

echo "backup and restore guard rails passed"
