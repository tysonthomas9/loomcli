# deploy/ — Production deployment reference

After Phase 5, the loom API server and the static frontend build are
independent artifacts. This directory is the canonical production
reference: two Dockerfiles and a compose file that wire them together
the way a real deployment would.

```
deploy/
├── docker-compose.yml       # Same-origin compose (recommended)
├── server/
│   └── Dockerfile           # Pure Go API — no Node
└── frontend/
    ├── Dockerfile           # Static SPA + nginx — no Go
    └── nginx.conf           # Serves dist, proxies /api/*, SSE + WS upgrade
```

## Shape 1: Same-origin (recommended)

Frontend and API share an origin. nginx serves the SPA at `/` and
proxies `/api/*` (plus WebSocket upgrades and SSE streams) to the Go
server. No CORS is exercised.

```bash
cd deploy
docker compose up --build
# Open http://localhost:8080
```

The compose file publishes the frontend container's port 80 on the host
as 8080 (matching the legacy single-port UX). The server container is
only reachable on the internal compose network.

## Shape 2: Cross-origin (CDN + API)

Frontend lives on a CDN or static host at one origin; the API server
runs on another. Build the frontend image with the API URL baked in and
run the server image with `--frontend-url` allowing the frontend origin.

```bash
# Build the frontend with a hard-coded API base URL.
docker build \
  -f deploy/frontend/Dockerfile \
  --build-arg VITE_API_BASE_URL=https://api.example.com \
  -t loom-frontend:cross-origin .

# Build and run the API server independently.
docker build -f deploy/server/Dockerfile -t loom-server .
docker run --rm -p 8080:8080 loom-server \
  serve --port 8080 --frontend-url https://app.example.com
```

Deploy `loom-frontend:cross-origin` as a plain static container; point
your DNS at it. Deploy `loom-server` behind whatever TLS terminator you
prefer. The `--frontend-url` flag registers the frontend origin with the
server's CORS middleware.

## Kubernetes

Both images are standalone and have no shared filesystem requirements,
so they map naturally to two Deployments (one per service) behind an
Ingress. The Ingress should forward `/api/*` (with WebSocket upgrade
and SSE-friendly buffering disabled) to the server Service and
everything else to the frontend Service. See `nginx.conf` for the
nginx-flavored version of those rules — translate them to your
Ingress controller's annotations.

## Separate hosts / bare-metal

Run `loom serve --frontend-url https://yourhost` on the API host and
copy the `loomcli-frontend_<version>.tar.gz` release tarball's `dist/`
directory onto the frontend host's static root. Configure your reverse
proxy (nginx, caddy, HAProxy) using the rules in `nginx.conf` as a
starting point.
