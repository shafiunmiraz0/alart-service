BINARY_NAME := alart-service
BUILD_DIR := .
CMD_DIR := ./cmd/alart-service
VERSION := 1.0.0
LDFLAGS := -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: build clean install uninstall test ensure-go

## ensure-go: Install Go if not present
ensure-go:
	@command -v go >/dev/null 2>&1 || { \
		echo "[INFO] Go not found. Installing golang-go..."; \
		apt-get update >/dev/null 2>&1 && apt-get install -y golang-go >/dev/null 2>&1 || { echo "[ERROR] Failed to install golang-go."; exit 1; }; \
		echo "[INFO] Go installed successfully."; \
	}

## build: Build the binary for Linux amd64
build: ensure-go
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY_NAME) $(CMD_DIR)

## build-arm: Build the binary for Linux ARM64 (Raspberry Pi, etc.)
build-arm:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY_NAME)-arm64 $(CMD_DIR)

## clean: Remove built binaries
clean:
	rm -f $(BINARY_NAME) $(BINARY_NAME)-arm64

## install: Install on the local system (requires root)
install: build
	sudo bash install.sh

## uninstall: Remove the service from the system (keeps config)
uninstall:
	sudo bash uninstall.sh

## purge: Remove the service and all config/logs
purge:
	sudo bash uninstall.sh --purge

## reload: Reload configuration without restarting
reload:
	alart -s reload

## test-config: Test configuration file syntax
test-config:
	alart -t

## gen-config: Generate a default config file locally
gen-config:
	go run $(CMD_DIR) -gen-config -config ./config.json

## test: Run tests
test:
	go test ./...

## help: Show this help
help:
	@echo "Available targets:"
	@grep -E '^## ' Makefile | sed 's/## /  /'
