# BCC Showcase Wall — Backend API (Go + Gin + Gorm)

Backend service for **BCC Showcase Wall**, currently implementing the complete Authentication & Session module:
- **Email + Password + 6-Digit Email OTP** (Registration, Login, Resend OTP, Verify OTP)
- **Firebase OAuth (Google Sign-In / Sign-Up)** with automatic account linking (`POST /api/v1/auth/oauth/firebase`)
- **Session Management** (`JWT` 15-minute access token + 1-day `HttpOnly` rotating refresh token cookie with reuse detection)
- **Embedded SQL Migrations** (`0001_create_users.sql`, `0002_create_sessions.sql`, `0003_oauth_users.sql`)
- **Admin Seed CLI** (`cmd/seed`)

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
   - **New User:** Creates a new user (`password_hash = NULL`, `auth_provider = "google"`). Assigns role `Admin` if the email is listed in `ADMIN_EMAIL` or `ADMIN_EMAILS`, otherwise `Contributor`.
4. **Immediate Session:** Skips email OTP and directly issues the 15-minute `access_token` and 1-day `HttpOnly` `refresh_token` cookie.

---

## 2. Environment Variables (`.env`)

Copy `.env.example` to `.env` and configure:

- `FIREBASE_PROJECT_ID`: Your Firebase project ID.
- `FIREBASE_CREDENTIALS_JSON`: Inline JSON string of your Firebase Service Account key (or set `FIREBASE_CREDENTIALS_FILE=/app/secrets/firebase.json`).
- `ADMIN_EMAILS`: Comma-separated list of emails that automatically receive the `Admin` role when signing up via Firebase OAuth.

---

## 3. Running with Docker Compose

Build and start PostgreSQL, Redis, and the Go API container:

```bash
cp .env.example .env
docker compose up -d --build
```

Seed the initial Admin account (uses `ADMIN_EMAIL`, `ADMIN_PASSWORD`, `ADMIN_NAME` from `.env`):

```bash
docker compose exec api /app/seed
```

---

## 4. CI/CD Deployment (`.github/workflows/deploy-staging.yml`)

Pushing to the `staging` branch builds `bcc-showcase-app:staging` on GitHub Actions, transfers the image archive to the VM over SCP, loads it into Docker, and runs `docker compose up -d --no-build` in `/opt/bcc-showcase`.

Required GitHub Repository Secrets:
- `VM_HOST`: VM IP address or hostname
- `VM_USER`: SSH username
- `VM_SSH_KEY`: OpenSSH private key
- `VM_PORT`: SSH port (e.g., `22` or `11114`)
