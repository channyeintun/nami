#!/bin/sh
set -e

# gocode installer
# Usage: curl -fsSL https://raw.githubusercontent.com/channyeintun/gocode/main/gocode/install.sh | sh

REPO="channyeintun/gocode"
BINARY_NAME="gocode"
ENGINE_NAME="gocode-engine"

DEFAULT_SYSTEM_DIR="/usr/local/bin"
DEFAULT_USER_DIR="${HOME}/.local/bin"
INSTALL_DIR="${INSTALL_DIR:-}"
USE_SUDO="false"

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
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

PLATFORM="${OS}-${ARCH}"
BINARY_ASSET="${BINARY_NAME}-${PLATFORM}"
ENGINE_ASSET="${ENGINE_NAME}-${PLATFORM}"
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

BINARY_URL="https://github.com/${REPO}/releases/latest/download/${BINARY_ASSET}"
ENGINE_URL="https://github.com/${REPO}/releases/latest/download/${ENGINE_ASSET}"
ARCHIVE_URL="https://github.com/${REPO}/releases/latest/download/${ARCHIVE}"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

download_asset() {
  url="$1"
  dest="$2"
  curl -fsSL "$url" -o "$dest" 2>/dev/null
}

requires_bun_runtime() {
  first_line="$(LC_ALL=C sed -n '1p' "$1" 2>/dev/null || true)"
  [ "$first_line" = "#!/usr/bin/env bun" ]
}

ensure_bun_available() {
  if command -v bun >/dev/null 2>&1; then
    return 0
  fi

  echo ""
  echo "Install failed: this gocode release uses a Bun launcher, but 'bun' was not found on PATH."
  echo ""
  echo "Install Bun first, then rerun this installer:"
  echo "  https://bun.sh"
  echo ""
  echo "After Bun is installed, verify it with:"
  echo "  bun --version"
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

echo "Trying direct release binaries..."
if download_asset "$BINARY_URL" "$TMPDIR/$BINARY_ASSET" && \
  download_asset "$ENGINE_URL" "$TMPDIR/$ENGINE_ASSET"; then
  BINARY_SOURCE="$TMPDIR/$BINARY_ASSET"
  ENGINE_SOURCE="$TMPDIR/$ENGINE_ASSET"
else
  rm -f "$TMPDIR/$BINARY_ASSET" "$TMPDIR/$ENGINE_ASSET"
  echo "Direct release binaries not found; trying legacy archive ${ARCHIVE}..."
  if download_asset "$ARCHIVE_URL" "$TMPDIR/$ARCHIVE"; then
    tar -xzf "$TMPDIR/$ARCHIVE" -C "$TMPDIR"
    BINARY_SOURCE="$TMPDIR/${BINARY_NAME}-${PLATFORM}/${BINARY_NAME}"
    ENGINE_SOURCE="$TMPDIR/${BINARY_NAME}-${PLATFORM}/${ENGINE_NAME}"
  else
    echo ""
    echo "Install failed: no release assets found for ${PLATFORM}."
    echo ""
    echo "Expected one of these release asset sets:"
    echo "  ${BINARY_ASSET}"
    echo "  ${ENGINE_ASSET}"
    echo "or:"
    echo "  ${ARCHIVE}"
    echo ""
    echo "This usually means the latest GitHub release has not been published for your platform yet."
    echo ""
    echo "If you already have a local build, install manually instead:"
    echo "  mkdir -p \"\$HOME/.local/bin\""
    echo "  install -m 755 gocode \"\$HOME/.local/bin/gocode\""
    echo "  install -m 755 gocode-engine \"\$HOME/.local/bin/gocode-engine\""
    echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
    exit 1
  fi
fi

if requires_bun_runtime "$BINARY_SOURCE"; then
  ensure_bun_available
fi

# Install binaries
echo "Installing to ${INSTALL_DIR}..."
install_binary "$BINARY_SOURCE" "$INSTALL_DIR/$BINARY_NAME"
install_binary "$ENGINE_SOURCE" "$INSTALL_DIR/$ENGINE_NAME"

echo ""
echo "gocode installed successfully!"
echo "Installed to: ${INSTALL_DIR}"
echo ""
echo "Verify installation:"
echo "  command -v gocode"

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
if requires_bun_runtime "$BINARY_SOURCE"; then
  echo "  bun --version"
fi
echo "  gocode"
echo ""
echo "Or use a different provider:"
echo "  gocode --model openai/gpt-4o"
echo "  gocode --model ollama/gemma3"
