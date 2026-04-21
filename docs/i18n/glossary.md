# Translation Glossary (English / 简体中文 / 日本語)

This document fixes translations of core cc-clip terminology so every translated file uses consistent wording. If a term is missing or a row is ambiguous, propose an edit here *before* translating documentation that uses the term — that keeps all translated files aligned to the same dictionary.

| English | 简体中文 | 日本語 | Notes |
|---|---|---|---|
| clipboard | 剪贴板 | クリップボード | Standard translation in all three languages. |
| shim | shim / 兼容层 | shim / 互換レイヤー | Prefer the English word on first use, then 兼容层 / 互換レイヤー in follow-ups. |
| tunnel | 隧道 | トンネル | Refers to the SSH `RemoteForward` tunnel. |
| daemon | 守护进程 | デーモン | The local `cc-clip serve` process. |
| hotkey | 热键 | ホットキー | Windows global hotkey for remote paste. |
| RemoteForward | RemoteForward | RemoteForward | Do not translate — SSH config keyword. |
| x11-bridge | x11-bridge | x11-bridge | Do not translate — project component name. |
| Xvfb | Xvfb | Xvfb | Do not translate — external tool name. |
| setup | 初始化 / 设置 | セットアップ | `cc-clip setup` command itself stays in English; use 初始化/设置 when describing the action in prose. |
| connect | 部署 / 连接 | デプロイ / 接続 | `cc-clip connect` command itself stays in English. |
| session | 会话 | セッション | The SSH session or cc-clip session token. |
| nonce | nonce | nonce | Do not translate — security term; same spelling in all three. |
| bridge | 桥接器 | ブリッジ | Use for the broader pattern (notification bridge, clipboard bridge). |
| hook (Claude Code / shell) | hook / 钩子 | フック | Technical contract; prefer English word on first use. |
| SSH tunnel | SSH 隧道 | SSH トンネル | — |
| launchd service | launchd 服务 | launchd サービス | macOS-specific system service. |
| shim interception | shim 拦截 | shim によるインターセプト | Describes how cc-clip intercepts `xclip` / `wl-paste` calls. |

## Non-translated surface

The following stay in English in every translated file:

- All command names, flags, and file paths: `cc-clip setup --codex`, `~/.ssh/config`, `127.0.0.1:18339`, etc.
- Product names: Claude Code, Codex CLI, opencode, Ghostty, tmux, macOS, launchd, Debian/Ubuntu, RHEL/Fedora.
- Environment variables: `CC_CLIP_PORT`, `CC_CLIP_TOKEN_TTL`, etc.
- MIME types and protocol terms: `image/png`, `OSC 52`, `TARGETS`.
- GitHub links, issue numbers, release tags: `#27`, `v0.6.0`, `anomalyco/opencode#19294`.

## Style cross-reference

When in doubt about tone, pronoun usage, or section structure, consult [TRANSLATION_BRIEF.md](TRANSLATION_BRIEF.md).
