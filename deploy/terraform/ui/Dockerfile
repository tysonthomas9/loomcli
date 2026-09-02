# Serves the loom SPA and proxies API traffic to the loom container.
#
# `loom serve` handles only /api/*, /healthz, /sse/* and /ws-tab/*; everything
# else is the static bundle. Build from the repo root AFTER building the
# frontend, so internal/webui/frontend/dist exists:
#
#   make build-frontend
#   docker build -f deploy/terraform/ui/Dockerfile -t <registry>/loom-ui:<tag> .
#   docker push <registry>/loom-ui:<tag>
FROM docker.io/library/caddy:2-alpine@sha256:5f5c8640aae01df9654968d946d8f1a56c497f1dd5c5cda4cf95ab7c14d58648

COPY deploy/terraform/ui/Caddyfile /etc/caddy/Caddyfile
COPY internal/webui/frontend/dist /srv
