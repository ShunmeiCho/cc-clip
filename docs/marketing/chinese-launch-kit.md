# cc-clip 中文宣发包

这份文档用于中文技术社区、朋友圈/微信群、私信请教和中文 X 动态。

原则：少一点“发布了一个大项目”，多一点“我把自己遇到的一个具体问题整理成工具，想请真实用户帮忙看看边界”。中文语境里，过度包装和连续刷屏更容易伤关系。

## 核心说法

一句话：

> cc-clip 帮你把本地截图粘贴到 SSH 远程的 Claude Code / Codex CLI / opencode 会话里。

更口语一点：

> 我做了一个小工具，尝试处理“截图在本地，AI coding agent 跑在远程服务器上，图片粘不过去”的问题。

必须带上的边界：

- 这是 `v0.9.0-beta.1`，是 opt-in prerelease。
- 主要验证路径是 macOS -> Linux 远程主机。
- Windows 是实验性 `send` / `hotkey` 路径。
- Codex CLI 需要 Xvfb + x11-bridge，远程可能需要 `sudo` 或手动装 Xvfb。
- Antigravity 目前只是通知桥，图片粘贴还没做。
- 这是独立开源工具，不是 Anthropic / OpenAI / opencode / Google 的官方集成。

## 不要这样说

- “终于完美解决远程剪贴板。”
- “所有 SSH 场景都能用。”
- “一条命令无脑搞定。”
- “支持 Antigravity。”
- “替代 X11 forwarding。”
- “求 star 求转发。”

更稳妥的说法：

- “如果你刚好也在用 SSH 远程跑 AI coding agent，可能有用。”
- “这还是 beta，我更想先收集真实环境里的失败样本。”
- “如果你本来很少粘图片，手动 `scp` 可能更简单。”

## 中文 X / 即刻式短帖

```text
我发布了 cc-clip v0.9.0-beta.1。

它面向的是一个很具体的远程开发问题：
本地截图在 Mac 上，Claude Code / Codex CLI / opencode 跑在 SSH 远程 Linux 上，图片不好直接粘过去。

这还是 beta，不想夸大。主要想收集真实远程环境里的反馈和失败样本。

https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1
```

## 朋友圈 / 微信群版本

适合发给认识你、知道你在折腾远程 agent 工作流的人。

```text
最近把自己远程开发里一个反复遇到的小痛点整理成了开源工具：cc-clip。

场景很具体：截图在本地 Mac，Claude Code / Codex / opencode 跑在 SSH 远程 Linux 上，想把截图直接粘进 agent 会话里，正常剪贴板路径经常断掉。

现在做了一个 beta：本地剪贴板 daemon + SSH RemoteForward + 远程 xclip/wl-paste shim；Codex 另外走 Xvfb + X11 bridge。

不是大而全的剪贴板同步，也还不是稳定版。主要想找同样用远程 AI coding 工作流的朋友帮忙看看：
- 哪些 SSH / tmux / shell 启动环境会翻车
- Codex headless 环境是否好用
- 文档哪里讲得不清楚

项目在这里：
https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1
```

## 私信请教版本

适合发给你尊重的同行、维护者、重度远程开发用户。不要群发，最好每条都加一句你为什么想到他。

```text
打扰一下。我最近在做一个很窄的开源工具 cc-clip，想请你帮忙看一下方向是否靠谱。

它面向的是 SSH 远程 AI coding 里的图片粘贴问题：截图在本地 Mac，Claude Code / Codex / opencode 跑在远程 Linux 上，普通 Ctrl+V 拿不到本地图片。

现在是 v0.9.0-beta.1，主要路径是 macOS -> Linux；Windows 还实验性，Antigravity 目前只是通知，不是图片粘贴。

如果你刚好有类似远程工作流，我想请教两个问题：
1. 这个问题在你的工作流里是否真实存在？
2. 现在 README / release notes 里的边界有没有哪里看起来像过度承诺？

不用特意安装，能帮我看一眼定位和边界也很有价值：
https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1
```

## 中文技术社区长帖

仅在社区规则允许开源项目分享时使用。标题不要太营销。

标题备选：

- `做了一个 SSH 远程 AI coding 的图片粘贴 beta 工具`
- `cc-clip: 把本地截图粘到远程 Claude Code / Codex 会话里`
- `远程跑 coding agent 时，本地截图怎么粘过去？我做了一个 beta 工具`

正文：

```text
我最近把自己工作流里的一个小痛点做成了开源工具：cc-clip。

问题很具体：

本地在 Mac 上截图，开发环境在远程 Linux 服务器上，通过 SSH 跑 Claude Code / Codex CLI / opencode。文字交互没问题，但想把截图、UI bug、报错弹窗、设计稿片段直接粘进 agent 会话时，远程进程看不到本地图片剪贴板。

cc-clip 的思路不是做通用剪贴板同步，而是只处理这个远程 AI coding 场景：

- 本地 daemon 读取图片剪贴板
- SSH RemoteForward 把请求转到本地
- Claude Code / opencode 走远程 xclip / wl-paste shim
- Codex CLI 走 Xvfb + X11 bridge
- 通知也可以通过同一个 tunnel 回到本地

当前版本是 v0.9.0-beta.1。它不是稳定版，我也不想把话说满。

已知边界：

- 主要验证路径是 macOS -> Linux
- Windows 是实验性 send / hotkey 工作流
- Codex 远程需要 Xvfb，可能需要 sudo 或手动安装
- Antigravity 目前只做通知，不支持图片粘贴

如果你也长期 SSH 到远程机器跑 AI coding agent，我想收集这几类反馈：

- setup / connect 在你的 SSH 配置下哪里不顺
- Codex headless 场景是否能跑通
- opencode 的 xclip / wl-paste 路径是否符合预期
- 文档有没有哪里让人误解为“全平台稳定支持”

项目地址：
https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1

如果这个场景你根本不需要，也很正常。这个工具就是为比较窄的远程工作流做的。
```

## 中文回复模板

有人说“这个太复杂了”：

```text
是的，这个工具不是每个人都需要。它主要适合经常在 SSH 远程 agent 会话里粘截图的人。如果只是偶尔传一张图，手动 `scp` 或保存文件路径可能更简单。
```

有人问“为什么不用 X11 forwarding”：

```text
如果你现有 X11 forwarding 已经稳定好用，那我也建议继续用。cc-clip 想覆盖的是更窄的路径：不转发完整图形会话，只把图片粘贴和 agent 通知这两个点打通。
```

有人指出边界写太大：

```text
你说得对，这个表述需要收窄。当前比较准确的边界是：macOS -> Linux 是主要路径，Windows 仍是实验性，Antigravity 目前只有通知没有图片粘贴。我会把文案改得更清楚。
```

有人反馈失败：

```text
谢谢你试。这个 beta 本来就是想找这些真实环境里的失败样本。方便的话发一下本地 OS、远程 OS、用的是 Claude/Codex/opencode 哪个目标、运行的 setup 命令，以及 `cc-clip doctor --host <host>` 输出；敏感路径和主机名可以打码。
```

有人推荐别的工具：

```text
谢谢，确实这个方向有几个不同工具在探索。远程剪贴板问题很依赖终端、SSH、主机 OS 和具体 agent。我会去看一下它的设计，也尽量把 cc-clip 适合和不适合的场景写清楚。
```

## 中文发布顺序建议

1. 先发一条中文 X / 即刻式短帖，语气轻一点。
2. 只私信 2-3 个真正懂远程工作流、愿意给直话的人。
3. 不要当天连续发多个中文社区。
4. 如果有人指出边界问题，先改文档，不急着继续发。
5. 如果有人愿意试用，优先跟进失败报告，不要追问 star。

## 暂停条件

看到这些情况就先停：

- 两个人以上卡在同一个安装步骤。
- 有人指出你把 Windows / Antigravity / Codex 支持讲大了。
- 社区反馈“像广告”。
- 你没有时间当天继续回复。

中文宣发里，克制比覆盖面更重要。
