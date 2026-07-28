#!/usr/bin/env sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
backup_dir="$repo_dir/backups"
mkdir -p "$backup_dir"
stamp=$(date -u +%Y%m%dT%H%M%SZ)
target="$backup_dir/rotakey-$stamp.sql.gz"

cd "$repo_dir"
docker compose exec -T postgres sh -c \
  'pg_dump --clean --if-exists --no-owner --no-privileges -U "$POSTGRES_USER" "$POSTGRES_DB"' \
  | gzip > "$target"
chmod 600 "$target"
printf 'Backup written to %s\n' "$target"
