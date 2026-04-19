#!/bin/sh
set -e

# nami installer for macOS and Linux
# Usage: curl -fsSL https://raw.githubusercontent.com/channyeintun/nami/main/nami/install.sh | sh

REPO="channyeintun/nami"
BINARY_NAME="nami"
ENGINE_NAME="nami-engine"
LAUNCHER_JS_NAME="${BINARY_NAME}.js"

DEFAULT_SYSTEM_DIR="/usr/local/bin"
DEFAULT_USER_DIR="${HOME}/.local/bin"
INSTALL_DIR="${INSTALL_DIR:-}"
USE_SUDO="false"
JS_RUNTIME=""

# Detect OS and architecture
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)       echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
  darwin|linux) ;;
  *) echo "Unsupported OS: $OS. On Windows, use nami/install.ps1 instead."; exit 1 ;;
esac

PLATFORM="${OS}-${ARCH}"
ARCHIVE="${BINARY_NAME}-${PLATFORM}.tar.gz"

if [ -z "$INSTALL_DIR" ]; then
  if [ -d "$DEFAULT_SYSTEM_DIR" ] && [ -w "$DEFAULT_SYSTEM_DIR" ]; then
    INSTALL_DIR="$DEFAULT_SYSTEM_DIR"
  else
    INSTALL_DIR="$DEFAULT_USER_DIR"
  fi
fi

mkdir -p "$INSTALL_DIR"

if [ ! -w "$INSTALL_DIR" ]; then
  USE_SUDO="true"
fi

echo "Detected platform: ${PLATFORM}"

ARCHIVE_URL="https://github.com/${REPO}/releases/latest/download/${ARCHIVE}"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

download_asset() {
  url="$1"
  dest="$2"
  curl -fsSL "$url" -o "$dest" 2>/dev/null
}

requires_bun_runtime() {
  return 1
}

detect_supported_runtime() {
  for runtime in node bun deno; do
    if command -v "$runtime" >/dev/null 2>&1; then
      echo "$runtime"
      return 0
    fi
  done
  return 1
}

ensure_supported_runtime_available() {
  JS_RUNTIME="$(detect_supported_runtime || true)"
  if [ -n "$JS_RUNTIME" ]; then
    return 0
  fi

  echo ""
  echo "Install failed: Nami needs one of these runtimes on PATH: node, bun, or deno."
  echo ""
  echo "Install one of the supported runtimes, then rerun this installer:"
  echo "  Node.js: https://nodejs.org"
  echo "  Bun:     https://bun.sh"
  echo "  Deno:    https://deno.com"
  echo ""
  echo "After installing a runtime, verify it with one of:"
  echo "  node --version"
  echo "  bun --version"
  echo "  deno --version"
  exit 1
}

install_binary() {
  src="$1"
  dest="$2"

  if [ "$USE_SUDO" = "false" ]; then
    install -m 755 "$src" "$dest"
  else
    sudo install -m 755 "$src" "$dest"
  fi
}

echo "Downloading release archive ${ARCHIVE}..."
if download_asset "$ARCHIVE_URL" "$TMPDIR/$ARCHIVE"; then
  tar -xzf "$TMPDIR/$ARCHIVE" -C "$TMPDIR"
  WRAPPER_SOURCE="$TMPDIR/${BINARY_NAME}-${PLATFORM}/${BINARY_NAME}"
  LAUNCHER_JS_SOURCE="$TMPDIR/${BINARY_NAME}-${PLATFORM}/${LAUNCHER_JS_NAME}"
  ENGINE_SOURCE="$TMPDIR/${BINARY_NAME}-${PLATFORM}/${ENGINE_NAME}"
else
  echo ""
  echo "Install failed: no release archive found for ${PLATFORM}."
  echo ""
  echo "Expected release asset:"
  echo "  ${ARCHIVE}"
  echo ""
  echo "This usually means the latest GitHub release has not been published for your platform yet."
  echo ""
  echo "If you already have a local build, install manually instead:"
  echo "  mkdir -p \"\$HOME/.local/bin\""
  echo "  install -m 755 nami \"\$HOME/.local/bin/nami\""
  echo "  install -m 755 nami.js \"\$HOME/.local/bin/nami.js\""
  echo "  install -m 755 nami-engine \"\$HOME/.local/bin/nami-engine\""
  echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
  exit 1
fi

for required in "$WRAPPER_SOURCE" "$LAUNCHER_JS_SOURCE" "$ENGINE_SOURCE"; do
  if [ ! -f "$required" ]; then
    echo ""
    echo "Install failed: release archive is missing required file: $required"
    exit 1
  fi
done

ensure_supported_runtime_available

# Install binaries
echo "Installing to ${INSTALL_DIR}..."
install_binary "$WRAPPER_SOURCE" "$INSTALL_DIR/$BINARY_NAME"
install_binary "$LAUNCHER_JS_SOURCE" "$INSTALL_DIR/$LAUNCHER_JS_NAME"
install_binary "$ENGINE_SOURCE" "$INSTALL_DIR/$ENGINE_NAME"

echo ""
echo "nami installed successfully!"
echo "Installed to: ${INSTALL_DIR}"
echo ""
echo "Verify installation:"
echo "  command -v nami"
echo "Detected JavaScript runtime: ${JS_RUNTIME}"

case ":$PATH:" in
  *":${INSTALL_DIR}:"*)
    ;;
  *)
    echo ""
    echo "${INSTALL_DIR} is not currently on your PATH."
    echo "Add this to your shell profile (~/.zshrc or ~/.bashrc):"
    echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    echo "Then open a new shell or run:"
    echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    ;;
esac

echo ""
echo "Set your API key and start:"
echo "  export ANTHROPIC_API_KEY=\"sk-ant-...\""
echo "  nami"
echo ""
echo "Or use a different provider:"
echo "  nami --model openai/gpt-4o"
echo "  nami --model ollama/gemma3"
