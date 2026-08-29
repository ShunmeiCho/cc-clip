I maintain cc-clip, which is external tooling rather than a Codex patch, but it
may be useful prior art for the env-gated / out-of-band reader direction here:

https://github.com/ShunmeiCho/cc-clip

For Codex CLI on a headless remote Linux host, cc-clip sets up Xvfb plus a small
X11 clipboard bridge. The bridge claims CLIPBOARD ownership, fetches image data
from the local machine through an SSH RemoteForward, and serves it to Codex via
the normal X11/arboard path. In other words, Codex still sees a regular X11
clipboard, but the image source is out-of-band.

That is not the same as `CODEX_CLIPBOARD_READER`. It is closer to the generic
reader escape hatch than to a built-in `wl-paste` fallback: Codex still calls
normal X11 clipboard APIs, while the bridge is responsible for crossing the
host/container/SSH boundary.

If useful, I am happy to share implementation details or failure cases from the
cc-clip side. If this is too product-specific for the issue, I can remove the
link and leave only the design note.
