BINARY_NAME := nami
ENGINE_NAME := nami-engine
LAUNCHER_JS_NAME := $(BINARY_NAME).js
VERSION     := 0.1.0
BUILD_DIR   := release
GO_SRC      := ../cmd/nami-engine
PREFIX      ?= /usr/local/bin
POSIX_WRAPPER := bin/$(BINARY_NAME)
WINDOWS_WRAPPER := bin/$(BINARY_NAME).cmd
LAUNCHER_NORMALIZER := scripts/normalize-launcher-bundle.mjs
LAUNCHER_BUNDLER_CONFIG := rolldown.config.mjs
# Native addons the launcher requires at runtime. They stay out of the bundle,
# so an install has to place them where node can resolve them from the launcher.
NATIVE_RUNTIME_SCOPES := @napi-rs @resvg

UNIX_PLATFORMS := darwin-arm64 darwin-amd64 linux-amd64 linux-arm64
WINDOWS_PLATFORMS := windows-amd64 windows-arm64
PLATFORMS := $(UNIX_PLATFORMS) $(WINDOWS_PLATFORMS)

.PHONY: all clean release release-local build-portable-launcher-bundle $(PLATFORMS)

all: release-local


# Build for current platform only (fast, for development)
release-local: clean build-portable-launcher-bundle
	@mkdir -p $(BUILD_DIR)
	@local_os=$$(go env GOOS); \
	local_arch=$$(go env GOARCH); \
	if [ "$$local_os" = "windows" ]; then \
		install -m 644 $(WINDOWS_WRAPPER) $(BUILD_DIR)/$(BINARY_NAME).cmd; \
		GOOS=$$local_os GOARCH=$$local_arch CGO_ENABLED=0 go build -o $(BUILD_DIR)/$(ENGINE_NAME).exe $(GO_SRC); \
		echo ""; \
		echo "Built: $(BUILD_DIR)/$(BINARY_NAME).cmd + $(BUILD_DIR)/$(LAUNCHER_JS_NAME) + $(BUILD_DIR)/$(ENGINE_NAME).exe"; \
	else \
		install -m 755 $(POSIX_WRAPPER) $(BUILD_DIR)/$(BINARY_NAME); \
		GOOS=$$local_os GOARCH=$$local_arch CGO_ENABLED=0 go build -o $(BUILD_DIR)/$(ENGINE_NAME) $(GO_SRC); \
		echo ""; \
		echo "Built: $(BUILD_DIR)/$(BINARY_NAME) + $(BUILD_DIR)/$(LAUNCHER_JS_NAME) + $(BUILD_DIR)/$(ENGINE_NAME)"; \
	fi
	@echo "The launcher now requires one of these runtimes where it runs: node, bun, or deno."
	@echo "Copy the launcher shim, $(LAUNCHER_JS_NAME), and the engine binary to a directory in your PATH to install."

build-portable-launcher-bundle:
	@mkdir -p $(BUILD_DIR)
	vp pack
	vp dlx rolldown -c $(LAUNCHER_BUNDLER_CONFIG)
	node $(LAUNCHER_NORMALIZER) \
		$(BUILD_DIR)/$(LAUNCHER_JS_NAME).raw \
		$(BUILD_DIR)/$(LAUNCHER_JS_NAME)
	rm -f $(BUILD_DIR)/$(LAUNCHER_JS_NAME).raw
	@mkdir -p $(BUILD_DIR)/node_modules
	@for scope in $(NATIVE_RUNTIME_SCOPES); do \
		if [ -d node_modules/$$scope ]; then \
			cp -R node_modules/$$scope $(BUILD_DIR)/node_modules/; \
		fi; \
	done

# Build release assets for all platforms.
release: clean build-portable-launcher-bundle
	@for platform in $(PLATFORMS); do \
		os=$$(echo $$platform | cut -d- -f1); \
		arch=$$(echo $$platform | cut -d- -f2); \
		dir=$(BUILD_DIR)/$(BINARY_NAME)-$$platform; \
		asset_dir=$(BUILD_DIR)/assets; \
		launcher_js_asset=$$asset_dir/$(BINARY_NAME)-js-$$platform.js; \
		echo "Building $$platform..."; \
		mkdir -p $$dir $$asset_dir; \
		install -m 755 $(BUILD_DIR)/$(LAUNCHER_JS_NAME) $$dir/$(LAUNCHER_JS_NAME); \
		if [ "$$os" = "windows" ]; then \
			engine_name=$(ENGINE_NAME).exe; \
			wrapper_asset=$$asset_dir/$(BINARY_NAME)-$$platform.cmd; \
			engine_asset=$$asset_dir/$(ENGINE_NAME)-$$platform.exe; \
			GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -o $$dir/$$engine_name $(GO_SRC); \
			install -m 644 $(WINDOWS_WRAPPER) $$dir/$(BINARY_NAME).cmd; \
			install -m 755 $$dir/$(LAUNCHER_JS_NAME) $$launcher_js_asset; \
			install -m 644 $$dir/$(BINARY_NAME).cmd $$wrapper_asset; \
			install -m 755 $$dir/$$engine_name $$engine_asset; \
			(cd $(BUILD_DIR) && zip -qr $(BINARY_NAME)-$$platform.zip $(BINARY_NAME)-$$platform); \
			echo "  -> $$launcher_js_asset"; \
			echo "  -> $$wrapper_asset"; \
			echo "  -> $$engine_asset"; \
			echo "  -> $(BUILD_DIR)/$(BINARY_NAME)-$$platform.zip"; \
		else \
			wrapper_asset=$$asset_dir/$(BINARY_NAME)-$$platform; \
			engine_asset=$$asset_dir/$(ENGINE_NAME)-$$platform; \
			install -m 755 $(POSIX_WRAPPER) $$dir/$(BINARY_NAME); \
			GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -o $$dir/$(ENGINE_NAME) $(GO_SRC); \
			install -m 755 $$dir/$(BINARY_NAME) $$wrapper_asset; \
			install -m 755 $$dir/$(LAUNCHER_JS_NAME) $$launcher_js_asset; \
			install -m 755 $$dir/$(ENGINE_NAME) $$engine_asset; \
			tar -czf $(BUILD_DIR)/$(BINARY_NAME)-$$platform.tar.gz -C $(BUILD_DIR) $(BINARY_NAME)-$$platform; \
			echo "  -> $$wrapper_asset"; \
			echo "  -> $$launcher_js_asset"; \
			echo "  -> $$engine_asset"; \
			echo "  -> $(BUILD_DIR)/$(BINARY_NAME)-$$platform.tar.gz"; \
		fi; \
	done
	@echo ""
	@echo "Release artifacts in $(BUILD_DIR)/"

clean:
	rm -rf $(BUILD_DIR)

# Install locally (copies to /usr/local/bin)
install: release-local
	install -d $(PREFIX)
	install -m 755 $(BUILD_DIR)/$(BINARY_NAME) $(PREFIX)/$(BINARY_NAME)
	install -m 755 $(BUILD_DIR)/$(LAUNCHER_JS_NAME) $(PREFIX)/$(LAUNCHER_JS_NAME)
	install -m 755 $(BUILD_DIR)/$(ENGINE_NAME) $(PREFIX)/$(ENGINE_NAME)
	@for scope in $(NATIVE_RUNTIME_SCOPES); do \
		if [ -d $(BUILD_DIR)/node_modules/$$scope ]; then \
			rm -rf $(PREFIX)/node_modules/$$scope; \
			mkdir -p $(PREFIX)/node_modules; \
			cp -R $(BUILD_DIR)/node_modules/$$scope $(PREFIX)/node_modules/; \
		fi; \
	done
	@echo "Installed to $(PREFIX)/"
