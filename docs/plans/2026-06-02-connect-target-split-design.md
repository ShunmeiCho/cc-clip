# connect/setup 多 target 拆分 + PluginAdapter + InstallSourceChain — 设计与实现计划 (v0.9.0 breaking)

> **范围**：设计级实现计划，先出方案、暂不改产品代码。逐行 TDD 测试代码于实现阶段补齐。
> **状态**：已并入一轮对抗式源码核验 + 一致性批判的收口（critic 原判 readyForDoc=false 的 2 矛盾 + 关键缺口已在本稿解决）。

**Goal:** 把 `connect`/`setup` 从"基础设施 + Claude 隐式耦合 + 单 `codex bool`"重构为三轴正交模型——clipboard transport / integration adapter / install source——提供 `--claude/--codex/--opencode/--antigravity/--all` + 交互菜单。

**Tech Stack:** Go 1.26；标准库 `os`/`flag`；TTY 判定用 `os.Stdin.Stat()` 的 `ModeCharDevice`（不引入 `golang.org/x/term`）。

---

## 0. 已验证事实（源码 + 本机 CLI 实测，作为设计地面真值）

| 事项 | 真值 | 出处 |
|---|---|---|
| 状态 schema | `NotifyDeployState`(`deploy.go:22-27`)=`{Enabled,HookInstalled,CodexInjected,HealthVerified}` 扁平 bool；`CodexDeployState`(`:15-19`)=`{Enabled,Mode,DisplayFixed}` 仅 X11。**`CodexInjected` 在 Notify，不在 Codex**。无 per-adapter 状态 | `internal/shim/deploy.go` |
| setup 入口 | `cmdSetup`(`main.go:1250`)→`runConnect(connectOpts{codex:hasFlag("codex")})`(`:1322`)。setup 与 connect 共用 runConnect | `cmd/cc-clip/main.go` |
| token-only | 早返回于 `main.go:793`，仅同步 token/session/nonce + 验 tunnel；**不**装 shim/hook/config。`--no-hooks/--hooks` 与 token-only 互斥(`:617`) | `cmd/cc-clip/main.go` |
| DeliveryChain | `deliver.go:18-48`：任意非 context error 即 fallthrough；仅 context 取消短路。**无** consent/policy 概念 | `internal/daemon/deliver.go` |
| Claude CLI | 2.1.160。`claude plugin install <plugin>@<mkt>`（`--scope`/`--config`，**无**版本 flag）；`claude plugin marketplace add <src>`（`--scope`/`--sparse`） | 本机实测 |
| Codex CLI | 0.135.0。**`codex plugin add <plugin>@<mkt>`**（`codex plugin install` 报 "unrecognized subcommand"）；`codex plugin marketplace add <src>` | 本机实测 |
| Antigravity CLI | `agy` 1.0.4（曾见 1.0.0，几天即漂）。`agy plugin install <dir>` / `agy plugin validate <dir>` **吃位置路径**（含 `plugin.json`）；config 目录 `~/.gemini/antigravity-cli/`，`plugins/` 首次安装才建 | 本机实测 |
| 版本钉死 | **按 CLI 分流**：Codex `plugin marketplace add` **有 `--ref <REF>`** 且 `<SOURCE>` 支持 `owner/repo[@ref]`/`--sparse`；Claude `plugin marketplace add` **无 `--ref`**（仅 `--scope`/`--sparse`），靠 git 源 URL 的 ref 钉。两者 install 阶段（`codex plugin add`/`claude plugin install`）均无版本 flag | 本机实测 |
| marketplace 概念 | 当前 Go 源码**完全不存在** marketplace/`--all`/`--opencode`/`--yes`/`parseDeployTargets`——全为净新增 | grep 全仓库 |

## 1. 破坏式变更 (v0.9.0)

`--codex` 从"全套 + Codex"超集改为**仅 Codex**（X11 transport + codex-notify）。旧行为用 `--all`。`--codex` **不卸载**已有 shim（只增不减；移除走 `uninstall`）。**connect 与 setup 同步破坏**（共用 `parseDeployTargets`）。

## 2. 三轴模型

```
轴 A clipboard transport（怎么读剪贴板）
   shim   → Claude / opencode / 任意 xclip 消费者
   x11    → Codex（arboard 进程内读 X11）
   media-file (SPECULATIVE) → Antigravity 候选之一，未定（见 §9）

轴 B integration adapter（通知，cc-clip 管生命周期；CLI 配置只是启动器）
   claude-notify / codex-notify / opencode-notify(步骤4) / antigravity-notify

轴 C install source（每 adapter 的安装来源，InstallSourceChain 按失败分类决策）
   marketplace(git源钉版, consent-gated) → bundled(cc-clip 上传) → config(legacy 直写)
```

## 命名决策（Antigravity，v0.9.0 锁定）

用户可见 flag 一律 = CLI 二进制名（与 `--claude`→`claude`、`--codex`→`codex`、`--opencode`→`opencode` 一致）。Antigravity 二进制是 `agy`，因此：

- **canonical flag = `--agy`**；`--antigravity` 作为 **alias** 接受（可发现性）。
- **adapter 名对齐 = `agy-notify`**（与 claude-notify/codex-notify/opencode-notify 同规律）。
- 内部 Go 字段保留 `DeployTargets.Antigravity`（描述性、非用户可见，无需缩写）。
- 交互菜单项显示 `agy (Antigravity)`。
- 真实路径 `~/.gemini/antigravity-cli/` 是 Antigravity 自带，**不改**。

> 本文档下文凡出现 `--antigravity` / `antigravity-notify`，按本决策读作 **`--agy`（alias `--antigravity`）** / **`agy-notify`**。Step 5/6 实现时以此为准并同步全文。

## 3. 命令语义

| 命令 | transport | adapter (v0.9.0) | 备注 |
|---|---|---|---|
| `--claude` | shim | claude-notify | |
| `--codex` | x11 | codex-notify | breaking：不再含 shim |
| `--opencode` | shim | **无** | 仅 shim；不碰 `~/.claude/settings.json`、`~/.codex/config.toml`；opencode-notify 步骤 4 引入 |
| `--antigravity` | **pending(probe)** | antigravity-notify | notify 可先做（bundled plugin）；clipboard 待远端 strace |
| `--all` | shim + x11 (+antigravity-notify) | claude-notify + codex-notify + antigravity-notify | **不含** antigravity clipboard（未定性前） |
| 无参 (TTY) | 菜单 | | connect 默认未触发；菜单见 §5 |
| 无参 (非 TTY) | connect=shim / setup=shim | | connect→claude+告警；setup→claude+告警（v0.9.0：与 connect 一致回退 {Claude}，绝不静默 all/sudo） |
| 修饰：`--token-only`/`--no-hooks`/`--hooks`/`--no-notify`/`--yes`/`--no-plugin-marketplace`/`--force` | | | 见 §6 矩阵 |

## 4. 目标解析（含判别器，connect/setup 共用）

```go
type DeployTargets struct{ Claude, Codex, Opencode, Antigravity bool }
func (t DeployTargets) Any() bool { return t.Claude||t.Codex||t.Opencode||t.Antigravity }

// 返回 explicit=false 表示"无显式 target flag"，由各调用方套自身默认。
// 解析阶段不做 SSH/IO，fail-fast。互斥冲突 -> exit 2（共用消息）。
func parseDeployTargets(args []string) (t DeployTargets, explicit bool, err error)
```

- **connect 默认**（explicit=false 且非菜单）：`{Claude}`（向后兼容）。
- **setup 默认**（explicit=false）：`{Claude}`（与 README 既有契约一致：`setup <host>` 是 Claude/opencode shim 路径、**不需 sudo**；仅 `--codex` 才引入 Xvfb/sudo——`README.md:159/160/166`）。**全装走 `--all`**（显式 opt-in），TTY 可弹菜单。绝不让普通 `setup` 静默触发 Xvfb/sudo/插件安装/consent gate/Antigravity hook。
- 多 target flag 并存（如 `--codex --all`）→ exit 2。

## 5. 交互菜单（TTY 无显式 target，connect/setup 共用）

须在 `[1]` daemon 探测前弹出（早于 SSH passphrase）：

```
Select deployment target:
  1) claude       clipboard shim + claude-notify
  2) codex        X11 bridge + codex-notify
  3) opencode     clipboard shim only (no Claude/Codex config changes)
  4) antigravity  antigravity-notify plugin (clipboard transport pending)
  5) all          everything above (antigravity clipboard excluded until resolved)
>
```

非 TTY：`os.Stdin.Stat()&os.ModeCharDevice==0` → connect 回退 `{Claude}`、setup 回退 `{Claude}`（v0.9.0 锁定：无人值守默认最小副作用，绝不静默 all/sudo；全装须显式 `--all`），stderr 告警，不阻塞。

## 6. Flag 交互矩阵

Legend: ALLOWED / ERROR(exit 2) / NO-OP(warn)

| 修饰 \ target | claude/none | codex | opencode | antigravity | all |
|---|---|---|---|---|---|
| `--token-only` | ALLOWED 仅同步凭据 | ALLOWED | ALLOWED | ALLOWED | ALLOWED 任何 adapter 都不装 |
| `--no-hooks`/`--hooks` | ALLOWED | **ERROR** Claude-only | **ERROR** | **ERROR** | ALLOWED 作用于 Claude 子项 |
| `--no-notify` | ALLOWED 装 transport 跳 notify | ALLOWED | ALLOWED | ALLOWED | ALLOWED 跳所有 notify，不跳 transport |
| `--yes` | ALLOWED（有安装才生效） | ALLOWED | ALLOWED | ALLOWED | ALLOWED |
| `--no-plugin-marketplace` | ALLOWED | ALLOWED | ALLOWED | ALLOWED | ALLOWED |
| `--force` | ALLOWED | ALLOWED | ALLOWED | ALLOWED | ALLOWED |

**token-only 高优先级覆盖**（沿用 `main.go:601/617` 既有互斥）：`--token-only` + `--no-hooks/--hooks` → ERROR；`+--force` → ERROR（全量重部署与"仅同步凭据"矛盾）；`+--auto-recover` → ERROR；`+--no-notify/--yes/--no-plugin-marketplace` → NO-OP(warn)。

规则要点：
- (a) token-only 不装任何 adapter、不写 marketplace/config（**权威在 transport 层早返回 `main.go:793`**，见 §8）。
- (b) `--no-hooks/--hooks` 是 Claude 专属，非 Claude target **ERROR 而非静默**（失败要响）。
- (c) `--no-notify` 只跳 notify adapter，**绝不**跳 clipboard transport。
- (d) `--yes`/`--no-plugin-marketplace` 仅在会安装 adapter 时有意义，否则 NO-OP(warn)。

## 7. PluginAdapter 抽象 + 每 adapter I/O 契约（净新增 `internal/plugin/`）

`cc-clip plugin run <name>`（main.go switch 加 `case "plugin"`）作为统一 runner 入口，但**每 adapter 的 stdin/stdout/exit 契约不同**：

| adapter | 触发入口 | runner I/O 契约 |
|---|---|---|
| claude-notify | `~/.claude/settings.json` hooks → `cc-clip-hook`（或直接 runner） | hook JSON in；**exit 0，fire-and-forget**，无 stdout 要求（CLAUDE.md 现有原则） |
| codex-notify | `~/.codex/config.toml` `notify=["cc-clip","notify",...]` | codex stdin；exit 0 |
| antigravity-notify | `agy` Stop hook（plugin `hooks.json`） | hook JSON in；**必须 stdout 吐合法放行 JSON 如 `{"decision":""}`**，否则可能阻止 agy 停止——与 fire-and-forget 相反 |
| opencode-notify（步骤4） | `~/.opencode/plugins/cc-clip-notify.js` 真 plugin 事件回调 | JS 反向调 cc-clip |

```go
type PluginAdapter interface {
    Name() AdapterID
    Install(s RemoteExecutor, req InstallRequest) install.InstallOutcome // 走 InstallSourceChain
    Verify(s RemoteExecutor) error
    Uninstall(s RemoteExecutor) error
}
```

## 8. token-only 不变量（解决批判矛盾 #2）

**权威层 = transport/编排层早返回**：`runConnect` 在 `main.go:793` 于装任何 adapter 或构造 InstallSourceChain **之前**返回。因此：
- token-only 下 **InstallSourceChain 根本不可达**，它**不**实现 token-only gate（避免双重真相）。
- 文档显式声明此不变量："token-only short-circuits before the install layer; the install chain is unreachable under token-only and MUST NOT re-implement the gate."
- 未来若有调用方在早返回之外触达 chain，是该调用方的 bug，不靠 chain 兜底。

## 9. InstallSourceChain（独立于 DeliveryChain，解决批判矛盾 #1）

净新增 `internal/install/`，**不复用** `daemon.DeliveryChain`（后者任意 error 即 fallthrough，无 consent 概念）。

**规范 AdapterSource 类型（单一来源，state 与 chain 共用——杜绝重复定义）：**
```go
// internal/install/source.go — 唯一定义，internal/shim/deploy.go 导入复用
type AdapterSource string
const (
    SourceMarketplace AdapterSource = "marketplace" // 远端 git 源下载，需 consent
    SourceBundled     AdapterSource = "bundled"     // cc-clip 上传，本地无网
    SourceConfig      AdapterSource = "config"      // legacy 直写 settings/config
)
```

**失败分类法 → 动作表**（chain 据 `FailureClass` 决策，非"任意 error 即 fallback"）：

| FailureClass | 动作 | 严格规则 |
|---|---|---|
| UserRefused | FALL_BACK | **仅可降级到 `IsLocal && !RequiresNetwork` 的源**；否则 HARD_STOP（绝不静默换远端下载） |
| PolicyForbidden | FALL_BACK | 同上 |
| NetworkFailure | FALL_BACK | 无限制降级（不回退到已失败的远端源） |
| CLIUnsupported | FALL_BACK | 无限制降级（探测到 CLI 不支持） |
| NotFound | FALL_BACK | 无限制降级 |
| VerifyFailed | **HARD_STOP** | 不降级（信任失败，降级可能装入未验证工件） |
| 成功 | 停止 | 首成功即停（唯一与 DeliveryChain 重合处） |

**consent gate × flags：**
- `--no-plugin-marketplace`（composition 级，最强）：从链中**删除** marketplace 源，链从 bundled 起，结构上永不远端下载。
- `--yes`（gate 级）：预批准 consent gate（不弹窗），但**不抑制** PolicyForbidden。
- 默认（无 flag、TTY）：gate 拒绝 → UserRefused → 静默降级 bundled（本地无网，允许）；bundled/config 都没注册才 HARD_STOP。
- HARD_STOP 需新 exitcode（建议 `14` install-refused/blocked；按 CLAUDE.md "Adding a new exit code" 改 `exitcode.go` + `classifyError` + shim 模板）。**值待确认**。

**版本钉死（按 CLI 分流的 source builder）**：源指向 `ShunmeiCho/cc-clip-plugins` 的特定 git ref。**Codex**：`codex plugin marketplace add <owner/repo>@<ref>`，或 `--ref <REF>`（实测支持）。**Claude**：`claude plugin marketplace add` 无 `--ref`，靠 git 源 URL 的 ref/tag（或 `--sparse`）钉。两者 install 阶段（`codex plugin add`/`claude plugin install`）均无版本 flag。`AdapterState.Version` 记录安装后从 plugin 元数据/`plugin list` 解析所得。

## 10. marketplace 命令模板（本机实测，含不存在性标注）

```
Claude (2.1.160):
  claude plugin marketplace add <git-url|owner/repo|path>   [--scope user|project|local] [--sparse <paths...>]
  claude plugin install <plugin>@<marketplace>              [--scope ...] [--config k=v]
  # 无版本 flag

Codex (0.135.0):
  codex plugin marketplace add <owner/repo[@ref]|git-url|local-path>   [--ref <REF>] [--sparse <path>]
  codex plugin add <plugin>@<marketplace>                   (或 <plugin> --marketplace <mkt>)
  # 注意：codex plugin install 不存在（报 "unrecognized subcommand 'install'"）
  # 版本钉死：marketplace add 用 --ref 或源 owner/repo@ref（实测支持）

Antigravity (agy 1.0.x):
  agy plugin validate <dir-with-plugin.json>
  agy plugin install <dir-with-plugin.json>                 # bundled-first，位置路径
  # marketplace (plugin@marketplace) 作后续源，非首发

不存在性（写进文档防回退）：
  - codex plugin install ✗（用 codex plugin add）   - Claude/agy marketplace add 无 --ref（靠源 ref/tag）；Codex marketplace add 有 --ref ✓   - install 阶段均无版本 flag
```

## 11. DeployState 重设计（per-adapter，向后兼容迁移）

`internal/shim/deploy.go`：保留 transport 状态（`ShimInstalled/ShimTarget/PathFixed/Codex.*`）原样；通知状态从扁平 bool 改 per-adapter map：

```go
type AdapterState struct {
    Installed bool                 `json:"installed"`
    Source    install.AdapterSource `json:"source,omitempty"` // 复用 §9 规范类型
    Version   string               `json:"version,omitempty"` // 安装后解析所得，非 flag
    Verified  bool                 `json:"verified"`
    LastError string               `json:"last_error,omitempty"`
}
type NotifyDeployState struct {
    Enabled  bool                          `json:"enabled"`
    Adapters map[AdapterID]*AdapterState   `json:"adapters,omitempty"`
    // 旧字段改 *bool + omitempty，保证 v0.8.x JSON 仍可反序列化
    HookInstalled  *bool `json:"hook_installed,omitempty"`
    CodexInjected  *bool `json:"codex_injected,omitempty"`
    HealthVerified *bool `json:"health_verified,omitempty"`
}
```

- **读时迁移** `migrateNotifyState`（在 `ReadRemoteState` unmarshal 后调用）：`HookInstalled→claude-notify`、`CodexInjected→codex-notify`（Source=config）；清空旧指针使下次 Write 只持久化新 schema；`Adapters!=nil` 守卫幂等。
- per-adapter 谓词：`NeedsAdapterInstall/NeedsAdapterVerify/AdapterInstalled`；`NeedsNotifySetup` 保留为 master-switch shim 减少 call-site 改动。
- **opencode-notify source 未定**：v0.9.0 不装（`--opencode`=仅 shim），Source 值留步骤 4 决定。
- **状态是 cache、`Verify()` 是权威（关键原则，来自复核）**：`WriteRemoteState` 失败仅 warning（`main.go:938`）、doctor 仅报告缺失（`doctor/remote.go:198`），故远端 `deploy.json` 可能缺失/陈旧。per-adapter 真实状态**必须由 `Verify()` 检查远端实际 plugin/config 文件**得出；state 缺失时**不得**盲目反复 marketplace 安装——先 `Verify()` 再决定是否装。

## 12. setup 入口收口

- setup 与 connect **共用 `parseDeployTargets`**（含 explicit 判别器），暴露同样 `--claude/--codex/--opencode/--antigravity/--all` + 同一菜单。
- setup 默认 `{Claude}`（与 README no-sudo 契约一致），connect 默认 `{Claude}`——靠判别器各套各自默认；**全装两者都靠显式 `--all`**。
- `setup --codex` 破坏式收窄为 codex-only：单 legacy target flag 且无 `--all` 时，[1/4] 前打印一次性 stderr 提示"v0.9.0: setup --codex 仅装 Codex，全套用 --all"。
- setup 步骤 [1/4]~[3/4]（deps/SSH config/daemon）与 target 无关；仅 [4/4] runConnect 消费 target set。
- usage `main.go:126-130,152-156` 更新。

## 13. Antigravity 第 5 target（独立）

```text
--agy  (alias --antigravity)
  notify: agy-notify
     staged bundle (temp source): ~/.cache/cc-clip/agy-plugin-src.XXXXXX/cc-clip-notify/  (plugin.json + hooks/hooks.json)
     install (bundled-first, temp-src): agy plugin validate <src> -> agy plugin install <src>  (agy copies into its managed plugins dir)
     hook: Stop ; runner = cc-clip plugin run agy-notify  (RemoteHasAgy via `command -v agy`; Verified=false until real hook-fire smoke)
     runner 契约: 必须 stdout 吐合法放行 JSON (如 {"decision":""}), 否则阻止 agy 停止
     marketplace 也可用（agy plugin install plugin@marketplace），但命令面不稳定（agy plugin install --help 把 --help 当路径）→ v0.9 坚持 bundled-first + 仅显式 --antigravity，不进全量默认
  clipboard: UNRESOLVED — 三假设待远端 Linux strace 定夺:
     H1 paste 时 exec xclip/wl-paste -> 复用 shim
     H2 进程内 X11               -> 复用/扩展 Codex x11-bridge
     H3 media-file ingestion (SPECULATIVE, PUSH 模型) -> 见下
  do not assume shim/X11/media-file until remote Linux evidence confirms
```

**media-file 的深层问题（不可低估）**：本机实测 `brain/<uuid>/uploaded_media_*.png` 是 agy ingestion 的 **OUTPUT 工件**（按 conversation UUID 命名 + `.tempmediaStorage`），**非可写入的 INPUT 监视目录**。cc-clip 既不知当前 UUID，agy 也不 watch 该目录。且 cc-clip 现有两 transport 都是**按需 PULL**，media-file 会是 **PUSH** 且**缺触发器**（看不到 paste 击键）——故 Antigravity clipboard 可能需**不同 UX**（显式命令或 agy hook 驱动 import），这是真正开放问题。

**取证（P0，远端 Linux，你来跑）**：
```bash
strace -f -e execve -o /tmp/agy.execve agy   # 在 prompt 里 paste image
grep -E 'xclip|wl-paste|xsel|wl-copy|osc52|tmux|uploaded_media' /tmp/agy.execve
```

## 14. 安全 / 同意门（security-first）

`connect` 触发远端从 GitHub 源**下载并执行代码**——首次必打印 source(`ShunmeiCho/cc-clip-plugins`)/plugin 名/版本并要确认；`--yes` 非交互批准；`--no-plugin-marketplace` 强制本地源。校验源身份；marketplace 源钉 git ref。

## 15. 实施顺序（含 critic 指出的 schema→flag 依赖）

> **进度**（截至 `c5fc37d`，分支 `fix/issue-84-settings-hooks` / PR #86）：✅ 完成 · ⏳ 待办。步骤 1–6 已落地并经对抗式审查（每步两轮：security + go-idiom + 约束合规 → 综合 → 怀疑者反驳，零 confirmed CRITICAL/HIGH）；7 待办。

1. ✅ **DeployState per-adapter schema + 迁移**（§11）——是 flag 矩阵 adapter 级断言的**前置**，先落。`d43c779`(step 1)。
2. ✅ 抽 `internal/plugin` 接口 + `cc-clip plugin run <name>` runner，旧入口降级薄封装，**零行为变更**（含 agy-notify 的 Stop-JSON 契约分支）。`786cc50`(step 2)。
3. ✅ 迁移 claude-notify / codex-notify adapters。`a5a822d`(3a/3b)、`0f608be`(3c)。
4. ✅ `internal/install` InstallSourceChain + 规范 AdapterSource + 失败分类 + consent gate + 新 exitcode。`dd30531`(step 4/7)。
5. ✅ 引入 `--claude/--codex/--opencode/--antigravity/--all` + 菜单 + `parseDeployTargets`(判别器) + 矩阵；connect/setup 同步 breaking。`25d5947`(5.1 parser)、`5b86477`(5.2 菜单/非TTY)、`0286f83`(5.3a 容错 host 解析)、`5a1e269`+`d453a38`(5.3b 目标解析接线 + exit-2 矩阵 + legacy `--codex` 提示)、`489eea2`+`7d2ce09`+`5d0883f`(5.3c per-target 门控 + 非对称适配器测试 + 措辞修复)。**Option A 已拍板**：纯 `--codex`/`--agy` 跳过 shim 安装、绝不卸载既有 shim。
6. ✅ **Antigravity**：agy-notify bundled adapter + connect N5/N5.5 接线（temp-src bundle → `agy plugin validate` → `agy plugin install`，`command -v agy` 探测，`Verified=false`）。install smoke 双门控（`CC_CLIP_AGY_SMOKE=1` + agy 在 PATH，隔离 HOME）；真实远端 Stop hook-fire smoke + uninstall 路径列入 backlog；clipboard 待 strace。`78819f8`+`196e8cb`+`c5fc37d`。
7. ⏳ opencode-notify 真 plugin（步骤 4），剪贴板仍 shim。**（agy 之后）**

## 16. 需协调修改的文件

- `cmd/cc-clip/main.go` — `connectOpts.targets`、`cmdConnect`/`cmdSetup` 解析、菜单、`case "plugin"`、编排守卫、usage(`126-130/152-156`)、`classifyError`(新 exitcode)
- `internal/plugin/`（新）— PluginAdapter + runner + 各 adapter（含 agy-notify Stop-JSON）
- `internal/install/`（新）— InstallSourceChain + 规范 AdapterSource
- `internal/shim/deploy.go` — per-adapter NotifyDeployState + 迁移 + 谓词（导入 `install.AdapterSource`）
- `internal/shim/`（`hook_template.go`/`ssh.go`/`settings.go`）— 入口薄封装化
- `internal/exitcode/exitcode.go` — HARD_STOP 码
- `cmd/cc-clip/hosts.go` — 各 transport/adapter 独立 sticky
- 外部仓库 `ShunmeiCho/cc-clip-plugins`（净新增，需发布并钉 ref；claude/codex marketplace plugin + antigravity bundled plugin + opencode JS plugin + bundled fallback 资产）
- 文档：README×3 / `docs/commands.md` / `docs/upgrading.md` / `CLAUDE.md` / `CHANGELOG`（v0.9.0 BREAKING）

## 17. 测试计划（合并各片段）

- **state**：legacy 全 true/部分迁移、新 schema no-op、legacy 仍可反序列化、nil-safe、幂等、`Needs*` 谓词、transport 字段不被迁移触碰、round-trip 不含旧 key（11 项）。
- **flag**：`parseDeployTargets` 五 target + 互斥 + explicit 判别器；矩阵 `--codex --no-hooks`→ERROR、`--all --no-hooks`→ALLOWED、token-only 各组合；`runConnect` token-only 下 deploy.json adapter 不变 + 无 config 写。
- **install-chain**：UserRefused/PolicyForbidden 仅降级本地否则 HARD_STOP；VerifyFailed 立即 HARD_STOP 不降级；`--no-plugin-marketplace` 删 marketplace 源；`--yes` 不抑制 PolicyForbidden；与 `daemon.DeliveryChain` 零符号共享（编译级）。
- **setup**：非 TTY→{Claude} 不阻塞、TTY→菜单、`setup --codex`→codex-only + 一次性 stderr 提示、`--codex --all`→exit 2、与 connect 共用 parser。
- **antigravity**：`agy plugin validate/install <dir>` 命令构造；Stop hook runner 始终吐 `{"decision":""}`；hook-fire smoke。
- **marketplace builder**：Codex 用 `plugin add` 非 `install`；无 `--ref`。

## 18. 开放问题（需你拍板，剩余真正未决项）

1. ~~HARD_STOP exitcode~~ → **锁定 `14`**（实测 `exitcode.go`：业务码 10-13 已用、内部码从 20 起，14 空闲合理）。
2. ~~HealthVerified 迁移~~ → **锁定：迁移为 per-adapter `Verified=false`，强制重验**（旧全局 probe 不足以证明每 adapter 健康）。
3. ~~legacy *bool 保留期~~ → **锁定：保留至 v1.0（整个 v0.x）**，远端状态文件长寿，保留成本低。
4. **Antigravity clipboard**（仍待你远端 `strace`，§13）；media-file 若走 PUSH 还需定触发器 UX。
5. ~~legacy 提示可否 env 抑制~~ → **锁定并实现：`CC_CLIP_NO_DEPRECATION_NOTICE` 仅抑制 stderr 提示、不改 `--codex` 安装语义**（5.3b `maybeLegacyCodexNotice`，`5a1e269`）。

## 19. 风险

- **`cc-clip-plugins` 仓库未发布**：净新增，须先发布并钉 ref。
- **外部 CLI 命令面随版本漂移**（agy 1.0.0→1.0.4 数日）：设计以运行时探测 + fallback 兜底，不硬编码。
- **迁移期双发**：runner 与旧入口共存须经同一 `dedup.go`。
- **Antigravity Stop hook 阻塞风险**：runner 任何路径都必须吐合法 JSON。
- **`--all` 不含 antigravity clipboard**：须在 `--help`/文档讲清，避免误解"全装了"。
- **隧道维持不属于本重构（来自复核）**：`connect` 自己的 SSH master 用 `ClearAllForwardings=yes` 且结束 `Close()`（`ssh.go:56/169`），**不是**长期 RemoteForward 来源；长期隧道来自用户交互 SSH 会话或 setup 写入的 SSH config（`setup/sshconfig.go:19`）。target/plugin 安装**不负责**维持隧道；`http=000` 应引导用户重开 SSH 会话或跑 doctor，**不可**误导为"重装 target"。
