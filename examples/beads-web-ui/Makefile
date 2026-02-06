# Makefile for beads-web-ui

.PHONY: all build dev dev-go frontend server run clean test test-e2e test-e2e-ui setup-e2e help

# Binary name
BINARY := webui

# Frontend directory
FRONTEND_DIR := frontend

# Default target: build everything for production
all: build

# Build both frontend and Go server for production
build: frontend server

# Run frontend dev server with hot reload (Vite)
dev:
	@echo "Starting frontend dev server..."
	@echo "API requests will proxy to http://localhost:8080"
	cd $(FRONTEND_DIR) && npm run dev

# Run Go server with hot-reload (requires air: go install github.com/air-verse/air@latest)
dev-go:
	@if [ ! -d "$(FRONTEND_DIR)/dist" ]; then \
		echo "Error: frontend/dist not found. Run 'make frontend' first."; \
		exit 1; \
	fi
	@if ! command -v air > /dev/null 2>&1; then \
		echo "Error: air not found. Install with: go install github.com/air-verse/air@latest"; \
		exit 1; \
	fi
	@echo "Starting Go server with hot-reload..."
	air

# Build only the frontend
frontend:
	@echo "Building frontend..."
	cd $(FRONTEND_DIR) && npm install && npm run build

# Build only the Go server (requires frontend/dist to exist)
server:
	@if [ ! -d "$(FRONTEND_DIR)/dist" ]; then \
		echo "Error: frontend/dist not found. Run 'make frontend' first."; \
		exit 1; \
	fi
	@echo "Building Go server..."
	go build -o $(BINARY) .

# Run the built server
run: build
	@echo "Starting server..."
	./$(BINARY)

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f $(BINARY)
	rm -rf $(FRONTEND_DIR)/dist
	rm -rf $(FRONTEND_DIR)/node_modules

# Run Go tests
test:
	@echo "Running tests..."
	go test -v ./...

# Setup Playwright browsers (run once before E2E tests)
setup-e2e:
	@echo "Installing Playwright browsers..."
	cd $(FRONTEND_DIR) && npx playwright install chromium

# Run E2E tests
test-e2e: setup-e2e
	@echo "Running E2E tests..."
	cd $(FRONTEND_DIR) && npx playwright test

# Open Playwright UI
test-e2e-ui: setup-e2e
	@echo "Opening Playwright UI..."
	cd $(FRONTEND_DIR) && npx playwright test --ui

# Show help
help:
	@echo "beads-web-ui Makefile targets:"
	@echo "  make build      - Build frontend and Go server for production"
	@echo "  make dev        - Start frontend dev server with hot reload"
	@echo "  make dev-go     - Start Go server with hot-reload (requires air)"
	@echo "  make frontend   - Build only the frontend (npm install + build)"
	@echo "  make server     - Build only the Go server"
	@echo "  make run        - Build and run the server"
	@echo "  make clean      - Remove build artifacts"
	@echo "  make test       - Run Go tests"
	@echo "  make setup-e2e  - Install Playwright browsers"
	@echo "  make test-e2e   - Run E2E tests with Playwright"
	@echo "  make test-e2e-ui - Open Playwright UI for interactive testing"
	@echo "  make help       - Show this help message"
