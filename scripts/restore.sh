#!/usr/bin/env sh
set -eu

if [ "$#" -ne 2 ] || [ "$1" != "--confirm-replace-database" ]; then
  printf 'Usage: %s --confirm-replace-database /absolute/path/to/backup.sql.gz\n' "$0" >&2
  exit 2
fi

archive=$2
case "$archive" in
  /*) ;;
  *) printf 'The backup path must be absolute.\n' >&2; exit 2 ;;
esac
if [ ! -f "$archive" ]; then
  printf 'Backup not found: %s\n' "$archive" >&2
  exit 2
fi

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"
gzip -dc -- "$archive" | docker compose exec -T postgres sh -c \
  'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" "$POSTGRES_DB"'
printf 'Database restored from %s\n' "$archive"
