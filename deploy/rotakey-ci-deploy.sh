#!/usr/bin/env bash
set -Eeuo pipefail

readonly APP_DIR="/opt/rotakey"
readonly COMPOSE_FILE="compose.vps.yml"
readonly HEALTH_URL="http://127.0.0.1:8787/health/ready"
readonly LOCK_FILE="/var/lock/rotakey-ci-deploy.lock"
readonly BACKUP_RETENTION=7

if [[ ! ${SSH_ORIGINAL_COMMAND:-} =~ ^deploy[[:space:]]([0-9a-f]{40})$ ]]; then
  echo "Only a tested Rotakey commit can be deployed." >&2
  exit 64
fi
readonly expected_sha="${BASH_REMATCH[1]}"

exec 9>"$LOCK_FILE"
if ! flock -n 9; then
  echo "Another Rotakey deployment is already running." >&2
  exit 75
fi

cd "$APP_DIR"
if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "Tracked Rotakey files have local changes; refusing deployment." >&2
  exit 65
fi

git fetch --prune origin main
readonly remote_sha="$(git rev-parse origin/main)"
if [[ "$remote_sha" != "$expected_sha" ]]; then
  echo "main advanced after this workflow started; the newer workflow will deploy it." >&2
  exit 75
fi

mkdir -p backups

# Build cache is disposable, and stale partial backups should never be allowed
# to block a verified deployment on a small VPS disk.
docker builder prune --all --force >/dev/null
docker image prune --force >/dev/null
find backups -maxdepth 1 -type f -name 'rotakey-*.dump.tmp' -delete
mapfile -t expired_backups < <(
  find backups -maxdepth 1 -type f -name 'rotakey-*.dump' -printf '%f\n' \
    | sort -r \
    | tail -n "+${BACKUP_RETENTION}"
)
for expired_backup in "${expired_backups[@]}"; do
  rm -f -- "backups/$expired_backup"
done

umask 077
readonly backup="backups/rotakey-$(date -u +%Y%m%dT%H%M%SZ).dump"
readonly backup_tmp="${backup}.tmp"
trap 'rm -f -- "$backup_tmp"' EXIT
docker compose -f "$COMPOSE_FILE" exec -T postgres \
  sh -c 'pg_dump -Fc -U "$POSTGRES_USER" -d "$POSTGRES_DB"' </dev/null >"$backup_tmp"
test -s "$backup_tmp"
mv -- "$backup_tmp" "$backup"

readonly app_container="$(docker compose -f "$COMPOSE_FILE" ps -q app)"
if [[ -z "$app_container" ]]; then
  echo "Rotakey app container is not running; refusing an unverified deployment." >&2
  exit 69
fi
readonly previous_image="$(docker inspect --format '{{.Image}}' "$app_container")"
docker image tag "$previous_image" rotakey-app:predeploy

git merge --ff-only "$expected_sha"
docker compose -f "$COMPOSE_FILE" build \
  --build-arg ROTAKEY_COMMIT="$expected_sha" \
  --build-arg ROTAKEY_BUILD_TIME="$(date -u +%FT%TZ)" \
  app </dev/null
docker compose -f "$COMPOSE_FILE" up -d --no-deps app </dev/null

ready=false
for _ in {1..30}; do
  if curl -fsS "$HEALTH_URL" >/dev/null; then
    ready=true
    break
  fi
  sleep 2
done

if [[ "$ready" != true ]]; then
  echo "New app failed readiness; restoring the previous image." >&2
  docker image tag rotakey-app:predeploy rotakey-app:latest
  docker compose -f "$COMPOSE_FILE" up -d --no-deps --force-recreate app </dev/null
  exit 1
fi

docker image rm rotakey-app:predeploy >/dev/null 2>&1 || true
docker image prune --force >/dev/null
trap - EXIT
printf 'Deployed Rotakey %s; backup %s\n' "$expected_sha" "$backup"
