GO ?= go
GOLANGCI_LINT ?= golangci-lint
GORELEASER ?= goreleaser
BINARY ?= go-updater
BUILD_DIR ?= build

.PHONY: all build build-x86_64 build-arm64 build-darwin-arm64 clean debug-amd64 debug-arm64 darwin-debug-arm64 clean-debug fix fmt-check lint snapshot test vet tidy-check

all: build

build: build-x86_64 build-arm64 build-darwin-arm64

build-x86_64:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "-s -w" -o $(BUILD_DIR)/$(BINARY)_x86_64 .

build-arm64:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "-s -w" -o $(BUILD_DIR)/$(BINARY)_arm64 .

build-darwin-arm64:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -ldflags "-s -w" -o $(BUILD_DIR)/$(BINARY)_darwin_arm64 .

clean:
	rm -f $(BUILD_DIR)/$(BINARY)_x86_64 $(BUILD_DIR)/$(BINARY)_arm64 $(BUILD_DIR)/$(BINARY)_darwin_arm64
	rm -f $(BUILD_DIR)/$(BINARY)-debug-amd64 $(BUILD_DIR)/$(BINARY)-debug-arm64 $(BUILD_DIR)/$(BINARY)-darwin-debug-arm64
	rmdir $(BUILD_DIR) 2>/dev/null || true

debug-amd64:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -gcflags "all=-N -l" -o $(BUILD_DIR)/$(BINARY)-debug-amd64 .

debug-arm64:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -gcflags "all=-N -l" -o $(BUILD_DIR)/$(BINARY)-debug-arm64 .

darwin-debug-arm64:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -gcflags "all=-N -l" -o $(BUILD_DIR)/$(BINARY)-darwin-debug-arm64 .

clean-debug:
	rm -f $(BUILD_DIR)/$(BINARY)-debug-amd64 $(BUILD_DIR)/$(BINARY)-debug-arm64 $(BUILD_DIR)/$(BINARY)-darwin-debug-arm64

fix:
	$(GO) fix ./...

fmt-check:
	$(GOLANGCI_LINT) fmt --diff

lint:
	$(GOLANGCI_LINT) run ./...

snapshot:
	$(GORELEASER) release --snapshot --clean

test:
	$(GO) test -race -mod=readonly ./...
	$(GO) test -mod=readonly ./...

vet:
	$(GO) vet -mod=readonly ./...

tidy-check:
	$(GO) mod tidy -go=1.27 -diff
