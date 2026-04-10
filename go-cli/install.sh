#!/bin/sh
set -e

# gocode installer
# Usage: curl -fsSL https://raw.githubusercontent.com/channyeintun/go-code/main/go-cli/install.sh | sh

REPO="channyeintun/go-code"
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

# Get latest release URL
LATEST_URL="https://github.com/${REPO}/releases/latest/download/${ARCHIVE}"
echo "Downloading ${LATEST_URL}..."

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

curl -fsSL "$LATEST_URL" -o "$TMPDIR/$ARCHIVE"
tar -xzf "$TMPDIR/$ARCHIVE" -C "$TMPDIR"

# Install binaries
echo "Installing to ${INSTALL_DIR}..."
if [ "$USE_SUDO" = "false" ]; then
  cp "$TMPDIR/${BINARY_NAME}-${PLATFORM}/${BINARY_NAME}" "$INSTALL_DIR/"
  cp "$TMPDIR/${BINARY_NAME}-${PLATFORM}/${ENGINE_NAME}" "$INSTALL_DIR/"
else
  sudo cp "$TMPDIR/${BINARY_NAME}-${PLATFORM}/${BINARY_NAME}" "$INSTALL_DIR/"
  sudo cp "$TMPDIR/${BINARY_NAME}-${PLATFORM}/${ENGINE_NAME}" "$INSTALL_DIR/"
fi

chmod +x "$INSTALL_DIR/$BINARY_NAME" "$INSTALL_DIR/$ENGINE_NAME"

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
echo "  gocode"
echo ""
echo "Or use a different provider:"
echo "  gocode --model openai/gpt-4o"
echo "  gocode --model ollama/gemma3"
