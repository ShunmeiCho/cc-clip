#!/bin/sh
set -e

# cc-clip installer
# Usage: curl -fsSL https://raw.githubusercontent.com/shunmei/cc-clip/main/scripts/install.sh | sh

REPO="shunmei/cc-clip"
INSTALL_DIR="${CC_CLIP_INSTALL_DIR:-$HOME/.local/bin}"

detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case "$ARCH" in
        x86_64|amd64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
    esac

    case "$OS" in
        darwin|linux) ;;
        *) echo "Unsupported OS: $OS (only macOS and Linux are supported)"; exit 1 ;;
    esac

    echo "${OS}_${ARCH}"
}

get_latest_version() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null | \
            grep '"tag_name"' | head -1 | cut -d'"' -f4
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null | \
            grep '"tag_name"' | head -1 | cut -d'"' -f4
    else
        echo "Error: curl or wget required" >&2
        exit 1
    fi
}

download() {
    local url="$1" dest="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" -o "$dest"
    else
        wget -qO "$dest" "$url"
    fi
}

main() {
    PLATFORM=$(detect_platform)
    VERSION=$(get_latest_version)

    if [ -z "$VERSION" ]; then
        echo "Error: could not determine latest version"
        exit 1
    fi

    echo "Installing cc-clip ${VERSION} for ${PLATFORM}..."

    ARCHIVE_NAME="cc-clip_${VERSION#v}_${PLATFORM}.tar.gz"
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE_NAME}"

    TMP_DIR=$(mktemp -d)
    trap 'rm -rf "$TMP_DIR"' EXIT

    echo "Downloading ${DOWNLOAD_URL}..."
    download "$DOWNLOAD_URL" "${TMP_DIR}/${ARCHIVE_NAME}"

    echo "Extracting..."
    tar -xzf "${TMP_DIR}/${ARCHIVE_NAME}" -C "$TMP_DIR"

    mkdir -p "$INSTALL_DIR"
    cp "${TMP_DIR}/cc-clip" "${INSTALL_DIR}/cc-clip"
    chmod +x "${INSTALL_DIR}/cc-clip"

    echo ""
    echo "cc-clip ${VERSION} installed to ${INSTALL_DIR}/cc-clip"

    if ! echo "$PATH" | tr ':' '\n' | grep -q "^${INSTALL_DIR}$"; then
        echo ""
        echo "Add to your PATH:"
        echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    fi

    echo ""
    echo "Quick start:"
    echo "  1. cc-clip serve          # Start local daemon"
    echo "  2. cc-clip connect HOST   # Deploy to remote server"
    echo "  3. Ctrl+V in remote Claude Code"
}

main
