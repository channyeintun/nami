PREFIX ?= /usr/local/bin
BINARY_NAME := nami
VERSION := 0.1.0
BUILD_DIR := release
GO_SRC := ./cmd/nami
UNIX_PLATFORMS := darwin-arm64 darwin-amd64 linux-amd64 linux-arm64
WINDOWS_PLATFORMS := windows-amd64 windows-arm64
PLATFORMS := $(UNIX_PLATFORMS) $(WINDOWS_PLATFORMS)

.PHONY: all release-local release install clean build-tui-launcher $(PLATFORMS)

all: release-local

release-local:
	@mkdir -p $(BUILD_DIR)
	@local_os=$$(go env GOOS); \
	local_arch=$$(go env GOARCH); \
	if [ "$$local_os" = "windows" ]; then \
		GOOS=$$local_os GOARCH=$$local_arch CGO_ENABLED=0 go build -o $(BUILD_DIR)/$(BINARY_NAME).exe $(GO_SRC); \
		echo "Built: $(BUILD_DIR)/$(BINARY_NAME).exe"; \
	else \
		GOOS=$$local_os GOARCH=$$local_arch CGO_ENABLED=0 go build -o $(BUILD_DIR)/$(BINARY_NAME) $(GO_SRC); \
		echo "Built: $(BUILD_DIR)/$(BINARY_NAME)"; \
	fi

release:
	@rm -rf $(BUILD_DIR)
	@mkdir -p $(BUILD_DIR)/assets
	@for platform in $(PLATFORMS); do \
		os=$$(echo $$platform | cut -d- -f1); \
		arch=$$(echo $$platform | cut -d- -f2); \
		dir=$(BUILD_DIR)/$(BINARY_NAME)-$$platform; \
		mkdir -p $$dir; \
		echo "Building $$platform..."; \
		if [ "$$os" = "windows" ]; then \
			binary=$(BINARY_NAME).exe; \
			asset=$(BUILD_DIR)/assets/$(BINARY_NAME)-$$platform.exe; \
			GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -o $$dir/$$binary $(GO_SRC); \
			install -m 755 $$dir/$$binary $$asset; \
			(cd $(BUILD_DIR) && zip -qr $(BINARY_NAME)-$$platform.zip $(BINARY_NAME)-$$platform); \
			echo "  -> $$asset"; \
			echo "  -> $(BUILD_DIR)/$(BINARY_NAME)-$$platform.zip"; \
		else \
			binary=$(BINARY_NAME); \
			asset=$(BUILD_DIR)/assets/$(BINARY_NAME)-$$platform; \
			GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -o $$dir/$$binary $(GO_SRC); \
			install -m 755 $$dir/$$binary $$asset; \
			tar -czf $(BUILD_DIR)/$(BINARY_NAME)-$$platform.tar.gz -C $(BUILD_DIR) $(BINARY_NAME)-$$platform; \
			echo "  -> $$asset"; \
			echo "  -> $(BUILD_DIR)/$(BINARY_NAME)-$$platform.tar.gz"; \
		fi; \
	done
	@echo ""
	@echo "Release artifacts in $(BUILD_DIR)/"

install: release-local
	install -d $(PREFIX)
	install -m 755 $(BUILD_DIR)/$(BINARY_NAME) $(PREFIX)/$(BINARY_NAME)
	@echo "Installed to $(PREFIX)/$(BINARY_NAME)"

clean:
	rm -rf $(BUILD_DIR)

build-tui-launcher:
	@echo "The JavaScript TUI launcher has been replaced by the Go Bubble Tea executable."
