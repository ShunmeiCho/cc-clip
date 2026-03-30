#!/bin/bash
# Simulated cc-clip macOS demo output
export PS1='$ '

case "$1" in
  title)
    echo "# cc-clip: Paste images into remote Claude Code over SSH"
    ;;
  install)
    echo "cc-clip v0.4.0 installed to ~/.local/bin/cc-clip"
    ;;
  setup)
    sleep 0.1; echo "[1/4] Checking SSH config... done"
    sleep 0.1; echo "[2/4] Installing pngpaste... done"
    sleep 0.1; echo "[3/4] Starting daemon... done (port 18339)"
    sleep 0.1; echo "[4/4] Connecting to myserver..."
    sleep 0.1; echo "      [1/7] Checking local daemon... daemon running"
    sleep 0.1; echo "      [2/7] Detecting remote arch... linux/amd64"
    sleep 0.1; echo "      [3/7] Uploading binary... done (hash match, skipped)"
    sleep 0.1; echo "      [4/7] Installing xclip shim... done"
    sleep 0.1; echo "      [5/7] Writing token... done"
    sleep 0.1; echo "      [6/7] Verifying tunnel... reachable"
    sleep 0.1; echo "      [7/7] Testing clipboard... image/png (245 KB)"
    echo ""
    echo "Setup complete! Paste images from your local clipboard."
    ;;
  ssh)
    echo "# Press Ctrl+V with a screenshot on your Mac clipboard..."
    echo "# Image appears in Claude Code -- clipboard works over SSH!"
    ;;
esac
