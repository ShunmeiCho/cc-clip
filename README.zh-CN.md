<!-- i18n-source: README.md @ 7694090fe90162db9cb66e4a2087ce0b4fab8e7f -->

<p align="center">
  <a href="README.md">English</a> ·
  <b>简体中文</b> ·
  <a href="README.ja.md">日本語</a>
</p>

<p align="center">
  <img src="assets/readme/hero.svg" width="100%" alt="cc-clip 通过仅限回环地址的 SSH 隧道，将本地剪贴板传送给远程 AI 编程代理">
</p>

> 本文是英文原文的简体中文翻译。若内容有差异，以 [English 原文](README.md) 为准。翻译版本可能晚于英文主线更新。

<p align="center">
  <a href="https://github.com/ShunmeiCho/cc-clip/releases"><img src="https://img.shields.io/github/v/release/ShunmeiCho/cc-clip?color=F97316" alt="最新版本"></a>
  <a href="https://github.com/ShunmeiCho/cc-clip/actions/workflows/ci.yml"><img src="https://github.com/ShunmeiCho/cc-clip/actions/workflows/ci.yml/badge.svg" alt="CI 状态"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-18181B.svg" alt="MIT 许可证"></a>
</p>

<p align="center">
  <b>通过 SSH 将图片粘贴到远程 Claude Code、Codex CLI、opencode 和 Cursor 会话中——并把文本原样复制回来，不带终端软换行。</b><br>
  可选集成还能把任务完成和授权请求通知发送回桌面。
</p>

<p align="center">
  <a href="#快速开始">快速开始</a> ·
  <a href="#选择目标">选择目标</a> ·
  <a href="#工作原理">工作原理</a> ·
  <a href="#文档">文档</a>
</p>

<p align="center">
  <img src="docs/marketing/demo-quick.gif" alt="展示 cc-clip 安装、设置和远程图片粘贴的终端演示" width="720">
  <br>
  <em>安装 → 设置 → 打开 SSH → 粘贴。</em>
</p>

> **从 v0.8.x 升级？** 在 v0.9.0 中，`--codex` 改为仅配置 Codex。如果
> 同一台主机还需要 Claude 集成，请使用 `--all`。参见
> [升级指南](docs/upgrading.md#upgrading-from-v08x-to-v090)。

## 快速开始

这是稳定的 macOS 到 Linux 路径。需要：

- macOS 13 或更高版本；
- 一台 amd64 或 arm64 Linux 远程主机，并安装 `curl`、`bash`，以及 `xclip` 或 `wl-paste`；
- `~/.ssh/config` 中有一个命名的 `Host` 条目。

### 1. 安装

```bash
curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh | sh
cc-clip --version
```

如果安装程序提示，请先将 `~/.local/bin` 添加到 `PATH`，再继续。

### 2. 设置一台主机

```bash
cc-clip setup myserver
```

默认目标是 Claude Code。设置过程会检查本地依赖、添加仅绑定回环地址的
`RemoteForward`、启动本地守护进程并部署远程 shim。若要配置 Codex、
opencode 或通知，请使用下一节中的目标选项。

### 3. 打开新的 SSH 会话

```bash
ssh myserver
```

启动编程代理，然后照常粘贴。新的 SSH 连接很重要：
它会保持反向隧道打开。

### 4. 验证完整链路

将一张图片复制到 Mac 剪贴板，然后在本地运行：

```bash
cc-clip doctor --host myserver
```

## 选择目标

每次设置选择一个目标选项。不指定目标选项时，cc-clip 会配置 Claude Code。

| 远程工作流 | 设置命令 | 图片粘贴 | 桌面通知 | 额外要求 |
|---|---|:---:|:---:|---|
| Claude Code | `cc-clip setup myserver` | 是 | 是 | `xclip` 或 `wl-paste` |
| 仅 Codex CLI | `cc-clip setup myserver --codex` | 是 | 是 | Xvfb；设置过程可能需要远程 `sudo` |
| 所有集成 | `cc-clip setup myserver --all` | 是 | 是 | Codex 需要 Xvfb |
| opencode | `cc-clip setup myserver --opencode` | 是 | 是 | `xclip` 或 `wl-paste` |
| Antigravity | `cc-clip setup myserver --agy` | 否 | 是 | 仅通知集成 |
| Cursor CLI | `cc-clip setup myserver --cursor` | 是 | 否 | Cursor 所在 shell 中需已设置 `DISPLAY` 或 `WAYLAND_DISPLAY` |

对于 Codex 目标，cc-clip 会尝试使用 `apt` 或 `dnf` 安装 Xvfb。如果
无法使用免密码 `sudo`，它会停止并输出准确的安装命令；
手动运行该命令，然后重新执行设置。

Claude、opencode 和 Cursor 路径使用远程 `xclip` 或 `wl-paste` shim。Codex
直接读取 X11，因此其目标会额外配置 Xvfb 和 `cc-clip x11-bridge`。

Cursor 有一个部署无法代为满足的前提条件：只有当 Cursor 所在的 shell 中设置了
`DISPLAY` 或 `WAYLAND_DISPLAY` 时，它才会读取剪贴板（用 `echo $DISPLAY` 检查）。
请用 `ssh -X myserver` 连接，或导出一个已存在的显示——cc-clip 有意不凭空注入
一个：背后没有 X 服务器的 `DISPLAY` 会让该 shell 中所有其他工具的剪贴板回退
必然失败。此外 Cursor 约 4 秒后就会停止等待剪贴板辅助进程，因此在慢速链路上
传大图时，请在远程 shell rc 中加入 `export CC_CLIP_FETCH_TIMEOUT_MS=3000`。
Cursor 的通知集成尚未接入。

如果远程的 `cc-clip` 已由包管理器管理，可用 `cc-clip setup myserver
--use-remote-bin` 保留这种归属。设置过程会在你远程**登录 shell** 的 PATH 下
解析 `cc-clip`（因此能找到 `~/.nix-profile/bin`、pipx 和 asdf 安装的版本），
记录其版本与哈希，并照常完成全部集成配置，而不上传替代二进制。

该模式会记入主机的部署状态：之后的 `cc-clip connect` 运行——包括
`cc-clip update` 提示你执行的那条 `connect <host> --force`——无需再带此
标志即可继续使用包管理的二进制。用 `--local-bin` 部署可将主机切回上传
模式。同一次运行中该标志不能与 `--local-bin` 组合。

> opencode 和 Antigravity 的集成生成已有测试覆盖，但尚未在代表性主机上
> 对事件交付进行冒烟测试。请
> [报告测试结果](https://github.com/ShunmeiCho/cc-clip/issues)。

### 其他本地平台

| 本地机器 | 远程主机 | 支持级别 | 推荐路径 |
|---|---|---|---|
| macOS 13+ | Linux | 稳定 | `cc-clip setup HOST` |
| Windows 10/11 | Linux | 实验性 | [`send` / `hotkey` 快速开始](docs/windows-quickstart.md) |
| Linux | Linux | 手动运行守护进程 | 运行 `cc-clip serve`，然后在另一个 shell 中运行 `cc-clip setup HOST` |

Windows 支持仍处于实验阶段。请先使用 [Windows 快速开始](docs/windows-quickstart.md)
中的显式上传并粘贴工作流。另有一个可选的直接 RemoteForward 传输
（自 v0.9.1 起提供），但它不是默认方案。

## 工作原理

cc-clip 将传输范围限制在 SSH 连接的本地范围内：

```text
Image paste
  local clipboard
      → cc-clip daemon on 127.0.0.1:18339
      → SSH RemoteForward
      → remote xclip/wl-paste shim or Xvfb bridge
      → remote coding agent

Notifications
  remote hook / notify command / plugin
      → SSH tunnel
      → local cc-clip daemon
      → macOS Notification Center or cmux
```

1. 只有远程端请求时，本地守护进程才会读取剪贴板数据。
2. SSH 在远程回环地址上暴露该守护进程；不会创建公开监听端口。
3. Claude Code 和 opencode 通过透明的剪贴板 shim 访问它。
4. Codex 通过 Xvfb 剪贴板所有者访问它，因为 Codex 会直接读取 X11，
   而不是调用 `xclip`。
5. 无法识别的 `xclip` / `wl-paste` 调用会转交给真正的远程工具。

## 通知

剪贴板数据和代理事件共享 SSH 隧道，但使用独立的
认证材料。`cc-clip connect` 可以接入：

| 来源 | 集成方式 | 事件示例 |
|---|---|---|
| Claude Code | 托管 hook | 停止、授权请求、图片粘贴 |
| Codex CLI | `notify` 命令 | 任务完成 |
| opencode | 生成的 plugin | 会话空闲 |
| Antigravity | 生成的 plugin | 代理停止 |

有关适配器细节、手动配置、nonce 注册和诊断，请参见
[SSH 通知](docs/notifications.md)。

## 安全模型

| 边界 | 防护措施 |
|---|---|
| 网络 | 守护进程和转发端口仅绑定回环地址 |
| 剪贴板 | 使用 30 天滑动过期时间的 Bearer token |
| 通知 | 每次部署使用独立 nonce |
| 进程列表 | token 和 hook payload 不会放入命令行参数 |
| 回退 | 无关剪贴板调用会转交给真正的远程二进制文件 |

同一台远程主机上的用户共享回环网络。token 文件权限为
`0600`，但 cc-clip 无法防御以你的 Unix 账户身份运行或能够读取
你文件的其他进程。在共享或不受信任的主机上使用 cc-clip 前，
请阅读明确的[威胁模型](SECURITY.md)。

## 核心命令

| 命令 | 用途 |
|---|---|
| `cc-clip setup HOST [target]` | 首次配置依赖、SSH config、守护进程和部署 |
| `cc-clip setup HOST --use-remote-bin` | 配置远程二进制由包管理器管理的主机 |
| `cc-clip connect HOST --force [target]` | 修复或完整重新部署主机 |
| `cc-clip connect HOST --token-only` | 同步已轮换或过期的 token |
| `cc-clip doctor --host HOST` | 端到端诊断 |
| `some-command \| cc-clip copy`（在远程执行） | 把远程输出复制到本地剪贴板，绕过终端软换行 |
| `cc-clip status` | 查看本地组件状态 |
| `cc-clip hosts list` | 查看已知主机注册表 |
| `cc-clip update --check` | 检查发布渠道中的最新版本 |
| `cc-clip update` | 安装最新发布版本 |

运行 `cc-clip --help` 查看权威命令列表。
[命令指南](docs/commands.md)涵盖常用选项和环境变量。

### 配置

| 设置 | 默认值 | 环境变量 |
|---|---:|---|
| 隧道端口 | `18339` | `CC_CLIP_PORT` |
| token 有效期 | `30d` | `CC_CLIP_TOKEN_TTL` |
| 调试日志 | 关闭 | `CC_CLIP_DEBUG=1` |

## 故障排查

先运行内置诊断：

```bash
cc-clip doctor --host myserver
```

最常见的三个修复方法是：

- **隧道不可用：**保持一个新的 `ssh myserver` 会话打开。
  `RemoteForward` 仅在 SSH 连接持有它时存在。
- **守护进程重启后 token 被拒绝：**运行
  `cc-clip connect myserver --token-only`。
- **Codex 没有剪贴板：**打开新的 SSH 会话，以加载注入的 `DISPLAY`；
  如果缺少 Xvfb 或 x11-bridge，运行
  `cc-clip connect myserver --codex --force`（或 `--all --force`）。

如果新的 SSH 标签页报告 `remote port forwarding failed for listen port 18339`，
说明另一个活动或残留的 SSH 会话已经占用固定远程端口。使用仍可工作的会话、
关闭旧会话，或按照[故障排查指南](docs/troubleshooting.md)中的端口清理步骤操作。

## 不适合使用 cc-clip 的场景

如果有更简单的方案，请优先使用：

- 如果整个工作流已经在编辑器内，使用编辑器内置的远程剪贴板；
- 仅同步文本剪贴板时，使用 OSC 52；
- 很少传输图片，且不值得为保留粘贴行为运行守护进程和 SSH 转发时，使用 `scp`；
- 需要广泛的双向剪贴板同步，而不是针对代理的窄工作流时，使用通用剪贴板桥接器；
- 如果远程本地用户不能访问你的用户级回环隧道，请避免在不受信任的共享主机上使用 cc-clip。

## 文档

| 指南 | 内容 |
|---|---|
| [Windows 快速开始](docs/windows-quickstart.md) | Windows 上传、粘贴和热键工作流 |
| [升级](docs/upgrading.md) | 破坏性变更和特定版本迁移 |
| [命令](docs/commands.md) | 常用命令、选项和环境变量 |
| [通知](docs/notifications.md) | hook 和 plugin 集成 |
| [故障排查](docs/troubleshooting.md) | 按症状诊断 |
| [安全](SECURITY.md) | 威胁模型和信任边界 |

## 贡献

欢迎提交 bug 报告和范围明确的 pull request。对于较大的功能，请先创建
[issue](https://github.com/ShunmeiCho/cc-clip/issues)，以便讨论实现方案。

从源代码构建需要使用 `go.mod` 中声明的 Go 版本：

```bash
git clone https://github.com/ShunmeiCho/cc-clip.git
cd cc-clip
make build
make test
```

提交消息请遵循 [Conventional Commits](https://www.conventionalcommits.org/)
规范（`feat:`、`fix:`、`docs:` 等）。

## 许可证

[MIT](LICENSE)
