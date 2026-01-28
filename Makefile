# Makefile for loomcli project

.PHONY: all build test lint clean install help

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

# Show help
help:
	@echo "Loomcli Makefile targets:"
	@echo "  make build   - Build the loom binary"
	@echo "  make test    - Run all tests with coverage"
	@echo "  make lint    - Run golangci-lint"
	@echo "  make install - Install loom to GOPATH/bin"
	@echo "  make clean   - Remove build artifacts"
	@echo "  make help    - Show this help message"
