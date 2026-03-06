---
name: Bug Report
about: Report a problem with cc-clip
title: ''
labels: bug
assignees: ''
---

**cc-clip version**
<!-- Output of: cc-clip version -->

**Environment**
- Local OS: <!-- e.g., macOS 15.2 arm64 -->
- Remote OS: <!-- e.g., Ubuntu 22.04 amd64 -->
- SSH client: <!-- e.g., OpenSSH 9.6 -->
- Terminal: <!-- e.g., iTerm2, Ghostty, Terminal.app -->

**Describe the bug**
<!-- A clear description of what's happening -->

**Steps to reproduce**
1.
2.
3.

**Expected behavior**
<!-- What you expected to happen -->

**Diagnostic output**
<!-- Run these and paste the output (redact sensitive info): -->

```bash
# Local
cc-clip doctor

# Remote (if applicable)
cc-clip doctor --host <your-host>

# Shim debug
CC_CLIP_DEBUG=1 xclip -selection clipboard -t TARGETS -o
```

**Additional context**
<!-- Any other relevant info, screenshots, etc. -->
