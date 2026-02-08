# Makefile for loomcli project

.PHONY: all build test lint clean install help frontend build-all sync-beads update-beads gate hooks

# Default target
all: build

# Build the loom binary
build:
	@echo "Building loom..."
	go build -ldflags="-X main.Build=$$(git rev-parse --short HEAD)" -o loom ./cmd/loom

# Run all tests with coverage
test:
	@echo "Running tests..."
	@TEST_COVER=1 ./scripts/test.sh

# Run linter
lint:
	@echo "Running linter..."
	golangci-lint run --timeout=5m

# Install loom to GOPATH/bin
install:
	@echo "Installing loom to $$(go env GOPATH)/bin..."
	@bash -c 'commit=$$(git rev-parse HEAD 2>/dev/null || echo ""); \
		branch=$$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo ""); \
		go install -ldflags="-X main.Commit=$$commit -X main.Branch=$$branch" ./cmd/loom'

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f loom
	rm -f /tmp/loom.coverage.out

# Frontend directory
FRONTEND_DIR := internal/webui/frontend

# Beads subtree config
BEADS_REMOTE := https://github.com/tysonthomas9/beads
BEADS_BRANCH := feature/web-ui
BEADS_PREFIX := third_party/beads

# Build frontend (requires Node.js >= 20)
# Run this after modifying frontend source; dist/ must exist for go:embed
frontend:
	@echo "Building frontend..."
	cd $(FRONTEND_DIR) && npm install && npm run build

# Build everything (frontend + Go binary)
build-all: frontend build

# Sync beads library packages (rewrite imports from vendored copy)
sync-beads:
	@echo "Syncing beads library packages..."
	./scripts/sync-beads.sh

# Pull latest beads from upstream and sync
update-beads:
	@echo "Pulling latest beads from $(BEADS_REMOTE) ($(BEADS_BRANCH))..."
	git subtree pull --prefix=$(BEADS_PREFIX) $(BEADS_REMOTE) $(BEADS_BRANCH) --squash
	$(MAKE) sync-beads

# Quality gate — build + vet + test (used by pre-push hook)
gate:
	@echo "=== Quality Gate ==="
	@go build ./...
	@go vet ./...
	@go test -timeout 15m ./...
	@echo "=== Quality Gate PASSED ==="

# Install git hooks (pre-push quality gate, applies to all worktrees)
hooks:
	@cp scripts/hooks/pre-push .git/hooks/pre-push
	@chmod +x .git/hooks/pre-push
	@echo "Pre-push hook installed (applies to all worktrees)"

# Show help
help:
	@echo "Loomcli Makefile targets:"
	@echo "  make build   - Build the loom binary"
	@echo "  make test    - Run all tests with coverage"
	@echo "  make lint    - Run golangci-lint"
	@echo "  make install - Install loom to GOPATH/bin"
	@echo "  make frontend  - Build frontend (requires Node.js >= 20)"
	@echo "  make build-all - Build frontend + Go binary"
	@echo "  make sync-beads   - Sync beads packages (rewrite imports)"
	@echo "  make update-beads - Pull latest beads + sync"
	@echo "  make gate         - Quality gate (build + vet + test)"
	@echo "  make hooks        - Install git hooks (pre-push gate)"
	@echo "  make clean        - Remove build artifacts"
	@echo "  make help         - Show this help message"
