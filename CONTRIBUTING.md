# Contributing to Rotakey

Thanks for helping improve Rotakey.

## Before opening a change

- Search existing issues and pull requests.
- Open an issue first for substantial behavior, API, schema, or UI changes.
- Never include provider keys, gateway keys, `.env` files, database dumps, or captured request bodies.
- Keep the v1 single-owner scope in mind.

## Development

Backend requirements are Go 1.25, PostgreSQL, and Redis. The admin UI uses Node.js 24.

```bash
go test ./...
go vet ./...
cd web
npm ci
npm run typecheck
npm run build
```

Redis-backed tests can be enabled with:

```bash
TEST_REDIS_URL=redis://127.0.0.1:6379/0 go test -count=1 -race ./...
```

Before submitting a pull request, run the tests above and `docker compose config --quiet`. Describe the operational impact and include screenshots for visible UI changes.

## Pull requests

- Keep changes focused.
- Add or update tests for behavioral changes.
- Update deployment documentation when environment variables, storage, networking, or migrations change.
- Preserve OpenAI-compatible error shapes and streaming behavior.
- Treat rate-limit correctness, secret handling, and SSRF defenses as security-sensitive.

By contributing, you agree that your contributions are licensed under the MIT License.
