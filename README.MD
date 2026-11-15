# BCC Showcase Wall — Backend API (Go + Gin + Gorm)

Backend service for **BCC Showcase Wall**, currently implementing the complete Authentication & Session module:
- **Email + Password + 6-Digit Email OTP** (Registration, Login, Resend OTP, Verify OTP)
- **Firebase OAuth (Google Sign-In / Sign-Up)** with automatic account linking (`POST /api/v1/auth/oauth/firebase`)
- **Session Management** (`JWT` 15-minute access token + 1-day `HttpOnly` rotating refresh token cookie with reuse detection)
- **Embedded SQL Migrations** (`0001_create_users.sql`, `0002_create_sessions.sql`, `0003_oauth_users.sql`)
- **Admin Seed CLI** (`cmd/seed`): the only way to create an Admin, run with `docker exec` on the server

---

## 1. API Endpoints (Current Implementation)

All API endpoints are prefixed with `/api/v1` (except `GET /health`).

| Method | Path | Access | Request Body / Notes | Response |
| :--- | :--- | :--- | :--- | :--- |
| `GET` | `/health` | Public | — | `200 OK` `{ "data": { "status": "ok" } }` |
| `POST` | `/api/v1/auth/register` | Public (rate-limited) | `{ "name", "email", "password" }` | `202 Accepted` `{ "data": { "status": "code_sent" } }` |
| `POST` | `/api/v1/auth/login` | Public (rate-limited) | `{ "email", "password" }` | `202 Accepted` `{ "data": { "status": "code_sent" } }` |
| `POST` | `/api/v1/auth/otp/verify` | Public (rate-limited) | `{ "email", "code", "purpose": "register" \| "login" }` | `200 OK` `{ "data": { "access_token", "user" } }` + `refresh_token` cookie |
| `POST` | `/api/v1/auth/otp/resend` | Public (rate-limited) | `{ "email", "purpose": "register" \| "login" }` | `202 Accepted` `{ "data": { "status": "code_sent" } }` |
| `POST` | `/api/v1/auth/oauth/firebase` | Public (rate-limited) | `{ "id_token": "<firebase_jwt>" }` | `200 OK` `{ "data": { "access_token", "user" } }` + `refresh_token` cookie |
| `POST` | `/api/v1/auth/refresh` | Cookie (`refresh_token`) | Requires matching `Origin` header | `200 OK` `{ "data": { "access_token" } }` + rotated `refresh_token` cookie |
| `POST` | `/api/v1/auth/logout` | Bearer Auth | Requires matching `Origin` header | `204 No Content` (revokes session & clears cookie) |
| `GET` | `/api/v1/me` | Bearer Auth | `Authorization: Bearer <access_token>` | `200 OK` `{ "data": { "id", "email", "display_name", "role" } }` |

### Firebase OAuth (`POST /api/v1/auth/oauth/firebase`) Behavior
1. **Token Verification:** Verifies the client's Firebase ID Token using `firebase.google.com/go/v4`. If Firebase credentials are not configured on the server, returns `503 Service Unavailable` (`oauth_unavailable`).
2. **Email Verification Check:** Rejects unverified provider emails with `403 Forbidden` (`email_not_verified`).
3. **Account Resolution & Linking:**
   - **Returning OAuth User:** Matches `firebase_uid` first.
   - **Existing Email+Password User:** If the email already exists in `users` without a `firebase_uid`, links the `firebase_uid` to the existing account and sets `auth_provider = "both"`.
   - **Conflict Protection:** If the email is already linked to a *different* `firebase_uid`, returns `409 Conflict` (`oauth_account_conflict`).
   - **New User:** Creates a new user (`password_hash = NULL`, `auth_provider = "google"`). Always creates a `Contributor`; nobody becomes Admin by signing up (see section 4).
4. **Immediate Session:** Skips email OTP and directly issues the 15-minute `access_token` and 1-day `HttpOnly` `refresh_token` cookie.

---

## 2. Environment Variables (`.env`)

Copy `.env.example` to `.env` and configure:

- `FIREBASE_PROJECT_ID`: Your Firebase project ID.
- `FIREBASE_CREDENTIALS_JSON`: Inline JSON string of your Firebase Service Account key (or set `FIREBASE_CREDENTIALS_FILE=/app/secrets/firebase.json`).
- `APP_ENV`: `development`, `staging` or `production`. Anything else stops the app at startup.
- `LOG_LEVEL` / `LOG_FORMAT`: optional overrides, see section 5.

---

## 3. Running with Docker Compose

Build and start PostgreSQL, Redis, and the Go API container:

```bash
cp .env.example .env
docker compose up -d --build
```

See section 4 for making Admins.

---

## 4. Making Admins (server only)

There is no Admin endpoint and no `ADMIN_*` environment variable, so no request from outside can create an Admin. Admins are made by running the `seed` command **inside the API container**, which needs shell access to the Docker host:

```bash
# one or several people; separate with spaces or commas
docker exec -it showcase-api /app/seed alice@example.com bob@example.com
```

(Production: the container is `showcase-prod-api`.)

- The person must **already have an account** (registered, or signed in with Google once). Unknown emails are reported and skipped; the command never creates accounts.
- Result per email: `is now Admin`, `was already Admin`, or `no account with this email`. The exit code is non-zero if any email failed.
- The new role takes effect at the person's next sign-in or token refresh (access tokens last 15 minutes).
- Every grant writes an `audit: admin role granted` line to the container log (`docker logs showcase-api`), with the email masked.

---

## 5. Logging

Logs go to stdout only; Docker keeps them (rotated: 5 files of 10 MB per container) and `docker logs -f showcase-api` reads them.

| | development | staging | production |
| :--- | :--- | :--- | :--- |
| Format | readable text | JSON, one object per line | JSON, one object per line |
| Default level | `debug` | `info` | `info` |
| `DEV_PRINT_OTP` | allowed | refused | refused |
| `COOKIE_SECURE=false` | allowed | allowed | refused |

Every line has `time` (UTC), `level`, `msg`, `service`, `env` and `version` (the git SHA of the image). Requests add `request_id`, `method`, `route`, `status`, `latency_ms`, `bytes`, `ip`, `user_agent` and `user_id` when known. The level follows the result: 5xx `ERROR`, 4xx and requests over 2 s `WARN`, the rest `INFO`; `/health` is `DEBUG`. A request ID sent by the reverse proxy in `X-Request-ID` is kept, otherwise one is generated and returned in the response header. Panics are logged with the value and stack trace; failed and slow (>300 ms) database queries are logged with placeholders only.

Secrets cannot leak by accident: attributes named like `password`, `token`, `authorization`, `cookie`, `code`, `secret` are replaced by `[REDACTED]`, and `email` is masked (`a***@example.com`). Audit events use the `audit:` message prefix, so `docker logs showcase-api | grep '"audit:'` lists them.

---

## 6. CI/CD Deployment

### Staging (`.github/workflows/deploy-staging.yml`)

Pushing to the `staging` branch builds `bcc-showcase-app:staging` on GitHub Actions, transfers the image archive to the VM over SCP, loads it into Docker, and runs `docker compose up -d --no-build` in `/opt/bcc-showcase`.

### Production (`.github/workflows/deploy-production.yml`)

Pushing to the `production` branch (or running the workflow by hand) deploys to `/opt/bcc-showcase-prod` as its own compose project (`bcc-showcase-prod`), so production never shares a database, Redis or cover storage with staging.

- **Fresh start:** the first production deploy has no `.fresh-db-initialized` marker on the VM, so it runs `docker compose down -v` and starts with an empty database; the app then applies all migrations itself. Later deploys keep the data. To wipe production again, run the workflow manually with **wipe_data** ticked, or run `./scripts/reset-data.sh` in the stack's directory (it asks you to type the environment name).
- **Config check:** the deploy fails before touching anything if `/opt/bcc-showcase-prod/.env` lacks `JWT_SECRET` (32+ chars), `RESEND_API_KEY`, `RESEND_FROM`, `FRONTEND_ORIGIN` or a non-default `POSTGRES_PASSWORD`, or has `DEV_PRINT_OTP=true` / `COOKIE_SECURE=false`.
- Put the production secrets in a GitHub Environment named `production` (same secret names as below), and use another VM or set `API_PORT`, `GRAFANA_PORT`, `PROMETHEUS_PORT` in the production `.env` if it shares a VM with staging.
- After the first deploy, create the admins with `docker exec -it showcase-prod-api /app/seed <emails>`.

Required GitHub Repository Secrets:
- `VM_HOST`: VM IP address or hostname
- `VM_USER`: SSH username
- `VM_SSH_KEY`: OpenSSH private key
- `VM_PORT`: SSH port (e.g., `22` or `11114`)

---

## Admin and Media modules

| Module | Path | Owns |
| :--- | :--- | :--- |
| `admin` | `internal/admin/{handler,service,repository}` | `GET /admin/works`, `GET /admin/summary`, `POST /admin/works/:id/publish`, `POST /admin/works/:id/unpublish`, `PATCH /admin/works/:id/feature` |
| `media` | `internal/media/{handler,service,repository}` | `GET /media/works/:id/cover`, image checks (JPEG/PNG/WebP by content, 5 MB), random object keys, stable and signed URLs, local-disk object storage (`MEDIA_LOCAL_DIR`) |

- Modules talk only through service interfaces. `works` uses media through `works/service.MediaStore`, `admin` through `admin/service.CoverURLs`, and media asks `works` about a cover through `media/service.WorkLookup` (bound in `cmd/api/adapters.go`).
- After a committed admin change the order is: commit, bump `works:version`, delete `work:{id}`, publish the event. Shared Redis keys live in `pkg/cache/keys.go`.
- Status changes are conditional updates (`409` for the loser of a race). Featuring runs under a Postgres advisory lock and clears the previous mark in the same transaction.
- Tests: `go test -race ./...` (admin and media service rules, local storage, `pkg/pgarray`).

1
## Monitoring (Prometheus + Grafana)

`docker compose up -d` starts Prometheus (7 days of data, 512 MB) and Grafana (256 MB) next to the API.
Prometheus scrapes the API's internal metrics port `api:9100` over the Docker network; that port is never published.
Both UIs listen on the VM's loopback only (`127.0.0.1`), so they are not reachable from the internet and the API does not proxy them.
Reach them with an SSH tunnel from your computer:

```bash
ssh -N -L 3000:127.0.0.1:3000 -L 9090:127.0.0.1:9090 -p <VM_PORT> <VM_USER>@<VM_HOST>
# Grafana    http://localhost:3000   (GRAFANA_ADMIN_USER / GRAFANA_ADMIN_PASSWORD)
# Prometheus http://localhost:9090   (Alerts tab shows monitoring/prometheus/alerts.yml)
```

The datasource and the **Showcase Wall — API** dashboard (traffic, errors, latency by route, WebSocket, logins/OTP, works actions, media, cache, runtime) are provisioned from `monitoring/` and read-only in the UI; edit the JSON and redeploy.

Staging deploy (`.github/workflows/deploy-staging.yml`) copies `monitoring/`, writes `GRAFANA_ADMIN_PASSWORD` from the GitHub secret of the same name into `/opt/bcc-showcase/.env`, and fails the deploy if Prometheus cannot scrape the API or Grafana cannot load the dashboard and datasource.
