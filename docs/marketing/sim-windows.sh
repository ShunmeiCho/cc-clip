#!/bin/bash
case "$1" in
  hotkey)
    echo "hotkey: registered Alt+Shift+V"
    echo "hotkey: auto-start enabled (launches at login)"
    echo "hotkey: tray icon active"
    echo ""
    echo "# 1. Copy a screenshot on Windows (Win+Shift+S)"
    echo "# 2. Switch to Windows Terminal with Claude Code SSH session"
    echo "# 3. Press Alt+Shift+V..."
    echo ""
    sleep 0.3
    echo "hotkey: Alt+Shift+V pressed"
    echo "hotkey: uploading clipboard image..."
    sleep 0.3
    echo "hotkey: uploaded -> /tmp/cc-clip/img_20260330_142355_a7f3.png (312 KB)"
    echo "hotkey: path pasted into terminal"
    echo ""
    echo "# Claude Code receives the file path and reads the image!"
    ;;
esac
