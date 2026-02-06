# Beads Web UI

A React + Go web interface for the Beads issue tracking system. Provides Kanban, Table, Graph, and Monitor views with real-time updates and multi-agent support.

## Features

| Feature | Description |
|---------|-------------|
| Kanban Board | Drag-and-drop swim lanes grouped by status, assignee, epic, label, priority |
| Table View | Sortable, searchable table with bulk selection and bulk actions |
| Graph View | Interactive dependency graph visualization (React Flow + Dagre layout) |
| Monitor Dashboard | Multi-agent operator dashboard for tracking parallel AI agent work |
| Real-time Updates | SSE (Server-Sent Events) for live mutation events |
| Filtering & Search | Filter by status, priority, type, labels; synced to URL params |
| Issue Detail Panel | Side panel with full issue details, comments, dependencies |
| Bulk Operations | Close or update priority for multiple issues at once |

## How It Works

See [docs/WEB_UI.md](../../docs/WEB_UI.md) for a high-level architecture and data-flow overview.

## Prerequisites

- **Go** 1.24 or later
- **Node.js** 20.0 or later
- **npm** (comes with Node.js)
- **Beads daemon** must be running (`bd daemon start`)

## Quick Start

```bash
bd daemon start
make run

# Open http://localhost:8080
```

## Development

### Frontend Hot-Reload (Vite)

For frontend development with hot reload:

```bash
# Start beads daemon
bd daemon start

# Terminal 1: Start the Go server (for API proxy)
make server
./webui

# Terminal 2: Start the Vite dev server
make dev
# Open http://localhost:3000
```

API requests from the frontend dev server are proxied to the Go backend.

### Backend Hot-Reload (air)

For Go server development with automatic restart on file changes:

```bash
# Start beads daemon
bd daemon start

# One-time: Install air (optional but recommended)
go install github.com/air-verse/air@latest

# Ensure frontend is built (required for embed)
make frontend

# Run with hot-reload
make dev-go
# Open http://localhost:8080
```

The server will automatically rebuild and restart when `.go` files change.

**Note**: Frontend changes require rebuilding with `make frontend` since the frontend is embedded at compile time. For frontend hot-reload, use the "Full-Stack Development" workflow below.

### Full-Stack Development

For simultaneous frontend and backend hot-reload:

```bash
# Start beads daemon
bd daemon start

# Terminal 1: Go server with hot-reload
make dev-go

# Terminal 2: Frontend dev server
make dev
# Open http://localhost:3000
```

## Agent Monitoring

The web UI includes an Agents sidebar that connects to a loom server for monitoring parallel AI agents.

```bash
# Full startup sequence
bd daemon start              # Start beads daemon
loom serve --port 9000       # Start loom server (for Agents sidebar)
air                          # Start web UI backend (or: go run . -port 8080)
```

The `VITE_LOOM_SERVER_URL` environment variable configures the loom server URL (defaults to `http://localhost:9000`). The Agents sidebar shows "Loom server not available" if loom isn't running.

## Project Structure

```
beads-web-ui/
├── frontend/           # React + TypeScript + Vite
│   ├── src/
│   │   ├── api/        # API client modules
│   │   ├── components/ # React components (40+)
│   │   ├── hooks/      # Custom React hooks (45+)
│   │   ├── types/      # TypeScript type definitions
│   │   ├── styles/     # Global CSS
│   │   ├── utils/      # Utility functions
│   │   ├── test-utils/ # Testing utilities
│   │   └── __tests__/  # Unit tests
│   ├── tests/
│   │   └── e2e/        # Playwright E2E tests
│   ├── dist/           # Built assets (generated)
│   └── package.json
├── daemon/             # Go daemon connection pool
├── main.go             # Server entry point
├── routes.go           # HTTP route definitions
├── handlers.go         # API endpoint handlers
├── handlers_comments.go # Comment endpoint handlers
├── sse.go              # Server-Sent Events (real-time updates)
├── subscription.go     # SSE subscription management
├── cors.go             # CORS middleware
├── security.go         # Security headers middleware
├── embed.go            # Frontend asset embedding
├── Makefile
├── CLAUDE.md           # Developer guidance
└── README.md
```

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Basic health check |
| `/api/health` | GET | Detailed health with daemon status |
| `/api/issues` | GET | List issues (supports filters) |
| `/api/issues/{id}` | GET | Get single issue |
| `/api/issues` | POST | Create new issue |
| `/api/issues/{id}` | PATCH | Update issue |
| `/api/issues/{id}/close` | POST | Close an issue |
| `/api/issues/{id}/comments` | POST | Add a comment |
| `/api/issues/{id}/dependencies` | POST | Add a dependency |
| `/api/issues/{id}/dependencies/{depId}` | DELETE | Remove a dependency |
| `/api/ready` | GET | Issues ready to work on |
| `/api/blocked` | GET | Issues blocked by dependencies |
| `/api/issues/graph` | GET | Dependency graph data |
| `/api/stats` | GET | Project statistics |
| `/api/metrics` | GET | SSE hub metrics |
| `/api/events` | GET (SSE) | Real-time mutation events |

## Testing

- **Go tests**: `make test` (requires frontend build for embed — see [CLAUDE.md](CLAUDE.md) for workarounds)
- **Frontend unit tests**: `cd frontend && npm test`
- **E2E tests**: `make test-e2e` (Playwright — runs `setup-e2e` automatically)
- **E2E interactive**: `make test-e2e-ui` (opens Playwright UI)

See [CLAUDE.md](CLAUDE.md) for detailed testing guidance.

## Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `BEADS_WEBUI_PORT` | 8080 | HTTP server port |
| `LOOM_SERVER_URL` | `http://localhost:9000` | Backend loom server URL for proxy |
| `LOOM_PROXY_ALLOWED_HOSTS` | (empty) | Comma-separated list of non-localhost hosts to allow proxying to |
| `VITE_LOOM_SERVER_URL` | `http://localhost:9000` | Frontend loom server URL for agent monitoring |

Command-line flags:

```bash
./webui -port 3000           # Custom port
./webui -socket /path/to.sock # Daemon socket path
```

## Makefile Targets

| Target | Description |
|--------|-------------|
| `build` | Build frontend and Go server for production |
| `dev` | Start frontend dev server with hot reload |
| `dev-go` | Start Go server with hot-reload (requires air) |
| `frontend` | Build only the frontend (npm install + build) |
| `server` | Build only the Go server |
| `run` | Build and run the server |
| `clean` | Remove build artifacts |
| `test` | Run Go tests |
| `setup-e2e` | Install Playwright browsers |
| `test-e2e` | Run E2E tests with Playwright |
| `test-e2e-ui` | Open Playwright UI for interactive testing |
| `help` | Show help message |

## Tech Stack

| Layer | Technology |
|-------|------------|
| Frontend | React 18, TypeScript, Vite |
| Backend | Go (stdlib net/http) |
| State | Zustand |
| Visualization | React Flow, Dagre |
| Drag & Drop | dnd-kit |
| Testing | Vitest, Playwright, Go testing |
