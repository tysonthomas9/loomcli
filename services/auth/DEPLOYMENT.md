# Auth Service — Self-Hosted Deployment Guide

## Quick Start (Docker Compose)

```bash
cd services/auth

# 1. Configure environment
cp .env.example .env
# Fill in BETTER_AUTH_SECRET and POSTGRES_PASSWORD:
#   openssl rand -hex 32        # → BETTER_AUTH_SECRET
#   openssl rand -base64 24     # → POSTGRES_PASSWORD

# 2. Start services
docker compose up -d

# 3. Verify
curl http://localhost:3001/health
# → {"status":"ok"}

# 4. Connect Loom
loom serve --auth-url http://localhost:3001
```

## Production Deployment (with HTTPS)

Requires a domain with DNS pointing to your server and ports 80/443 open.

```bash
# 1. Set AUTH_DOMAIN in .env
echo 'AUTH_DOMAIN=auth.mycompany.com' >> .env

# 2. Update BETTER_AUTH_URL to match
# BETTER_AUTH_URL=https://auth.mycompany.com

# 3. Start with production overlay
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

Caddy automatically obtains Let's Encrypt certificates for your domain.

## TLS Requirements

The auth service does **not** handle TLS directly. It **must** sit behind a TLS-terminating reverse proxy in production.

- In production (`NODE_ENV=production`), the service logs a warning when requests arrive without `X-Forwarded-Proto: https` (rate-limited to 1 per 60 seconds).
- `docker-compose.prod.yml` provides Caddy as the TLS-terminating proxy.
- If using your own reverse proxy (e.g., nginx), ensure it sets `X-Forwarded-Proto` and `X-Forwarded-For` headers.

### Example nginx config

```nginx
server {
    listen 443 ssl http2;
    server_name auth.mycompany.com;

    ssl_certificate     /etc/letsencrypt/live/auth.mycompany.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/auth.mycompany.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:3001;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

## OAuth Provider Setup

### GitHub

1. Go to [GitHub Developer Settings](https://github.com/settings/developers) → **OAuth Apps** → **New OAuth App**
   - **Important:** Use "OAuth App", NOT "GitHub App"
2. Set **Authorization callback URL** to: `<BETTER_AUTH_URL>/api/auth/callback/github`
3. Copy **Client ID** and **Client Secret** to `.env`:
   ```
   GITHUB_CLIENT_ID=your_client_id
   GITHUB_CLIENT_SECRET=your_client_secret
   ```
4. Better Auth requests `user:email` scope by default — verify this if you customized scopes

### Google

1. Go to [Google Cloud Console → Credentials](https://console.cloud.google.com/apis/credentials)
2. **Configure OAuth consent screen first** (required before creating credentials):
   - **Internal** = organization members only
   - **External** = any Google account (requires verification for >100 users)
3. Create **OAuth client ID** → **Web application**
4. Add **Authorized redirect URI**: `<BETTER_AUTH_URL>/api/auth/callback/google`
5. Copy **Client ID** and **Client Secret** to `.env`:
   ```
   GOOGLE_CLIENT_ID=your_client_id
   GOOGLE_CLIENT_SECRET=your_client_secret
   ```

## Common Pitfalls Checklist

- [ ] **redirect_uri_mismatch**: Callback URL must EXACTLY match provider registration (trailing slashes, http vs https, port numbers)
- [ ] **Missing user:email scope (GitHub)**: Better Auth requests this by default, but verify if scopes were customized
- [ ] **BETTER_AUTH_URL mismatch**: Must match the public-facing URL, not `localhost` in production
- [ ] **TRUSTED_ORIGINS missing WebUI origin**: Loom WebUI origin must be in `TRUSTED_ORIGINS` or CORS blocks auth
- [ ] **Google consent screen not configured**: Required before credentials work
- [ ] **GitHub OAuth App vs GitHub App**: Use "OAuth App" under Developer settings, NOT "GitHub App"
- [ ] **Port mismatch in callback URL**: Non-default `AUTH_PORT` must be reflected in the provider callback URL

## Database

### PostgreSQL (recommended)

Default in `docker-compose.yml`. Data persists in the `postgres-data` Docker volume.

**Backup:**
```bash
docker compose exec postgres pg_dump -U loom loom_auth > backup.sql
```

> **SECURITY WARNING:** Backups contain private signing keys from the `jwks` table. They **must** be encrypted at rest.
>
> Recommendations:
> - `pg_dump -U loom loom_auth | gpg --symmetric --cipher-algo AES256 > backup.sql.gpg`
> - Encrypted storage volumes (AWS EBS encryption, GCP PD encryption)
> - Managed database encryption (RDS, Cloud SQL)

**Restore:**
```bash
docker compose exec -T postgres psql -U loom loom_auth < backup.sql
```

### SQLite (single instance only)

Edit `docker-compose.yml`: remove the `postgres` service and environment overrides, add a volume mount for the database file, and set `DATABASE_PROVIDER=sqlite`.

> **SECURITY WARNING:** The SQLite database file contains private signing keys. Encrypt at rest.
>
> Recommendations:
> - Encrypted archives (`tar czf - auth.db | gpg --symmetric > auth.db.tar.gpg`)
> - Encrypted filesystem (LUKS, FileVault)
> - `chmod 600` on the database file

**Do NOT use SQLite with multiple auth service replicas.** WAL mode allows concurrent reads but only a single writer.

## Key Rotation

### Automatic

- Keys rotate every **7 days** (rotationInterval: 604800 seconds)
- Old keys remain valid for **24 hours** after rotation (gracePeriod: 86400 seconds)
- The Go JWKS cache (5-minute TTL with on-demand refresh) picks up new keys automatically

### Emergency (Compromised Key)

If a signing key is compromised, force immediate rotation:

1. **Identify the compromised key:**
   ```bash
   docker compose exec postgres psql -U loom loom_auth -c \
     "SELECT id, created_at FROM jwks ORDER BY created_at DESC;"
   ```

2. **Expire the compromised key immediately** by backdating it beyond the rotation + grace window:
   ```bash
   docker compose exec postgres psql -U loom loom_auth -c \
     "UPDATE jwks SET created_at = created_at - INTERVAL '8 days' WHERE id = '<key-id>';"
   ```

3. **Restart the auth service** to force new key pair generation:
   ```bash
   docker compose restart auth
   ```

4. **Verify the new key is active:**
   ```bash
   curl http://localhost:3001/api/auth/jwks | jq '.keys | length'
   # Should show 2 keys (new active + expired old)
   ```

5. **After 24 hours**, the expired key drops from JWKS. Optionally delete it immediately:
   ```bash
   docker compose exec postgres psql -U loom loom_auth -c \
     "DELETE FROM jwks WHERE id = '<compromised-key-id>';"
   docker compose restart auth
   ```

6. For **SQLite** deployments, use `sqlite3 /app/data/auth.db` instead of `psql`.

### Changing BETTER_AUTH_SECRET

The `BETTER_AUTH_SECRET` is used to encrypt private keys stored in the `jwks` table. Changing it makes existing keys undecryptable.

1. Clear the jwks table: `DELETE FROM jwks;`
2. Restart the auth service — new keys are auto-generated on the next signing request
3. All active JWTs become unverifiable (15-minute TTL limits impact)

## Architecture

```
                    ┌─────────────┐
                    │   Caddy     │ ← ports 80/443 (prod only)
                    │  auto-HTTPS │
                    └──────┬──────┘
                           │
              ┌────────────▼────────────┐
              │    Auth Service         │ ← 127.0.0.1:3001
              │    (Hono + Better Auth) │
              │    metrics: 9090        │ ← internal only
              └────────────┬────────────┘
                           │
              ┌────────────▼────────────┐
              │    PostgreSQL           │ ← Docker internal network
              │    (not exposed)        │
              └─────────────────────────┘
```

## Updating Docker Images

All Docker images use digest pinning (`@sha256:...`) for reproducible builds. To update:

```bash
# Pull latest and get new digest
docker pull node:22-alpine
docker inspect --format='{{index .RepoDigests 0}}' node:22-alpine

# Update the digest in Dockerfile and docker-compose.yml
# Then rebuild
docker compose build --no-cache
```
