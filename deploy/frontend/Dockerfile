# Production Dockerfile for the loom frontend (static SPA + nginx reverse proxy).
#
# Pure Node + nginx — no Go anywhere. Proves the frontend is decoupled
# from the backend post-Phase 5. Build context is the repo root.
#
# Build (same-origin, proxy /api via nginx):
#   docker build -f deploy/frontend/Dockerfile -t loom-frontend .
#
# Build (cross-origin, hard-coded API URL):
#   docker build -f deploy/frontend/Dockerfile \
#     --build-arg VITE_API_BASE_URL=https://api.example.com \
#     -t loom-frontend .

# ─── Stage 1: Build frontend dist ────────────────────────────────────────────
FROM node:20-bookworm-slim AS builder

WORKDIR /app
COPY internal/webui/frontend/package*.json ./
RUN npm ci
COPY internal/webui/frontend/ .

# Empty default → frontend uses relative URLs (/api/...) which nginx proxies
# to the server container. Override with --build-arg for cross-origin deploys.
ARG VITE_API_BASE_URL=""
ENV VITE_API_BASE_URL=${VITE_API_BASE_URL}
RUN npm run build

# ─── Stage 2: nginx runtime ──────────────────────────────────────────────────
FROM nginx:1.27-alpine AS runtime

COPY deploy/frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY deploy/frontend/99-render-nginx-resolver.sh /docker-entrypoint.d/99-render-nginx-resolver.sh
RUN chmod +x /docker-entrypoint.d/99-render-nginx-resolver.sh
COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 80
