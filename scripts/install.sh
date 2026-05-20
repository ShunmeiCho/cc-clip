#!/bin/sh
set -e

# cc-clip installer
# Usage: curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh | sh

REPO="ShunmeiCho/cc-clip"
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

verify_checksum() {
    local archive="$1" checksums="$2" archive_name="$3"
    local expected actual

    expected=$(awk -v name="$archive_name" '$2 == name {print $1; exit}' "$checksums")
    if [ -z "$expected" ]; then
        echo "Error: checksum for ${archive_name} not found in checksums.txt" >&2
        exit 1
    fi

    if command -v sha256sum >/dev/null 2>&1; then
        actual=$(sha256sum "$archive" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
        actual=$(shasum -a 256 "$archive" | awk '{print $1}')
    else
        echo "Error: sha256sum or shasum required for checksum verification" >&2
        exit 1
    fi

    if [ "$actual" != "$expected" ]; then
        echo "Error: checksum mismatch for ${archive_name}" >&2
        echo "  expected: ${expected}" >&2
        echo "  actual:   ${actual}" >&2
        exit 1
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
    CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"

    TMP_DIR=$(mktemp -d)
    trap 'rm -rf "$TMP_DIR"' EXIT

    echo "Downloading ${DOWNLOAD_URL}..."
    download "$DOWNLOAD_URL" "${TMP_DIR}/${ARCHIVE_NAME}"

    echo "Downloading checksums.txt..."
    download "$CHECKSUMS_URL" "${TMP_DIR}/checksums.txt"

    echo "Verifying checksum..."
    verify_checksum "${TMP_DIR}/${ARCHIVE_NAME}" "${TMP_DIR}/checksums.txt" "$ARCHIVE_NAME"

    echo "Extracting..."
    tar -xzf "${TMP_DIR}/${ARCHIVE_NAME}" -C "$TMP_DIR"

    mkdir -p "$INSTALL_DIR"
    cp "${TMP_DIR}/cc-clip" "${INSTALL_DIR}/cc-clip"
    chmod +x "${INSTALL_DIR}/cc-clip"

    # macOS Gatekeeper fix: downloaded binaries are blocked in two ways:
    # 1. com.apple.quarantine / com.apple.provenance extended attributes
    # 2. Missing or invalid code signature (Identifier=a.out)
    # Clear all xattrs and re-sign with proper identifier to satisfy Gatekeeper.
    if [ "$(uname -s)" = "Darwin" ]; then
        xattr -cr "${INSTALL_DIR}/cc-clip" 2>/dev/null || true
        codesign --force --sign - --identifier com.cc-clip.cli "${INSTALL_DIR}/cc-clip" 2>/dev/null || true
    fi

    echo ""
    echo "cc-clip ${VERSION} installed to ${INSTALL_DIR}/cc-clip"

    if ! echo "$PATH" | tr ':' '\n' | grep -q "^${INSTALL_DIR}$"; then
        echo ""
        echo "Add to your PATH:"
        echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    fi

    echo ""
    echo "Quick start:"
    echo "  cc-clip setup HOST        # One command: deps, daemon, deploy"
    echo "  Ctrl+V in remote Claude Code"
}

main
