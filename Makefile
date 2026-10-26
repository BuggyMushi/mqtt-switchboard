APP_NAME := mqtt-switchboard
VERSION := 1.0.0
BUILD_DIR := dist
SRC_DIR := ./cmd/mqtt-switchboard
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build cross-compile clean test

all: build

build:
	@echo "==> Building for host architecture..."
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME) $(SRC_DIR)

test:
	@echo "==> Running unit tests..."
	go test -v ./...

cross-compile: clean
	@echo "==> Cross-compiling for OpenWrt architectures..."
	@mkdir -p $(BUILD_DIR)

	# 1. Linux x86_64 (amd64)
	@echo "--> Compiling linux/amd64..."
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -trimpath -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-linux-amd64 $(SRC_DIR)

	# 2. Linux aarch64 (arm64)
	@echo "--> Compiling linux/arm64..."
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
		go build -trimpath -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-linux-arm64 $(SRC_DIR)

	# 3. Linux armv7 (arm)
	@echo "--> Compiling linux/armv7..."
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
		go build -trimpath -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-linux-armv7 $(SRC_DIR)

	# 4. Linux mipsle (MIPS Little-Endian with softfloat for MT7621/MT7628)
	@echo "--> Compiling linux/mipsle (GOMIPS=softfloat)..."
	CGO_ENABLED=0 GOOS=linux GOARCH=mipsle GOMIPS=softfloat \
		go build -trimpath -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-linux-mipsle $(SRC_DIR)

	@echo "==> Cross-compilation completed successfully! Artifacts located in $(BUILD_DIR)/"
	@ls -lh $(BUILD_DIR)

clean:
	@rm -rf $(BUILD_DIR)
