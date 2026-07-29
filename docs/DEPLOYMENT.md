# Deployment guide

## 1. Prepare the VPS

Use a supported Ubuntu LTS release with 1–2 vCPU, 2 GB RAM, and 20 GB disk or more. Install Docker Engine from Docker's official Ubuntu repository and confirm:

```bash
docker version
docker compose version
```

Allow inbound `22/tcp` from trusted administration addresses, `80/tcp`, `443/tcp`, and `443/udp`. Do not expose PostgreSQL `5432` or Redis `6379`.

## 2. Configure secrets

Copy `.env.example` to `.env`. Generate every secret independently:

```bash
openssl rand -base64 32  # APP_MASTER_KEY
openssl rand -hex 32     # BOOTSTRAP_TOKEN
openssl rand -hex 24     # POSTGRES_PASSWORD
```

Permissions:

```bash
chmod 600 .env
```

`APP_MASTER_KEY` is not a password that can be reset. Store it in an offline password manager alongside database backups.

## 3. Choose the public host

For temporary IP mode:

```dotenv
APP_HOST=:80
PUBLIC_BASE_URL=http://203.0.113.10
SESSION_COOKIE_SECURE=false
```

For domain mode, create DNS records first:

```dotenv
APP_HOST=ai.example.com
PUBLIC_BASE_URL=https://ai.example.com
SESSION_COOKIE_SECURE=true
```

Ports 80 and 443 must reach Caddy directly for automatic certificate issuance. If a cloud firewall or NAT is present, update it as well as the VPS firewall.

## 4. Start and bootstrap

```bash
docker compose pull
docker compose up -d --build
docker compose ps
docker compose logs --tail=100 app caddy
```

The app runs migrations before it reports ready. Verify both health endpoints:

```bash
curl -i "$PUBLIC_BASE_URL/health/live"
curl -i "$PUBLIC_BASE_URL/health/ready"
```

Complete `/admin/` once with `BOOTSTRAP_TOKEN`. There is no default password. The bootstrap endpoint refuses a second admin after setup.

## 5. Validate a route

After adding and testing a provider:

```bash
curl -fsS "$PUBLIC_BASE_URL/v1/models" \
  -H "Authorization: Bearer $ROTAKEY_KEY"
```

Send a non-streaming request, a streaming request, and a request using each configured capability. Confirm:

- the public alias is returned instead of the upstream model ID
- the request log records every attempt and credential label
- secrets never appear in logs
- body inspection is present only on routes where capture was explicitly enabled

## 6. Resource and retention checks

Useful commands:

```bash
docker stats --no-stream
docker system df
docker compose exec postgres psql -U rotakey -d rotakey -c \
  "SELECT pg_size_pretty(pg_database_size(current_database()));"
```

Metadata and body retention can be shortened in **System**. The application runs expiry maintenance automatically. Caddy, PostgreSQL, Redis AOF, and app containers use restart policies; PostgreSQL, Redis, and Caddy data use named volumes.

## 7. Backup and recovery drill

Create a backup and copy it off the VPS:

```bash
./scripts/backup.sh
sha256sum backups/rotakey-*.sql.gz
```

Also store the exact `APP_MASTER_KEY`. Test restore on a separate VPS or an isolated Compose project before relying on the backup:

```bash
./scripts/restore.sh --confirm-replace-database /absolute/path/to/backup.sql.gz
docker compose restart app
curl -fsS "$PUBLIC_BASE_URL/health/ready"
```

Rotate the gateway key if a client key is exposed. Rotate provider credentials at the upstream provider first, replace them in Rotakey, test, then revoke the old upstream values.

## 8. Troubleshooting

`ready` returns `503`:

- inspect `docker compose ps`
- inspect app, PostgreSQL, and Redis logs
- verify `.env` secrets and database volume permissions

Caddy does not issue a certificate:

- verify DNS resolves to this VPS
- verify ports 80 and 443 are reachable
- verify `APP_HOST` is only the hostname, without `https://` or a path

A model returns `429`:

- inspect the capacity rail for the limiting dimension
- inspect each credential's shared and model-specific limits
- inspect request attempts for upstream cooldowns
- use `Retry-After`; do not blindly retry in a tight loop

A credential is quarantined:

- confirm the upstream key and auth scheme
- edit the credential with a replacement secret, which resets quarantine state
- run the provider connection test before sending production traffic

## 9. GitHub CI/CD

Every push and pull request runs Go tests with the race detector, `go vet`, the admin UI typecheck/build/audit, Compose validation, and a production image build. A successful push to `main` can then deploy only the Rotakey app service through a restricted SSH key.

Install `deploy/rotakey-ci-deploy.sh` as root-owned `/usr/local/sbin/rotakey-ci-deploy`, then add the deploy public key to root's `authorized_keys` with a forced command:

```text
command="/usr/local/sbin/rotakey-ci-deploy",restrict ssh-ed25519 AAAA... rotakey-github-actions
```

Configure these GitHub Actions repository secrets:

- `ROTAKEY_DEPLOY_HOST`
- `ROTAKEY_DEPLOY_PORT`
- `ROTAKEY_DEPLOY_USER`
- `ROTAKEY_DEPLOY_SSH_KEY`
- `ROTAKEY_DEPLOY_KNOWN_HOSTS`

The forced command accepts only `deploy <tested-commit-sha>`. It refuses dirty tracked files or a stale workflow, takes a PostgreSQL backup, builds and recreates only `app`, verifies readiness, and restores the previous app image if readiness fails. PostgreSQL, Redis, Caddy, and unrelated VPS services are not restarted.
