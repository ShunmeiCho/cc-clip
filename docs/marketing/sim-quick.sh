#!/bin/bash
case "$1" in
  install)
    echo "cc-clip v0.4.0 installed to ~/.local/bin/cc-clip"
    ;;
  setup)
    echo "[1/4] Checking SSH config... done"
    echo "[2/4] Installing pngpaste... done"
    echo "[3/4] Starting daemon... done (port 18339)"
    echo "[4/4] Connecting to myserver... done"
    echo ""
    echo "Setup complete! Paste images from your local clipboard."
    echo ""
    echo "# Copy image on Mac/Windows -> Ctrl+V in remote Claude Code"
    echo "# Clipboard works over SSH now."
    ;;
esac
