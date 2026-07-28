# Security policy

## Supported version

Security fixes are provided for the latest release on the default branch.

## Reporting a vulnerability

Do not open a public issue for suspected vulnerabilities or accidentally exposed secrets. Use GitHub's private vulnerability reporting for this repository:

https://github.com/jisunahamed/rotakey/security/advisories/new

Include the affected version or commit, reproduction steps, impact, and any suggested mitigation. Please allow a reasonable time for investigation before public disclosure.

## Operator responsibilities

- Run Rotakey behind HTTPS for ongoing use.
- Keep `.env`, `APP_MASTER_KEY`, database backups, gateway keys, and provider credentials private.
- Restrict VPS administration and keep Docker, PostgreSQL, Redis, and Caddy updated.
- Do not enable captured request bodies unless the data-retention risk is understood.
- Rotate any secret immediately if it may have been exposed.
