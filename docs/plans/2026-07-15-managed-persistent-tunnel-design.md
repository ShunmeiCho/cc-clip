---
created_at: "2026-07-15T13:32:13+08:00"
updated_at: "2026-11-13"
---

# cc-clip 托管式持久 SSH 隧道设计

<!-- markdownlint-configure-file { "MD013": { "tables": false } } -->

> **状态**：方向已获维护者同意。Phase 1A 已在 PR
> [#165](https://github.com/ShunmeiCho/cc-clip/pull/165) 的最终审查提交
> `c736247` 中实现，通过自动化、升级兼容性和真实主机验证，并已获维护者批准；
> 当前分支已完成 Phase 1B 服务与生命周期实现，范围见 §0.2 和 §14。
> 历史实机验收和重放后的自动化复验记录见 §15.4。
> 迁移 PR 和后续阶段尚未实现。托管隧道仍为 experimental。
>
> **决策优先级**：本文保留完整设计背景和演进记录；如果本文与 issue
> [#108](https://github.com/ShunmeiCho/cc-clip/issues/108) 中维护者确认的决定存在
> 差异，以 #108 中达成一致的决定为准。Phase 1A 的最终实现和审查结论以 PR #165
> 及其获批提交 `c736247` 为准。
>
> **原始评审基线**：2026-08-30，基于 `main@c43295c`、issue
> [#108](https://github.com/ShunmeiCho/cc-clip/issues/108) 的维护者回复、关联的
> issue/PR，以及当时的主分支代码复核。实现与验证更新见 §0.1、§0.2 和 §15。

**适用范围**：遵循 README 当前稳定路径——本机为 macOS
13+，远端为 Linux（amd64/arm64），远端具备 `curl`、`bash` 以及 `xclip` 或
`wl-paste`，并通过 `~/.ssh/config` 中命名的 `Host` 连接。

本文不设计 Linux/Windows 本机、非 Linux 远端或缺少上述依赖时的兼容路径；
这些环境继续按 README 的 experimental/manual 定位处理，不为本方案增加
service manager、SSH backend 或 probe fallback。

**核心目标**：初期由用户显式执行 `cc-clip tunnel enable <host>` 接管隧道；
启用后普通使用只需要 `ssh <host>`，cc-clip 不再依赖某个交互 SSH
会话“碰巧”持有 `RemoteForward`。旧交互式 forwarding 模式永久受支持；Phase 1A
已取得关键状态的真实主机证据，但 `setup` 是否对新 host 默认启用仍须另行评审，
当前不得据此改变默认值，已有 host 永不静默迁移。

## 0. 维护者决策与当前实现基线

维护者在 2026-08-15 的
[方向确认回复](https://github.com/ShunmeiCho/cc-clip/issues/108#issuecomment-5302967982)
中同意问题定义和总体方向，并明确了以下发布与评审约束：

1. 托管隧道初期必须 opt-in，由用户显式执行 `cc-clip tunnel enable`；
2. legacy 交互 forwarding 不进入弃用倒计时，而是永久支持，作为无本地 service
   manager 和 supervisor 无法认证时的可用模式；
3. `setup` 不得在 `reconnecting`、`auth-required`、`crash-loop`
   等状态经过真实主机观察前默认启用托管模式，已有 host 无论何时都不得静默迁移；
4. 原 Phase 1 先提交可手工驱动的 supervisor、SSH backend 和 state store
   （Phase 1A，已完成）；2026-10-01 的最新共识进一步将 Phase 1B 拆成服务/生命周期
   和迁移两个 PR；原 Phase 2、Phase 3 保持边界；
5. 状态必须穷尽且 fail closed，任何无法确定的探测结果保留独立状态，不能折叠成
   boolean 或邻近的成功/失败状态；
6. legacy `RemoteForward` 的恢复不能只依赖 journal；journal 丢失或损坏时也必须有
   一条文档化命令，安全地把目标 `Host` 恢复为 legacy 可用状态。

截至该原始基线，#108 仍为 open，上游没有 open PR；仓库当时没有实现 supervisor、
per-host LaunchAgent、`tunnel` 命令组、迁移器或回滚事务。已落地的是可复用原语
和代码整理：

| 来源                                                                                                                    | 已实现内容                                                                                                 | 对本文的影响                                                         |
| ----------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------- |
| [PR #74](https://github.com/ShunmeiCho/cc-clip/pull/74)、[PR #78](https://github.com/ShunmeiCho/cc-clip/pull/78)        | 本地 `tunnel.ProbeHealth`、`ErrDaemonNotAnswering`，以及 `/health` 的 `service=cc-clip` 身份校验           | 本地 health 直接复用，不重写                                         |
| [PR #114](https://github.com/ShunmeiCho/cc-clip/pull/114)                                                               | `RemoteHealthProbeCommand`、`ClassifyRemoteProbeOutput` 与 `ok/down/stale/unverified/unknown` 五态远端探测 | supervisor 的公开 health 层必须建立在该原语上                        |
| [PR #122](https://github.com/ShunmeiCho/cc-clip/pull/122)                                                               | troubleshooting 与 Windows 文档改用 `/health`，明确裸 TCP 可达不代表隧道健康                               | 后续文档延续 fail-closed 语义                                        |
| [Issue #20](https://github.com/ShunmeiCho/cc-clip/issues/20)、[PR #135](https://github.com/ShunmeiCho/cc-clip/pull/135) | 将 `main.go` 纯移动拆分为 `connect.go`、`notify.go`、`serve.go`，零行为变化                                | 只是为 #108 降低改动与审查成本；没有实现 supervisor 或可复用 backend |
| [Issue #3](https://github.com/ShunmeiCho/cc-clip/issues/3)                                                              | `internal/service/launchd.go` 管理本地 `cc-clip serve` daemon                                              | 可参考 service 生命周期，但不能当作 per-host tunnel service          |
| [PR #51](https://github.com/ShunmeiCho/cc-clip/pull/51)                                                                 | `hosts.json` 记录 literal SSH host 的部署历史                                                              | 不能替代本文的 canonical tunnel spec/state store                     |

GitHub 在 #108 时间线中显示 #20/#135，是因为 #135 明确写到未来更深的 connect
解耦最可能由 #108 的 supervisor 驱动。#135 同时明确声明没有签名变化、没有新 seam、
没有行为变化，因此该关联只代表前置整理，不代表 Phase 1 已开始实现。

§0.1 保留 Phase 1A 的交付记录。当前服务 PR 的实现范围见 §0.2 和 §14，
验收构建、结果及覆盖边界见 §15.4。

### 0.1 2026-09-29 实现状态更新

PR #165 的最终审查提交 `c736247` 已实现 Phase 1A，并获维护者 `APPROVED`。最终实现
边界如下：

- 已新增 `internal/tunnelmgr` 的 state/store、SSH backend、supervisor、私有 control
  socket、远端 health/identity 探测、退避、shutdown budget 和 crash-loop 熔断；
- 已新增持久 `instance-id` 和 token-protected `/tunnel/identity`，并在 connect
  部署/更新路径中保留安装 identity；
- 已提供前台入口 `cc-clip tunnel run <host> [--port N] [--reset]`，不安装
  LaunchAgent、不修改 SSH config，也不迁移 legacy `RemoteForward`；
- `remote-token-invalid` 会保留已有 SSH master/forward，持久化并输出
  `cc-clip connect <host> --token-only` 恢复指引，外部同步 token 后原地恢复；
- notification nonce 与 clipboard tunnel 认证保持分离；stale nonce 由 #150 的
  delivery receipts 和 `doctor --host` 暴露，supervisor 不检查或重写 nonce，也不修复
  缺失的 Claude hook 或真实 `xclip`；
- identity helper 缺失、identity endpoint 不可用和 instance mismatch 分别保留为
  `identity-helper-missing`、`identity-endpoint-unavailable` 和
  `identity-mismatch`，不再折叠为 `probe-unavailable` 或 `config-error`；
- `instance-id` 通过完整临时文件、`fsync` 和 create-if-absent 硬链接发布；identity
  初始化失败时 `serve` 降级运行，`/health` 仍可用，经过认证的
  `/tunnel/identity` 返回 503；
- 远端探测通过 `shim.WrapRemoteShell` 在 `/bin/sh` 下执行并带标准 PATH 前导；SSH
  master 检查和远端 probe 共享 `ProbeTimeout`；forward 失败也统一受 crash-loop
  熔断器约束；
- 默认 control socket 使用短的用户私有运行时目录，并验证目录所有者、权限和
  symlink；`tunnel run` 同时接受 `--port N` 与 `--port=N`；
- Phase 1B 的 `enable/status/restart/disable/auth/logs/restore-legacy`、per-host
  LaunchAgent、SSH config 迁移和 journal/回滚仍未实现；setup/update 默认行为没有
  改变。

“Phase 1A 已实现”只描述 PR #165 的功能边界，不表示 Phase 1B/2/3 已实现，也不表示
§15 中跨后续阶段的完整验收矩阵已全部通过。

### 0.2 Phase 1B 服务 PR 实现状态（2026-11-13）

维护者在 [#108 的回复](https://github.com/ShunmeiCho/cc-clip/issues/108#issuecomment-5925364328)
中将 Phase 1B 拆成服务与迁移两个 PR。当前服务 PR 提供 macOS 的
`tunnel enable/status/restart/disable/auth/logs`，并将 identity probe 改为认证但
不续期。交付范围和迁移 PR 的后续工作见 §14。

§5 至 §13 保留完整设计目标，并在相关位置注明当前服务 PR 的实现。
验收记录集中在 §15.4，分别标明自动化、本地 launchd fixture、真实远端和
文件系统验证的范围及对应构建。

## 1. 为什么需要重新设计

cc-clip 的远端 shim、Codex X11 bridge 和通知 hook 都通过远端回环地址
`127.0.0.1:18339` 访问本机 cc-clip
daemon。当前数据通路本身是合理的：端口只绑定在两端回环地址，剪贴板 API 继续使用 token 鉴权，也不需要在公网暴露服务。

问题出在“谁负责维持 SSH 隧道”。现在 `cc-clip setup` 把下面的配置写入目标主机的
`Host` block：

```sshconfig
RemoteForward 18339 127.0.0.1:18339
ControlMaster no
ControlPath none
```

因此，隧道不是 cc-clip 自己管理的长期资源，而是某个交互 SSH 连接的附属资源：

- 第一个成功绑定远端 `18339` 的 SSH 会话成为隐式“隧道所有者”；
- 关闭该会话会同时关闭隧道，即使远端工具正在另一个 SSH 连接中运行；
- 再打开多个 SSH 会话时，每个会话都会尝试绑定相同的远端端口，后来者出现
  `remote port forwarding failed for listen port 18339`；
- 用户无法从终端窗口用途、shell 内容或普通 `ssh`
  输出直观看出哪个连接才是所有者；
- 网络切换、睡眠唤醒或异常断线后，旧 `sshd`
  子进程可能暂时占用端口，新连接不能立即接管；
- 为回避已有 `ControlMaster`
  未携带新转发的问题，当前实现强制关闭 SSH 复用，牺牲了连接速度，却仍没有提供真正的隧道生命周期管理。

### 1.1 典型应用场景及其问题

**打开多个普通 SSH 连接后出现 port forward
warning**：连接 A 最先建立并成功绑定远端
`18339`；连接 B、C 随后根据同一份 SSH 配置再次申请该端口，于是出现
`remote port forwarding failed for listen port 18339`。触发条件只是多个 SSH 连接竞争同一个固定的
`RemoteForward`。

该 warning 本身不一定表示 cc-clip 已经失效。如果连接 A 的转发仍然健康并且指向预期的本地 daemon，那么连接 B 中的远端工具仍可通过共享的
`127.0.0.1:18339`
使用它。warning 表明的是“B 没有取得隧道所有权”，而不是“远端端口一定不可用”。

**cc-clip 在仍有 SSH 连接时突然失效**：连接 B 中正在运行需要 cc-clip
的远端工具，但实际 tunnel 由较早建立的连接 A 持有。用户关闭 A 后，远端
listener 随之消失；B 建立时的端口申请已经失败，OpenSSH 不会在 A 退出后自动替
B 重新申请该 `RemoteForward`。因此 B 和远端工具都还在运行，cc-clip
却会从“原本可用”突然变成不可用。这两个看似独立的现象，实际来自同一个隐式
所有权机制。

**本机睡眠、切换网络或关闭隧道所有者**：承载 forwarding 的 SSH
TCP 连接断开后，当前机制没有独立的恢复主体。网络恢复并不等于 reverse
tunnel 已恢复；在新连接成功绑定端口前，远端 clipboard 和 notification 请求都会失败。

“cc-clip 突然不生效”也可能由本地 daemon 停止、token 失效、远端 shim/bridge
异常或版本不一致导致，因此它不是端口所有权问题的唯一证据。只有结合 SSH
warning、远端 `127.0.0.1:18339` 健康检查和端口占用者，才能确认具体原因。

## 2. 已核实的现有机制

### 2.1 交互 SSH 才是长期隧道来源

`internal/setup/sshconfig.go` 的 `EnsureSSHConfig` 负责写入
`RemoteForward`、`ControlMaster no` 和
`ControlPath none`。README 也明确要求在 setup 后新开 SSH 连接，因为该连接负责保持 reverse
tunnel。

### 2.2 `connect` 的 ControlMaster 不是长期隧道

`internal/shim/ssh.go` 创建的是一次部署期间复用的临时 master：

- `ControlMaster=yes`；
- `ControlPersist=10`；
- 部署结束后调用 `Close()`；
- `ClearAllForwardings=yes`，故意不继承用户配置中的 `RemoteForward`。

这一设计解决的是重复输入 SSH
passphrase 和部署命令互相竞争端口的问题，不能维持 clipboard
tunnel。`docs/plans/2026-06-02-connect-target-split-design.md`
也把长期隧道明确排除在 connect/target 重构之外。

### 2.3 当前修复措施只避免竞争，没有消除所有者问题

`ControlMaster no`
能避免“旧 master 建立时没有 RemoteForward，后续 client 复用旧 master 后仍没有转发”的情况；`ClearAllForwardings=yes`
能避免部署 SSH 误抢端口。但普通交互 SSH 仍会逐个尝试同一个固定端口，所有权仍然隐式存在。

由此可见，问题与终端复用工具无关，而是 cc-clip 把数据通路的生命周期绑定到了
任意一条交互 SSH 连接。只要同时存在多个 SSH 连接，或者真正持有 forwarding
的连接先于使用 cc-clip 的连接退出，就会暴露这一结构性问题。

### 2.4 已发布的 health 原语

v0.9.2 已通过 PR #114 把 `connect`/`doctor` 的远端 tunnel 验证从裸 TCP
握手替换为 `GET /health` 服务身份检查。`internal/tunnel/remote_probe.go` 已提供：

- `RemoteHealthProbeCommand`：在远端通过 forward 探测 cc-clip daemon；
- `ClassifyRemoteProbeOutput`：保留 `ok`、`down`、`stale`、`unverified`、
  `unknown` 五种结果；
- `RemoteTunnelState.Healthy`：只有 `ok` 返回 true，其余全部 fail closed。

本地侧已经有 `tunnel.ProbeHealth` 和 `ErrDaemonNotAnswering`。本文的 supervisor
必须组合这些现有原语，不得重新引入 TCP-only 检查，也不得把五态重新压缩为
reachable/unreachable boolean。authenticated identity 是公开 health 之后的附加
所有权校验，不替代或重写已发布探针。

### 2.5 #20/#135 只完成实施前整理

issue #20 的 I1 由 PR #135 完成：把 `cmd/cc-clip/main.go` 中现有声明按职责纯移动到
`connect.go`、`notify.go` 和 `serve.go`。PR 的声明数保持 67=67，测试未因该移动而
改写，并明确说明“zero behavior change”“no new seams”。

因此 `cmdSetup`、`runConnect` 和 tunnel verification 现在更容易定位，但仍是
`cmd/cc-clip` 包内现有流程；Phase 1A 若需要共享 SSH 执行、探测或状态转换，仍应由
supervisor 的实际调用关系驱动最小解耦，不能把文件移动误认为 backend 已经存在。

### 2.6 现有 LaunchAgent 只管理本地 daemon

`internal/service/launchd.go` 当前生成固定 label `com.cc-clip.daemon`，执行
`cc-clip serve --port <port>`。它证明仓库已有 daemon 的 install/uninstall/status
经验，但没有 per-host label、tunnel spec、SSH child、control socket、重连状态机
或迁移事务。本文的 `ServiceManager` 可以复用测试和错误处理经验，不能直接延长
现有 daemon job 来承担 tunnel 生命周期。

### 2.7 现有规避方案及其使用成本

在托管式隧道实现前，可以通过 SSH 配置和进程管理规避问题，但这些方法都要求用户理解并手动维护本应属于 cc-clip 的运行时状态。

| 规避方案                                     | 能解决什么                              | 用户需要承担的操作                                                                              | 仍然存在的问题                                                                           |
| -------------------------------------------- | --------------------------------------- | ----------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| 保留一个固定的 SSH 隧道所有者                | 避免所有交互连接同时争抢端口            | 记住哪个窗口先连接；保持它一直打开；其他连接显式使用 `ClearAllForwardings=yes`                  | 关闭错误窗口、睡眠或断网后立即失效；没有自动恢复和状态提示                               |
| 创建专用 SSH alias/后台隧道                  | 把 tunnel 与日常 shell 初步分离         | 从普通 Host block 移走 `RemoteForward`；创建专用 alias；单独运行并维护 `ssh -NT <tunnel-alias>` | 需要手动启动、重启、查看日志和处理认证；开机后不会天然恢复                               |
| 使用 `ControlMaster auto` + `ControlPersist` | 多个终端复用同一 SSH 连接，减少重复绑定 | 设计 ControlPath；清理旧 master；保证第一个 master 建立时已加载 forwarding                      | 旧 master 可能没有 forwarding；Persist 到期或网络断开后仍需恢复；会改变用户普通 SSH 行为 |
| 为不同连接或 alias 分配不同 remote port      | 从端口层面避免直接冲突                  | 为每个 alias 维护端口，并同步修改远端 shim、bridge、token/config                                | 端口选择传播到整个数据通路；已有远端进程可能继续引用旧端口                               |
| 用户自行使用 autossh 维护专用 tunnel         | SSH 退出或失去传输能力后自动重启        | 安装 autossh；准备无人值守认证；维护每 host 命令、后台服务、日志和启动顺序                      | 不理解 cc-clip daemon/token/应用健康；仍需另外完成 SSH config 迁移和 LaunchAgent 配置    |

当前条件下，相对可控的临时做法是：指定一条专用 SSH 连接持有
`RemoteForward`，其他交互连接使用
`ssh -o ClearAllForwardings=yes <host>`，避免再次申请端口。但它要求用户在
每次连接时记住两类 SSH 的区别，并确保专用连接始终存活；一旦需要多个 host、
开机恢复、睡眠重连或故障诊断，操作量会迅速增加。

这些方案在技术上可行，却把以下内部细节暴露给了用户：

- 哪条 SSH 连接拥有远端端口；
- 普通连接何时需要清除 forwarding；
- 连接断开后由谁重启以及采用什么退避策略；
- 端口占用代表健康 owner、旧连接还是其他机器；
- daemon、SSH 连接和远端 HTTP endpoint 应按什么顺序启动和检查；
- cc-clip 更新后哪些后台进程需要重新加载。

因此，“可以通过手工配置解决”不等于产品无需改进。推荐设计的价值正是把这些决策收回到 cc-clip 内部，使用户只维护“该 host 是否启用 cc-clip
tunnel”这一项期望状态。

## 3. 设计目标与非目标

### 3.1 目标

1. 一次 setup 后，用户日常只执行 `ssh <host>`，无需记住
   `-R`、端口或哪一个终端负责隧道。
2. 交互 SSH 连接与 cc-clip tunnel 的生命周期互相独立。
3. 网络断开、睡眠唤醒和 SSH 子进程退出后自动恢复，并对连续失败进行退避。
4. 为每台 host 提供结构化运行状态，区分
   `healthy`、`reconnecting`、`local-daemon-down`、`auth-required`、`host-key-error`、
   `remote-down`、`remote-stale`、`remote-unverified`、`remote-unknown`、
   `identity-helper-missing`、`identity-endpoint-unavailable`、`identity-mismatch`、
   `remote-token-invalid`、`port-conflict`、`config-error`、`crash-loop` 和 `stopped`。
5. 不自动杀死远端 `sshd`，不覆盖用户自己定义的 SSH 转发，不降低 host
   key 校验强度。
6. 继续只在远端回环地址监听，保留现有 token/nonce 安全边界。
7. 更新 cc-clip 二进制后，已有 tunnel service 能受控重启并使用新版本。
8. legacy 交互 forwarding 永久保持受支持，并可从托管模式安全恢复，即使 migration
   journal 已丢失或损坏。

### 3.2 非目标

- 本文只解决“同一台本地电脑打开多个 SSH 连接”的端口竞争。若两台电脑同时连接同一远端主机，两边的 tunnel
  supervisor 仍会各自申请远端
  `127.0.0.1:18339`，只能有一边成功；支持这种多本地设备场景需要额外设计远端租约、每客户端动态端口或 broker 协议。
- 不把 cc-clip 变成通用 SSH 连接管理器。
- 不自动清理无法确认归属的远端监听进程。
- 不静默迁移已有 host，也不以实现托管模式为理由弃用 legacy 交互 forwarding。
- 本设计不把 autossh 集成为运行时依赖或 tunnel
  backend，也不复制或 vendor 其源码；只参考它在 SSH 监控、重连和退避方面的成熟机制，具体取舍见 §8。

## 4. 方案总览

推荐把现有的一条隐式通路拆成两个独立生命周期：

```text
交互面：  Terminal ── ssh <host> ── shell / remote tool
                         │
                         │ 不携带 cc-clip RemoteForward
                         │
数据面：  local daemon ←── cc-clip tunnel supervisor ──→ remote 127.0.0.1:18339
             :18339              │
                                 └── launchd 负责登录启动和 supervisor 崩溃拉起
```

每个目标 host 有一个 cc-clip 托管的 tunnel
supervisor。supervisor 启动一个专用、无 TTY、无远端 shell 的 SSH
master，并在其私有 control socket 上动态申请唯一的 reverse forward。普通
`ssh <host>` 不再携带 cc-clip 的 `RemoteForward`，因此任意数量的交互 SSH
client 都不会争抢 `18339`。

## 5. 用户体验

### 5.1 目标体验

初期发布采用显式 opt-in：

```console
$ cc-clip setup example-host --all
... legacy mode remains available ...

$ cc-clip tunnel enable example-host
$ cc-clip tunnel status example-host

$ ssh example-host
```

`enable` 只确认服务已启用，仍需通过 `status` 和日志核对隧道是否健康。
完成迁移后，关闭任意 SSH 窗口或新开多个 SSH tab，都不影响 cc-clip tunnel。

PR #165 已在真实主机上记录 `reconnecting`、`auth-required` 和 `crash-loop` 的进入与
恢复，满足维护者为未来 default-on 讨论提出的证据前提；这不自动改变产品默认值。
后续版本仍须单独评审是否让新 host 的 `setup` 默认调用 enable。已有 host 始终要求
显式确认迁移。选择不启用或无法满足 service-context 认证的用户继续使用永久受支持
的 legacy 模式。

### 5.2 管理命令

下面的实验性服务命令均在运行本地 daemon 的 macOS 上执行。命令不修改 SSH
配置，运行情况可通过 `status` 和 `logs` 查看。

```console
cc-clip tunnel enable <host> [--port <port>]
                                      # 创建状态并安装/启动 LaunchAgent
cc-clip tunnel status [<host>]     # 显示服务设置、进程和最近健康观测
cc-clip tunnel restart <host>      # 重启该 tunnel 的 LaunchAgent 并重建 SSH child
cc-clip tunnel auth <host>         # 前台刷新认证并在 service 环境复验
cc-clip tunnel disable <host>      # 持久禁用并移除 job；保留配置/runtime/日志
cc-clip tunnel logs <host>         # 给出或跟随该 host 的日志
```

`restore-legacy` 留给 Phase 1B 迁移 PR，当前分支尚未实现。

Phase 1A 的前台/manual 入口继续可用。

```console
cc-clip tunnel run <host> [--port <port>] [--reset]
```

该入口直接从 host 和本地 daemon identity 构造 spec，用于前台运行 supervisor。
`--reset` 按当前 spec 重建 runtime，可清除 crash-loop 熔断状态。本地 identity
变化后，也可用它显式更新期望身份。它不写 SSH config，也不安装 service。
当前服务 PR 已提供实验性内部入口 `tunnel run --id <tunnel-id>`，读取持久
启用配置。该形式与 host、`--port`、`--reset` 互斥。
Phase 1A 同时接受 `--port <port>` 和 `--port=<port>`。

当前服务 PR 新建服务时，端口依次优先使用 `--port`、已安装 daemon 的端口、
`CC_CLIP_PORT`，最后使用 18339。再次 enable 已启用的服务会复验已有配置，
请求不同端口时明确报错。`tunnel restart` 先校验配置、runtime/schema、本地 identity 和后台
认证，再停止旧 job、等待 lifetime lock 释放、重置有效 runtime 并重新安装启动。
损坏或不支持的 schema、权限及 identity 问题需要先修复，再显式重启。
迁移 PR 才增加 §9.2 的端口来源和 journal 契约。

### 5.3 setup 的目标行为与决策边界

本文定义 README 稳定路径下的长期目标行为：setup 可编排远端部署/探针能力检查、
后台认证预检、LaunchAgent 安装、旧 SSH 配置迁移和端到端 health 验证；成功后
用户只需普通 `ssh <host>`。初期 setup 不默认调用该路径，而是由显式
`tunnel enable` opt-in；已有 host 永不静默迁移。其中 SSH 配置与 LaunchAgent
切换属于本地迁移事务，远端组件部署是独立操作，两者不能伪装成一个跨机器原子事务。

`setup` 是面向完整安装流程的 orchestrator：它在满足远端能力前提后调用与
`tunnel enable` 相同的内部
`TunnelManager.Enable(sshAlias)`，而不是维护第二套迁移器。独立的
`tunnel enable <host>`
面向“远端 cc-clip 已部署、只需接管现有 tunnel”的场景，只修改本地 tunnel
spec、SSH config 和 service；它不得隐式改写远端 hook、target 或 deploy
state。若远端缺少交付级 identity
helper，命令应提示先通过现有 connect/redeploy 流程补齐能力，或以
`identity-helper-missing` 运行，但不能把该部署步骤并入迁移 journal。

如果目标环境无法进行无人值守认证，setup/enable 必须明确说明托管模式不可用，
不能静默声称安装成功，也不能让 LaunchAgent 进入持续认证失败循环。此时继续保留并
支持旧交互模式，不把它标记为 deprecated，也不以升级提示推动用户离开该模式。

从 opt-in 改为新 host 默认启用属于未来独立发布决策。PR #165 已提供真实主机上
`reconnecting`、`auth-required`、`crash-loop` 的观察与恢复证据，完成了 #108 约定
的证据前提，但任何默认值变化仍须另行评审。无论未来默认值如何变化，已有 host 的
迁移都必须保持显式。

## 6. 组件设计

### 6.1 `internal/tunnelmgr`

建议新增专用包，避免与当前负责 HTTP probe/fetch 的 `internal/tunnel` 混合：

```go
type Spec struct {
    ID                 string         `json:"id"`
    SSHAlias           string         `json:"ssh_alias"`
    CanonicalHost      string         `json:"canonical_host"`
    User               string         `json:"user"`
    SSHPort            int            `json:"ssh_port"`
    IdentityFiles      []string       `json:"identity_files"`
    CertificateFiles   []string       `json:"certificate_files"`
    ProxyJump          string         `json:"proxy_jump"`
    LocalPort          int            `json:"local_port"`
    RemotePort         int            `json:"remote_port"`
    ExpectedInstanceID string         `json:"expected_instance_id"`
    Enabled            bool           `json:"enabled"`
    Migration          MigrationState `json:"migration"`
}

type MigrationState struct {
    Phase            string `json:"phase"`
    JournalPath      string `json:"journal_path"`
    BackupPath       string `json:"backup_path"`
    SourceConfigHash string `json:"source_config_hash"`
}

type RuntimeState string

const (
    StateStarting           RuntimeState = "starting"
    StateHealthy            RuntimeState = "healthy"
    StateReconnecting       RuntimeState = "reconnecting"
    StateLocalDaemonDown    RuntimeState = "local-daemon-down"
    StateAuthRequired       RuntimeState = "auth-required"
    StateHostKeyError       RuntimeState = "host-key-error"
    StateRemoteDown         RuntimeState = "remote-down"
    StateRemoteStale        RuntimeState = "remote-stale"
    StateRemoteUnverified   RuntimeState = "remote-unverified"
    StateRemoteUnknown      RuntimeState = "remote-unknown"
    StateIdentityHelperMissing       RuntimeState = "identity-helper-missing"
    StateIdentityEndpointUnavailable RuntimeState = "identity-endpoint-unavailable"
    StateIdentityMismatch            RuntimeState = "identity-mismatch"
    StateRemoteTokenInvalid          RuntimeState = "remote-token-invalid"
    StatePortConflict                RuntimeState = "port-conflict"
    StateConfigError                 RuntimeState = "config-error"
    StateCrashLoop                   RuntimeState = "crash-loop"
    StateStopped                     RuntimeState = "stopped"
)
```

职责边界：

- `SpecStore`：持久化用户期望状态；
- `Supervisor`：创建/监控 SSH 子进程、退避、信号处理；
- `SSHBackend`：以 argv 调用 OpenSSH，不经过 shell；
- `HealthChecker`：本地 daemon、SSH
  master、远端公开 health 和认证 identity 探测；
- `ServiceManager`：封装 LaunchAgent 的安装、停启、重启和卸载；
- `Migrator`：移除旧的 cc-clip `RemoteForward`，但不泛化编辑用户 SSH 配置。

### 6.2 每 host 独立 LaunchAgent

每个 enabled tunnel spec 对应一个当前用户权限下的 LaunchAgent，统一执行
`cc-clip tunnel run --id <tunnel-id>`。launchd 只负责登录启动和 supervisor 意外退出后的兜底拉起；SSH
child 的健康检查与重连由 cc-clip supervisor 负责。

当前服务 PR 的 job 与本地 daemon 分开管理。日志保存在
`~/Library/Logs/cc-clip/tunnels/<tunnel-id>.log`，目录权限为 0700，文件为 0600。
停止服务时会在规定时间内清理自己创建的 SSH 进程和 control socket，保留配置、
runtime 和日志供排查。并发控制与配置存储见 §10。

LaunchAgent 扩大了运行面，也把错误从当前终端移到后台：job 可能未加载，SSH
可能在无 TTY 的 service context 中等待无法显示的 keychain/passphrase 交互，host
key、agent socket 或网络错误也可能只出现在日志中。正因这些失败比普通
`RemoteForward` 的终端 warning 更难被发现，初期必须 opt-in，并把
`status`/`logs`、结构化状态、低频等待和 service-context 预检作为 LaunchAgent
交付的一部分，不能先默认安装后台 job 再补可观测性。

plist 示例为：

```text
Label: com.cc-clip.tunnel.<tunnel-id>
ProgramArguments: <cc-clip-binary> tunnel run --id <tunnel-id>
RunAtLoad: true
KeepAlive.SuccessfulExit: false
ThrottleInterval: 60
ExitTimeOut: 10
```

对于 `auth-required`、`host-key-error`
等需要用户介入的状态，supervisor 保持运行并进入低频等待，而不是退出后交给 launchd 反复拉起。`KeepAlive.SuccessfulExit=false`
表示只在异常退出时兜底重启，主动停止不会再次拉起。

启动阶段还需要持久化 crash-loop circuit
breaker。supervisor 在进入 bootstrap 前写入
`started_at`；进程存活越过 15 秒 starting
gate 后，才不再把本次退出计为“快速启动失败”。端到端 health 连续稳定 30 秒后写入
`stable_at` 并重置 SSH 退避。两者用途不同：starting gate 约束进程 crash
loop，stable-reset-window 约束连接重试节奏。连续快速失败超过阈值后写入
`crash-loop`/`last_error` 并成功退出，配合 `KeepAlive.SuccessfulExit=false`
阻止再次拉起，等待用户执行 restart/repair。可确定的配置解析错误采用相同停机语义。launchd 不会按自定义非零 exit
code 提供指数退避，因此不能依赖“特殊错误码”解决 crash loop，意外崩溃只由静态
`ThrottleInterval` 兜底。

本地 daemon 是创建 SSH child 前的启动门闩。setup 必须先保证现有 daemon
service 已安装且 `/health` 正常；单独执行 `tunnel enable`
时若 daemon 未安装，应返回可操作错误而不是静默安装另一个服务。实现不能依赖 daemon
job 与 tunnel job 的操作系统启动顺序；supervisor 可以长期停留在
`local-daemon-down` 并低频探测，但在本地 daemon 健康前不得建立或反复重建 SSH
tunnel；该等待不计入 SSH 重连失败次数。

plist 不保存 token、私钥或密码。control
socket、运行状态和日志位于用户私有目录；`~/.cache/cc-clip/tunnel-runtime/`
及其 control-socket 子目录必须以 `0700` 创建，状态文件和 spec 为
`0600`。已有目录权限更宽时应 fail closed 或先安全收紧，不能继续创建 socket。

### 6.3 专用 SSH master

推荐使用私有 ControlMaster，但它与用户的交互 ControlMaster 完全隔离：

```text
ssh -M -N -T \
  -S <private-control-socket> \
  -o ControlMaster=yes \
  -o ControlPersist=no \
  -o ClearAllForwardings=yes \
  -o BatchMode=yes \
  -o ServerAliveInterval=15 \
  -o ServerAliveCountMax=3 \
  -- <host>
```

master 建立后，再通过 control operation 增加 reverse forward：

```text
ssh -F <empty-control-client-config> \
  -S <private-control-socket> -O forward \
  -R 127.0.0.1:<remote-port>:127.0.0.1:<local-port> \
  -- <host>
```

之所以分两步，是因为 `ClearAllForwardings=yes`
会清除配置文件及命令行中的 forward；不能一边设置它、一边假设同一条启动命令里的
`-R` 仍然有效。两步方式先保证 tunnel master 不继承用户的任何
`LocalForward`/`RemoteForward`，再由 control
operation 只增加 cc-clip 自己管理的那一条转发。

control operation 还必须通过 `-F`
使用 cc-clip 创建的私有空配置文件，避免它第二次读取用户 `Host`
block 中尚未迁移的 legacy
`RemoteForward`。初始 master 仍使用用户的正常 SSH 配置，以保留 HostName、User、
IdentityFile、ProxyJump 等连接语义；只有已经拿到私有 socket 的 control client
使用空配置。使用实际文件也便于检查权限和测试 argv。

Phase 1A 的默认 control socket 不再固定放在 HOME 下。实现按
`XDG_RUNTIME_DIR`、`os.TempDir()`、`/tmp` 的顺序尝试创建随机、当前用户拥有且权限为
`0700` 的短目录；只有 socket 路径长度仍安全时才回退到 HOME 目录。目录必须通过
`Lstat` 的普通目录、非 symlink、当前用户所有权和权限检查，清理时只删除本次创建的
socket、空配置和临时运行时目录。显式 `ControlDir` 仍保留给调用方。

`-O forward` 失败必须被当成 tunnel 未建立，不能只看 SSH
TCP 连接是否存在。若失败被明确分类为远端 bind conflict，且随后的 `-O check`
证明 master 仍健康，supervisor 将该 master 作为受控的 `port-conflict`
等待资源保留，只按固定间隔重试
`-O forward`，避免重复认证和 ProxyJump 开销。其他 forward 失败，或 master
check 失败时，必须立即尝试带超时的 `-O exit`；若 control
operation 未在时限内完成，则向自己记录的 master child 发送 `SIGTERM`，经过 grace
period 仍未退出才发送
`SIGKILL`。不得留下既未建立 forwarding、又不处于显式 conflict 状态的 master。

正常停止也采用同一套有界流程：分别为 `-O cancel` 和 `-O exit` 使用独立的
`context.WithTimeout`；任一步骤超时都跳过后续 control socket 等待，直接
`SIGTERM` 已记录的直接子进程，等待固定 grace period 后再
`SIGKILL`。所有 wait 都必须可被 supervisor 自身的 shutdown
context 取消，不得按进程名或端口批量 kill。

该流程的总预算上界为 8 秒：`cancel` 最多 1 秒、`exit` 最多 1 秒、`SIGTERM`
grace 最多 4 秒，另保留 2 秒完成状态落盘和进程回收；进入后续阶段时不再重新获得完整预算，因此小于 plist 的
`ExitTimeOut: 10`。若 launchd 在异常情况下仍强制终止 supervisor，下次启动必须检查 runtime 中记录的 child
PID、进程启动时间和 control socket，并在确认仍是自己创建的 SSH
child 后执行同样的有界清理；PID 身份无法确认时不得发送信号，以避免 PID
reuse 误杀。

### 6.4 为什么仍使用 ControlMaster

这里的 ControlMaster 是内部实现细节，而不是重新打开用户全局的 SSH 复用：

- control socket 路径由 cc-clip 独占；
- 生命周期由 supervisor 明确拥有；
- 可以在同一连接上执行 `forward`、`cancel`、`check` 和远端 health probe；
- 普通 `ssh <host>` 不会复用该 socket，也不会继承它的 forwarding。

因此它不会重现当前 troubleshooting 文档中的“旧用户 master 没有 RemoteForward”问题。

## 7. 健康检查与端口冲突

### 7.1 四项健康契约

公开 health 层直接复用 v0.9.2 已发布的
`tunnel.RemoteHealthProbeCommand`/`ClassifyRemoteProbeOutput`，不得另写一份 probe
脚本或重新解释 marker。其五态在 supervisor 中保持一一可见：

| 已有 `RemoteTunnelState` | supervisor 状态                                                  | 语义                                                                     |
| ------------------------ | ---------------------------------------------------------------- | ------------------------------------------------------------------------ |
| `ok`                     | 继续执行 authenticated identity；identity 也通过后才是 `healthy` | forward 确实连到一个回答 `/health` 的 cc-clip daemon                     |
| `down`                   | `remote-down`                                                    | 远端端口不可达；不能折叠为普通网络错误                                   |
| `stale`                  | `remote-stale`                                                   | 端口接受连接但 cc-clip daemon 未回答；结合本地 health 与 master 状态诊断 |
| `unverified`             | `remote-unverified`                                              | 端口可达但远端缺少 `curl`，明确 fail closed                              |
| `unknown`                | `remote-unknown`                                                 | 输出无法分类或 probe 未完成，保留独立未知状态                            |

authenticated identity 层继续细分为 `identity-helper-missing`、
`identity-endpoint-unavailable`、`remote-token-invalid`、`identity-mismatch` 和
`remote-unknown`，不能与已有 public health 的 `unverified` 或 `unknown` 合并。这样
既复用已发布五态，又满足维护者关于“无法确定的结果必须拥有自己的状态”的要求。

只有以下四项都通过才显示 `healthy`：

1. 本机 `127.0.0.1:<local-port>/health` 返回现有最小响应
   `{"service":"cc-clip","status":"ok"}`；
2. 使用空 control-client config 执行
   `ssh -S <socket> -O check <host>`，确认 master 活着；
3. 通过该 master 在远端请求
   `127.0.0.1:<remote-port>/health`，响应中的 service/status 必须属于 cc-clip；
4. 使用远端 helper 携带 token 请求 `GET /tunnel/identity`，且 `protocol_version`
   受支持、`instance_id` 与 spec 中的 `expected_instance_id` 完全一致。

第三、四项远端检查不是在 `-N` master 进程中执行命令，而是由普通 control
client 复用同一 master 新开一个 session channel：

```text
ssh -F <empty-control-client-config> \
  -S <private-control-socket> \
  -o ClearAllForwardings=yes \
  -- <host> <remote-probe-command>
```

OpenSSH 没有 `-O exec` control command；`-O` 只用于
`check`、`forward`、`cancel`、`exit`
等控制操作。远端 probe 必须使用上面的普通远端命令形式，并为整个 exec 设置超时。
最终传给 SSH 的远端命令必须经过 `shim.WrapRemoteShell`，带上仓库统一的远端 PATH
前导并由 `/bin/sh` 执行，不能依赖目标账号的 login shell。`-O check` 和随后执行的
远端 probe 共享同一个 `ProbeTimeout` 预算，避免卡住的 master 让状态长期停留在旧值。

仅验证公开 `/health`
不能区分“该端口连到预期的本地 daemon”与“连到其他客户端的 cc-clip
daemon”。在托管模式达到可交付状态前，daemon 必须增加 token-protected 的只读
`GET /tunnel/identity` endpoint，例如：

```json
{
  "service": "cc-clip",
  "status": "ok",
  "protocol_version": 1,
  "instance_id": "<random-persistent-id>"
}
```

`instance_id`
是安装级随机标识，不使用 hostname 或其他设备信息，持久化在用户私有的
`~/.cache/cc-clip/instance-id`（目录 `0700`、文件
`0600`），并在 daemon 重启和常规版本更新之间保持不变；endpoint 只有持有现有 clipboard
token 的请求才能访问。tunnel spec 记录期望的 `instance_id`，从而在 port
conflict 时区分“同一 daemon 的现有 tunnel”“其他 cc-clip instance”与“非 cc-clip
listener”。只有显式的维护操作才能轮换该标识；轮换必须原子更新所有关联 spec
的期望值，再重启并复验 tunnel，不能在 daemon 启动时自动生成新值。在增加该
endpoint 前，不应通过真实 clipboard fetch 做周期性强验证。

首次创建时先写完临时文件、设置 `0600`、执行 `fsync`，再通过 create-if-absent
硬链接发布，因此并发创建者不能覆盖胜出者，也不会暴露半写入的最终文件。若已有
`instance-id` 为空或内容无效，Phase 1A 不会自行修复：`serve` 继续以 identity
unavailable 降级启动，`/health` 保持 200，未认证 `/tunnel/identity` 保持 401，经过
认证的 `/tunnel/identity` 返回 503。Phase 1B 的 identity 后续项保留这一降级行为，
警告会给出文件路径和修复步骤。先停止 daemon 和 managed tunnels，
恢复有效备份，或移走损坏文件后重启 daemon。若创建了新 identity，必须显式
disable/re-enable 各 tunnel service，或通过 `tunnel run --reset` 更新前台 tunnel
的期望值；不能静默轮换 identity 或把普通 restart 当作重新绑定。完整操作步骤见
[故障排查文档](../troubleshooting.md#tunnel-identity-unavailable)。
无法创建硬链接的 token 目录（例如部分 FUSE/exFAT）同样会保持 identity
unavailable；这是已知兼容性限制，不得回退到可能暴露部分写入或覆盖既有 identity
的发布方式。发布失败会提示检查目录权限和硬链接支持；用户文档说明将 token/identity
目录放在支持硬链接的文件系统上，并在迁移时保留已有 identity 和凭据。已有有效
identity 的读取不需要硬链接。

所有 enable 路径都必须在写入 enabled spec、修改 SSH
config 或安装 LaunchAgent 前，用本地 token 请求本地 identity
endpoint，并把结果原子写入
`expected_instance_id`；不能先建立 tunnel 再回填期望身份。§9.2 步骤 7 是 legacy 迁移场景下的具体顺序。

README 的稳定路径已经要求 Linux 远端具备 `curl` 和 `bash`，因此 remote
probe 可以直接依赖这两个工具，无需设计 `wget` 或无 HTTP
client 的兼容分支。已经部署的远端 cc-clip
helper 从现有 token 文件读取凭据并调用 identity
endpoint，不把 token 放进 argv、日志或拼接后的 shell 字符串。缺少支持认证 identity
的 helper 时进入 `identity-helper-missing`；helper 存在但 daemon 返回 404/503 时进入
`identity-endpoint-unavailable`，两者都不能报告为“tunnel 不可达”或 `healthy`。
Phase 1A 固定从 `$HOME/.local/bin/cc-clip` 调用 helper，因此通过
`--use-remote-bin` 使用其他远端路径的 host 需要重新部署到该位置，或接受
`identity-helper-missing` 状态；任意远端 helper 路径发现留给后续设计。
已有 host 若仍部署旧版 helper，必须先运行
`cc-clip connect <host> --force` 重新部署支持 `tunnel probe-identity` 的远端二进制，
否则 `tunnel run` 无法达到 `healthy`。

能力边界需要分别报告：建立 SSH master 和 reverse
forward 不要求升级远端二进制；公开 `/health` 可由远端 `curl`
验证；交付级 identity 校验要求支持认证 probe 的远端 helper。按维护者最新共识，
迁移 PR 只有在 managed forward 持有端口并通过完整本地及远端 identity 检查后才
提交 ownership。缺少 helper、identity 不可用或 token 被拒绝时不得提前提交迁移，
也不能对用户宣称已经 `healthy`。当前服务 PR 不写 SSH config 或迁移 journal。`connect`/`setup`
可以编排远端 helper 部署后再调用 enable，但 helper 部署成败不写入本地迁移 journal。

### 7.2 状态分类

| 条件                                                                          | 状态                            | 行为                                                                                                                            |
| ----------------------------------------------------------------------------- | ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| 本地 daemon 不可达                                                            | `local-daemon-down`             | 保持服务，低频重试并提示修复 daemon                                                                                             |
| SSH 初次认证失败                                                              | `auth-required`                 | 保持 supervisor，低频等待并提示前台执行认证预检                                                                                 |
| host key 失败                                                                 | `host-key-error`                | 保持 supervisor，不自动绕过校验，等待用户修复                                                                                   |
| 已有 public probe 返回 `down`                                                 | `remote-down`                   | 保留原始结果，检查 master/forward，不把未监听误报为健康                                                                         |
| 已有 public probe 返回 `stale`                                                | `remote-stale`                  | 保留原始结果，结合本地 daemon 与 master 状态诊断，不自动杀远端进程                                                              |
| 已有 public probe 返回 `unverified`                                           | `remote-unverified`             | 明确缺少 `curl`，fail closed                                                                                                    |
| 已有 public probe 返回 `unknown`                                              | `remote-unknown`                | 保留未完成/无法分类状态和原始安全摘要，不并入邻近状态                                                                           |
| 缺少或不兼容认证 identity helper                                              | `identity-helper-missing`       | SSH/tunnel 层单独显示；通过现有 connect/redeploy 流程部署 helper 后自动复验，不假报健康                                         |
| helper 存在，但 daemon identity endpoint 返回 404/503                         | `identity-endpoint-unavailable` | 保留 SSH master/forward，单独报告 daemon capability 或 identity 初始化问题，不与 helper 缺失合并                                |
| 我方 forward 已成功，公开 health 正常，但 identity 返回 401/403 或 token 缺失 | `remote-token-invalid`          | 保留 SSH master/forward，提示执行 `cc-clip connect <host> --token-only` 或 setup，再复验 identity；不得因 token 错误重建 master |
| 本地 daemon 或我方 forward 返回的 identity 与 spec 期望不匹配                 | `identity-mismatch`             | 与一般配置解析错误分开，停止自动重建并提示检查 instance-id/显式 rotate；不得误报健康                                            |
| `-O forward` 返回端口占用                                                     | `port-conflict`                 | 探测现有 listener，每 45 秒重试 bind+identity，不进入 SSH 指数退避                                                              |
| spec/config 无法解析                                                          | `config-error`                  | 持久化错误并停止 launchd 自动重启，等待 repair                                                                                  |
| supervisor 连续未越过 starting gate                                           | `crash-loop`                    | 打开 circuit breaker，停止自动重启，等待显式 restart/repair                                                                     |
| SSH/网络断开                                                                  | `reconnecting`                  | 按退避策略重建 master                                                                                                           |
| 四项均通过                                                                    | `healthy`                       | 进入稳定窗口；连续 30 秒后重置 SSH 退避                                                                                         |

探测和恢复节奏按失败域分开，避免无意义的 HTTP 或 SSH 风暴：

| 当前状态                                                                             | 默认动作与间隔                                                                                                                                                                                                                                                                         |
| ------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `healthy`                                                                            | 每 60 秒执行一次四项检查；status 只读显示最近观测，不发起新检查                                                                                                                                                                                                                          |
| `local-daemon-down`                                                                  | 每 15 秒只检查本地 daemon，恢复前不创建 SSH child                                                                                                                                                                                                                                      |
| `auth-required` / `host-key-error`                                                   | 每 5 分钟做一次 service-context 预检，并允许 `tunnel auth` 立即触发                                                                                                                                                                                                                    |
| `remote-down` / `remote-stale`                                                       | 每 15 秒复验 public health，并先利用本地 daemon 与 `-O check` 区分失败域；不能只靠重启 master 碰运气                                                                                                                                                                                   |
| `remote-unverified` / `remote-unknown`                                               | 每 60 秒复验 public probe，保留原始分类，不在未知时报告成功                                                                                                                                                                                                                            |
| `identity-helper-missing` / `identity-endpoint-unavailable` / `remote-token-invalid` | 每 60 秒复验对应 capability/identity，不重建健康的 SSH master                                                                                                                                                                                                                          |
| `identity-mismatch`                                                                  | fail closed 并停止当前 supervisor；等待用户核对本地 identity、spec 与端口归属后显式修复                                                                                                                                                                                                |
| `port-conflict`                                                                      | master 健康时每 45 秒只重试 bind；bind 成功后继续完整 identity 检查，检查通过才允许提交 pending ownership 迁移；失败不得提交。运行状态分别进入 `healthy`、identity capability 状态或 token 错误。若 `-O check` 或重试前的 SSH 操作失败，则按原因转为 `reconnecting`、`auth-required` 或 `host-key-error`，不继续沿用 conflict 定时器 |
| `reconnecting`                                                                       | 只按 SSH 指数退避重建 master；master 建立前不执行远端 HTTP probe                                                                                                                                                                                                                       |
| `config-error` / `crash-loop`                                                        | 不自动重试，等待显式 repair/restart                                                                                                                                                                                                                                                    |

`port-conflict` 的分类必须以“我方 bind 已失败”为前提，再结合远端 listener
probe，不能脱离 transport 上下文只看 HTTP 状态：

| bind 失败后的 probe 结果                                           | `conflict_class` | 首选处理                                                              |
| ------------------------------------------------------------------ | ---------------- | --------------------------------------------------------------------- |
| authenticated identity 与 `expected_instance_id` 相同              | `self`           | 尝试回收上一次 supervisor 明确拥有的旧 SSH child；成功后立即重试 bind |
| authenticated identity 成功但 instance 不同                        | `other-instance` | 保留当前 healthy master，固定间隔重试 bind；提示关闭另一客户端        |
| public health 是 cc-clip，但 identity 返回 401/403、缺失或无法认证 | `owner-unknown`  | 按端口归属冲突处理，不提示 token-only；身份不足以安全断定是哪一实例   |
| public health 不是 cc-clip                                         | `non-cc-clip`    | 不自动清理，展示 listener 类型和手动检查建议                          |
| 无法获得足够 probe/owner 信息                                      | `owner-unknown`  | 不自动清理，固定间隔重试并展示权限/可见性限制                         |

`conflict_class=self` 只证明 listener 通向同一个 cc-clip
daemon，不单独证明占用端口的 SSH 进程归 supervisor 所有。reclaim 必须先从上一份 runtime 取得旧 control
socket、child PID 和启动时间，按 §6.3 校验进程身份，再执行有界 `cancel/exit`
或 PID 精确的 TERM→grace→KILL。清理成功后立即用当前 healthy
master 重试 bind；记录缺失、PID 身份不符或 socket 归属不可验证时禁止发送信号，
保留当前 master 并回到 45 秒重试，同时提示关闭可能的 legacy SSH 连接。

### 7.3 不自动杀远端 sshd

仅凭 `18339` 被占用无法判断它是：

- 当前 supervisor 建立但状态记录异常的 tunnel；
- 其他客户端建立的 tunnel；
- 用户自己的转发；
- 真正失效的 sshd 子进程。

因此自动 `sudo kill $(lsof -ti :18339)` 风险过高。`doctor` 可以先做 `/health`
和 authenticated identity
probe，再尽力查询 listener/PID；但普通远端用户可能没有权限看到其他用户或 `sshd`
的进程信息，此时必须明确显示“listener 存在但 owner 不可见”，不能伪造精确
归属。清理动作必须要求用户明确确认，并且只针对有权限且已识别的精确 PID；
无权限时只给出 Linux 上的手动诊断建议。

## 8. 如何参考 autossh（不集成）

autossh 在这里是设计与测试参考，不是产品组件。cc-clip 不调用 autossh、不要求用户安装 autossh，也不把它作为可选 tunnel
backend。

### 8.1 借鉴的行为

[autossh 原始项目说明](https://www.harding.motd.ca/autossh/)把问题拆成“启动 ssh、监控、失败后重启”。本设计借鉴以下机制：

- **starting
  gate**：连接需稳定一段时间才算成功，避免“刚连上立即断开”不断重置退避；
- **渐进退避**：短时间连续失败时逐步拉长重试间隔；
- **稳定后清零**：连接超过稳定窗口后再失败，从较短间隔重新开始；
- **信号语义**：supervisor 收到停止信号时终止 child；显式 `tunnel restart`
  重启该 host 的 LaunchAgent 和 SSH child，不影响本地 daemon 或其他 host；
- **无人值守认证前提**：后台任务只能依赖 key、agent 或系统 keychain，不能等待交互密码；
- **连接存活检测**：配合 OpenSSH 的
  `ServerAliveInterval`/`ServerAliveCountMax`，让半开连接最终退出并触发重建；
- **forward 建立失败必须失败**：不能把“SSH 已登录”误判成“隧道已建立”。

autossh 的 monitor port/echo
loop 用于判断 SSH 是否仍能传输数据；cc-clip 已经拥有可识别服务身份的
`/health`，并计划增加 token-protected 轻量探测，因此无需照搬额外的 monitor
port。OpenSSH
server-alive 负责尽快发现连接失效，cc-clip 端到端 probe 负责确认目标应用通路，两者职责不同。

建议默认退避参数：

```text
starting-gate = 15s
initial = 1s
factor  = 2
max     = 5m
jitter  = ±20%
stable-reset-window = 30s
```

固定值最终应通过测试和真实睡眠/网络切换场景校准，不暴露为 Phase 1A/1B 用户配置。

### 8.2 不直接依赖 autossh 的原因

| 方案                       | 优点                                              | 问题                                                                                                               |
| -------------------------- | ------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| 直接执行 `autossh`         | 成熟、改动少                                      | macOS 未必预装，会增加额外安装依赖和 LaunchAgent 配置；状态与错误难结构化；不了解 cc-clip token、daemon 和端口归属 |
| vendor/copy autossh 源码   | 可完全控制                                        | 引入 C 构建与许可证维护成本，且 cc-clip 主体是 Go；多数 monitor-port 逻辑不是本产品所需                            |
| Go 内部实现专用 supervisor | 保持单二进制、可做 cc-clip 端到端健康和结构化状态 | 需要自行测试重连状态机                                                                                             |

结论：**参考 autossh 的状态机和运维经验，在 Go 中实现小而专用的
supervisor**。实现阶段可以把 autossh 作为开发环境中的对照或手工压力测试工具，
但它不进入 cc-clip 的安装、配置或运行路径。

OpenSSH 参数语义以官方
[`ssh_config(5)`](https://man.openbsd.org/OpenBSD-current/man5/ssh_config.5)
为准；尤其要覆盖
`ControlMaster`、`ControlPersist`、`ClearAllForwardings`、`RemoteForward`
和 server-alive 选项的版本兼容测试。本文的两步动态转发路径以 `-O forward`
的退出状态判断 bind 成败，不依赖
`ExitOnForwardFailure`；后者只用于与 legacy/单命令 `ssh -R`
路径做回归对照，不能成为正式实现的成功判据。

### 8.3 何时才需要重新评估集成

当前没有集成 autossh 的必要。只有出现下面至少一种情况，并且有实际运行数据支持时，才值得重新做架构决策：

- 内建 supervisor 在 macOS 稳定路径上长期无法达到可接受的断线检测或重连可靠性，而 autossh 能明确补足该能力；
- 目标部署环境已经统一提供并管理 autossh，且用户明确需要 cc-clip 接入现有运维体系；
- 维护自有 supervisor 的实际成本持续高于外部依赖带来的安装、状态映射和故障诊断成本。

即使满足条件，也应先提交独立设计决策，证明 autossh
backend 能保留 cc-clip 的结构化状态、端到端 health、配置迁移和更新语义；不能仅因为 autossh 已存在就直接引入。本文方案不预留或承诺该集成。

## 9. SSH 配置迁移

这里的“迁移”指本地 `~/.ssh/config` 中 cc-clip
forwarding 的所有权迁移，不是修改远端服务器的
`sshd_config`。旧机制由每条普通 SSH 连接读取
`RemoteForward`；新机制由独立 tunnel
service 创建 forwarding，因此必须避免两边同时申请同一个远端端口。

迁移前的典型配置：

```sshconfig
Host example-host
    HostName host.example.com
    User example-user
    RemoteForward 18339 127.0.0.1:18339
    ControlMaster no
    ControlPath none
```

迁移后的目标状态：

```sshconfig
Host example-host
    HostName host.example.com
    User example-user
```

`RemoteForward` 改由 tunnel service 持有。`ControlMaster no` 和
`ControlPath none`
只有在能够证明由 cc-clip 写入时才移除；无法证明时可以暂时保留，它们不会再造成端口竞争，但可能继续禁用该 host 的 SSH 连接复用。

### 9.1 新安装

托管模式不再向普通 `Host <host>` block 写入 cc-clip
`RemoteForward`、`ControlMaster no` 或
`ControlPath none`。用户现有的 HostName、User、Port、IdentityFile、ProxyJump 等仍由 OpenSSH 正常解析。

### 9.2 旧安装

当前版本没有为写入的三条 directive 加 ownership
marker，因此不能假定所有同名配置都属于 cc-clip。迁移采用保守、可回滚的切换流程：

1. 修改前继续生成可验证 backup；
2. 先确定本次迁移的 effective daemon port：优先使用显式
   `--port`，其次读取现有 daemon
   service 的启动配置并通过本地 listener/health 复验，再其次使用本次进程有效的
   `CC_CLIP_PORT`，均不存在时才采用默认端口；来源和数值写入 journal，不能把
   `18339` 硬编码为唯一 legacy 候选；
3. 只把“remote port 与 local port 均等于该 effective port、目标为 loopback”的
   `RemoteForward` 识别为 legacy
   candidate；若 service 配置、环境变量和实际 listener 不一致则停止迁移并要求显式选择端口；
4. `tunnel enable`（或调用它的 setup）向用户展示将删除的精确行；执行迁移前必须获得明确确认或已有的非交互授权；
5. `ControlMaster no`/`ControlPath none`
   只有在能证明由 cc-clip 写入时才自动删除；否则保留并由 doctor 提示可选清理；
6. 不删除同一 Host block 中其他 forward；
7. 在修改 SSH config 前完成 daemon、后台认证、control
   socket 和 service 安装预检；使用本地 token 请求本地
   `GET /tunnel/identity`，把返回的 `instance_id` 写入 pending spec 的
   `expected_instance_id` 并原子落盘。identity 读取或落盘失败时不得继续修改 SSH
   config 或安装 LaunchAgent；
8. 把移除 legacy `RemoteForward` 的候选配置写入本地 SSH
   config，同时在 state 文件中标记迁移“待提交”，然后启动托管 service；初始 master 使用
   `ClearAllForwardings=yes`，control
   client 使用空配置，因此不会重新加载该 legacy forward；
9. 如果旧的交互 SSH 连接仍持有远端端口，进入可诊断的
   `port-conflict`，提示用户关闭精确识别出的旧连接；supervisor 保留健康 master
   并每 45 秒自动重试 bind，不要求用户再次执行 continue，也不自动终止进程；
   master 自身失败时转入对应的 SSH 失败状态；
10. managed forward 必须持有远端端口，并且本地 identity、经 forward 返回的
    远端 identity 和公开 health 均通过一次完整端到端检查，才可提交 ownership
    迁移。提交前发生 transport、认证、helper、identity 检查失败或用户取消时，
    停止新 service 并恢复 SSH config。提交后的 ownership 与持续运行的 health
    分别展示；不能仅凭 bind 成功或本地 health 提前提交。

配置文件已经写入但 forward 尚未持有端口或完整 identity 检查尚未通过时，仍属于
迁移事务的“待提交”状态，需要在 state 中记录。若此时崩溃，恢复路径须根据
backup、service、master、bind 和 identity 检查决定继续提交或回滚，不能仅凭
配置文件内容或端口绑定猜测迁移已完成。

新版本若仍需要写 SSH 配置，必须使用明确的 managed block
marker 或独立状态记录，避免再次出现无法判定归属的迁移。

Phase 1B migrator 只自动编辑本地 `~/.ssh/config`
主文件中能够精确定位的 directive，不递归改写 `Include`
引入的文件。写入候选配置后必须再次运行 `ssh -G <host>` 检查 effective
config；如果仍存在来自 Include、通配 Host block 或其他来源的 cc-clip
`RemoteForward`，迁移停止并由 doctor 提示“effective
forwarding 仍存在，但来源文件未由 cc-clip 管理”。用户可以手工处理，后续版本再考虑带来源追踪的完整 SSH
config parser。

迁移 journal 是精确恢复的首选证据，但不能成为恢复 legacy 模式的单点故障。
`cc-clip tunnel restore-legacy <host> [--port <port>]` 必须提供以下两条路径：

1. journal 完整时，停止该 host 的 managed service，并按 journal 精确恢复被删除的
   cc-clip directive；
2. journal 缺失、损坏或 schema 不可读时，进入保守重建路径：先停止可识别的 managed
   service，再从显式 `--port`、已验证 daemon service 配置和本地 health 中解析端口；
   无法唯一确定时要求用户提供 `--port`。命令只向用户明确指定、位于主
   `~/.ssh/config` 的 literal `Host` block 添加一条
   `RemoteForward <port> 127.0.0.1:<port>`，不恢复 `ControlMaster no` 或
   `ControlPath none`，除非另有可靠 ownership 证据。

两条路径都必须先创建新 backup、显示精确 diff 并获得确认，保留所有无关 forward，
随后用 `ssh -G <host>` 验证 effective forwarding。若目标只存在于 Include/通配
block、主文件无法安全定位或 backup 失败，命令 fail closed 并输出可复制的最小
配置片段，不猜测来源文件。该命令即使 `tunnels.json` 和 migration journal 均已
丢失也可依赖 `<host>` 与显式 `--port` 工作，从而满足独立于 journal 的恢复要求。

### 9.3 从旧版本升级

用 `T`
表示首个实现托管隧道的版本；具体版本号由上游维护者决定。仓库历史显示，旧版本不能按同一种方式处理：

| 来源版本            | 已有状态                                                                                                          | 升级时可做什么                                                                          |
| ------------------- | ----------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| v0.1.0              | README 指导用户手工添加 `RemoteForward`；没有 setup 写入记录和 host registry                                      | 只能在用户明确指定 host 后识别候选行并确认，不能推断归属或自动枚举                      |
| v0.2.0–v0.6.2       | setup 会写入 `RemoteForward`、`ControlMaster no`、`ControlPath none` 和单个 `.cc-clip-backup`；没有 host registry | 仍需按 host 显式迁移；旧 backup 可能被后续 setup 覆盖，不能作为 ownership 证据          |
| v0.7.x–v0.8.x       | 延续上述 SSH 配置，并增加以 literal host 为 key 的 `hosts.json`；远端部署状态早于 v0.9.0 target split             | 可以用 registry 生成候选清单；隧道迁移必须与远端 integration/deploy-state 升级分开      |
| v0.9.0–v0.9.1       | SSH 配置和 `hosts.json` 与 v0.8.x 相同；已经采用 v0.9.0 target split，但 registry 仍只记录 `Codex bool`           | 本地隧道迁移与 v0.8.x 相同；不能从 registry 推断完整的 `--all`/per-target 部署意图      |
| v0.9.1 之后、T 之前 | 若仍检测到 legacy `RemoteForward` 且没有 `tunnels.json`，按 legacy 模式处理                                       | 不依赖未经核实的版本号推断，按实际配置和 capability 检测决定                            |
| T 及以后            | 有带 `schema_version` 的 `tunnels.json`、迁移 journal 和 LaunchAgent                                              | setup/update 按 schema 做幂等升级；遇到高于当前二进制支持范围的 schema 必须 fail closed |

从旧版本执行 `cc-clip update` 时，二进制更新本身不得自动改写所有 host 的 SSH
config。原因是旧版本可能没有 registry，已有 registry 也只记录 literal
host，且 updater 没有足够上下文确认每条 forwarding 的归属。安全路径是：

1. 由 T 之前的旧 updater 更新本地二进制，并维持现有交互隧道行为；旧 updater 本身不知道新 tunnel
   schema，不能负责迁移；
2. 新二进制首次执行相关 setup/tunnel/doctor 命令时，检测 registry 和 legacy SSH
   directive，只生成待迁移 host 提示；
3. 用户针对具体 host 触发隧道迁移时，再按 §9.2 完成事务式切换；
4. 已迁移 host 重复执行 setup 必须幂等，不得重复创建 service 或删除其他配置。

本设计保持远端 loopback port 和既有公开 HTTP
health 协议不变，因此“把隧道从交互 SSH 迁移到 supervisor”本身不要求重新部署远端 shim/bridge。新增 authenticated
identity 是交付级验证能力。旧远端缺少 helper 时，须先通过现有 connect/redeploy
流程部署新版 helper，完整 identity 检查通过后才能提交 tunnel ownership 迁移。
在此之前不能只凭 transport 成功提交。setup 可以按发布策略提示或编排该部署，迁移 journal 仍只记录
本地 SSH config/service 事务，两类升级不能混为一次隐式操作。

#### v0.9.0/v0.9.1 → T

1. 旧版 `cc-clip update` 只负责下载、替换本地二进制和重启现有 daemon；原
   `RemoteForward` 继续生效，因此升级动作本身不立即中断 clipboard。
2. 新二进制发现 `tunnels.json` 不存在、`hosts.json` 存在且目标 Host
   block 含 legacy `RemoteForward`，将该 host 标为“待迁移”，但不自动修改配置。
3. 用户针对该 host 触发迁移后，新版本执行后台认证预检、写入 migration
   journal、移除 legacy forward、启动 service 并验证 transport/public health
   与本地/远端 identity；缺少新版 helper 时提示独立 redeploy，不提交迁移，
   并按 §9.2 恢复配置。
4. `hosts.json` 中的 `Codex bool` 和 `LastDeployedVersion`
   仅用于展示历史信息，不能被解释为当前应执行 `--codex` 或
   `--all`；隧道迁移不触发远端 redeploy。

旧 updater 进程在二进制替换后打印的 checklist 仍可能反映旧版本逻辑；该输出
不能作为新 tunnel 已迁移或新 target 已推断的证据。新二进制后续应通过
tunnel/doctor 状态给出权威结果。

#### v0.8.0/v0.8.1 → T

本地步骤与 v0.9.x 相同，因为两代版本写入相同的三条 SSH
directive，并使用相同结构的 `hosts.json`。区别仅在远端部署代际：v0.8.x 的 deploy
state 早于 v0.9.0 target split。因此：

1. 先完成本地二进制更新，保持 legacy tunnel 可用；
2. 对具体 host 只迁移 tunnel ownership，不在同一事务中改写远端 deploy
   state、hook 或 target；
3. 是否把远端组件升级到新版本，由现有 connect/redeploy 流程单独决定，并遵循其 schema
   guard 与 target 选择规则；
4. 即使 registry 中 `Codex=true`，也不能据此替用户选择完整部署目标。

### 9.4 降级与反向迁移

升级设计还必须覆盖迁移后的降级，否则用户换回旧二进制后会同时遇到两个问题：旧版本不认识
`cc-clip tunnel run`，而原 `RemoteForward` 已从普通 SSH 配置中移除。

- **T 及以后版本之间降级**：目标二进制必须先检查
  `tunnels.json.schema_version`；能够读取时重启 service，不能读取时拒绝覆盖新状态并给出兼容版本要求。
- **降级到 T 之前**：在替换二进制前准备反向迁移——停止 tunnel
  service，把该 host 的 legacy `RemoteForward` 精确恢复到 SSH
  config，再替换二进制。只有旧二进制安装成功后才卸载 service 并归档新版本状态；如果二进制替换失败，则重新移除刚恢复的 legacy
  directive、启动原 service 并恢复升级前模式。恢复到旧版本后需要重新建立普通 SSH 连接才能重新产生 tunnel。
- **绕过 cc-clip
  updater 手工覆盖旧二进制**：旧二进制无法理解或自动恢复新状态，因此必须在升级文档中提供“降级前恢复配置”的明确步骤；不能宣称这种跨能力边界的降级是无损自动回滚。

长期降级不能直接用升级时保存的整份 `~/.ssh/config`
覆盖当前文件，因为用户可能在迁移后修改过其他 host。迁移 journal 必须记录原文件 hash、被删除 directive 的精确内容和所在 Host
block；反向迁移只恢复 cc-clip 自己移除的行。若上下文已变化且无法安全定位，则停止并要求用户确认，不能覆盖整份配置。

反向迁移、二进制替换和 service 状态切换必须共享一个可恢复的 transaction
journal。进程在任一步骤崩溃后，新版本的 updater/doctor 应能判断当前处于 managed、legacy 还是切换中状态，并完成提交或回滚。

## 10. Host 身份与状态存储

当前服务 PR 按用户输入的 SSH 目标名生成 `tunnel-id`，每个别名有独立的 job
和状态。启用配置保存在 `~/.cache/cc-clip/tunnel-specs/<tunnel-id>.json`，runtime
保存在 `~/.cache/cc-clip/tunnel-runtime/<tunnel-id>.json`，目录权限为 0700，文件
为 0600。启用配置是权威来源，管理操作串行执行，同一隧道只允许一个 supervisor。
`status` 只读展示设置、job/进程及最近健康观测，配置差异列出字段与保存值、请求值。

下文的 `tunnels.json`、跨别名 canonical identity、迁移状态及配置来源诊断属于
完整设计目标，当前服务 PR 使用上述独立文件，后续再接入 canonical registry。

现有 `~/.cache/cc-clip/hosts.json` 以用户输入的命名 SSH `Host`
为 key，适合记录部署历史，但不适合直接作为 tunnel runtime
identity：多个 alias 仍可能解析到同一 Linux 远端和端口，若分别启动 service 会互相竞争。

建议新增独立文件：

```text
~/.cache/cc-clip/tunnels.json
```

示例 schema：

```json
{
  "schema_version": 1,
  "tunnels": {
    "<tunnel-id>": {
      "ssh_alias": "example-host",
      "canonical_host": "host.example.com",
      "user": "example-user",
      "ssh_port": 22,
      "identity_files": ["~/.ssh/id_ed25519"],
      "certificate_files": [],
      "proxy_jump": "",
      "local_port": 18339,
      "remote_port": 18339,
      "expected_instance_id": "<random-persistent-id>",
      "enabled": true,
      "migration": {
        "phase": "committed",
        "journal_path": "<user-private-journal-path>",
        "backup_path": "<user-private-backup-path>",
        "source_config_hash": "sha256:<hex>"
      }
    }
  }
}
```

schema 为长期恢复预留 `none`、`pending`、`committed` 和
`rollback-pending`，但 Phase 1B 不应一次实现完整的跨版本事务引擎。Phase 1B 只实现
`none → pending → committed`、原文件 backup、被删除行及上下文记录，以及
`tunnel disable` 对本次迁移的精确恢复；自动恢复
`rollback-pending`、二进制替换与 service/config 联合回滚属于 Phase
3。任何已由当前阶段支持的操作都必须先持久化 journal，再执行外部副作用，完成后提交 phase。backup 与 journal 都位于用户私有目录且权限为
`0600`。

运行状态不频繁写回 `tunnels.json`。每个 supervisor 另写
`~/.cache/cc-clip/tunnel-runtime/<tunnel-id>.json`，记录 `runtime_state`、独立的
`conflict_class`、`last_error`、`started_at`、`stable_at`、连续启动失败次数、直接 child
PID、child 启动时间和 control
socket 路径。supervisor 启动时必须先读取并保留上一份 runtime
snapshot，再写入本次 `started_at`；否则会在判断 `conflict_class=self`
前丢失可验证的旧 child/socket 归属。`runtime_state=port-conflict`
时，`conflict_class` 的规范值为 `self`、`other-instance`、`non-cc-clip` 或
`owner-unknown`；不得把 `port-conflict:other-instance`
一类拼接字符串扩展成新的 RuntimeState。该目录权限为 `0700`、文件为
`0600`。runtime 可重建，不是用户期望状态的权威来源；即使 stale
runtime 写着 healthy/running，只要 `SpecStore.enabled=false`，`tunnel run`
就必须退出 0，不能反向启用 service。

所有会修改 `tunnels.json`、migration
journal 或 LaunchAgent 期望状态的命令共享 per-user advisory file
lock，macOS 实现使用 `flock(2)`
或等价内核原语，而不是仅凭 lock-file 是否存在判断所有权；状态通过同目录临时文件、`fsync`
和 atomic replace 提交。重复 `enable`
返回现有 spec 并复验，不创建第二个 LaunchAgent；`disable` 与进行中的 `enable`
串行化；机器睡眠或 launchd 并发拉起时，只有持锁者能改变期望状态。锁文件及临时文件沿用用户私有目录权限。

`tunnel-id` 使用固定、带 domain separation 的序列化，不能直接模糊拼接字段：

```text
SHA-256(
  "cc-clip-tunnel-id-v1\0" +
  user + "\0" + canonical_host + "\0" + ssh_port + "\0" + remote_port + "\0" +
  proxy_jump + "\0" + identity_file_count + "\0" + each_identity_file + "\0" +
  certificate_file_count + "\0" + each_certificate_file + "\0"
)
```

`ssh_port`、`remote_port` 和列表计数使用无前导零的十进制表示，其他字段采用
`ssh -G` 的归一化值；重复出现的 `identityfile`/`certificatefile`
按 OpenSSH 返回顺序逐项编码，不能只取第一项。路径先展开 `~`，再执行
`filepath.Clean`、`filepath.Abs` 和
`filepath.EvalSymlinks`；symlink 解析失败时保留清理后的绝对路径用于诊断，但将 identity
resolution 标为 incomplete，并拒绝跨 alias 自动合并。所有输入字段必须拒绝 NUL；label 使用 digest 前 16
bytes（32 个十六进制字符），完整 digest 保存在 spec 中用于碰撞校验。原始
`ssh_alias`
仍需保留为实际连接参数，避免 cc-clip 自己重建并遗漏复杂 SSH 配置。两个 alias 即使 canonical
host 相同，只要 IdentityFile/CertificateFile 不同，也必须生成不同 identity 或 fail
closed，不能让一个 service 冒用另一套凭据。

`status` 先按用户可见的 `ssh_alias` 找到已有 spec，再用当前 `ssh -G`
结果重算 canonical
identity。若 IdentityFile、CertificateFile、ProxyJump 或其他入 hash 字段已经
变化，不静默创建新 spec；status 保持只读，另行显示
`configuration: ssh-identity-changed`、列出变化字段，并提示先
`tunnel disable <host>`、确认旧 forward 已撤销后再
`tunnel enable <host>`。已经建立的 master 可以继续服务到 disable/断线，但 supervisor 下次重建连接时进入
`config-error`，不能静默改用新凭据。

如果两个 alias 解析为同一连接身份和 remote
port，默认复用同一个 spec；若解析不确定，则拒绝静默创建第二个 service，并要求用户显式选择，而不是让两个后台进程互相重连。

### 10.1 与 `hosts.json` 的关系

两个文件职责不同，互不替代：

- `hosts.json` 继续以 literal
  host 为 key，记录远端部署历史、最后部署版本和 integration 提示；
- `tunnels.json` 以 canonical tunnel
  identity 为 key，记录本机希望长期维持哪些 tunnel 以及迁移状态；
- setup 同时完成远端部署和 tunnel enable 时，分别更新两份状态；单独
  `tunnel enable/disable` 只更新 `tunnels.json`，不得删除或改写部署历史；
- `hosts list` 可以只读 join
  tunnel 状态用于展示，但不能把 alias 相同当成两个 registry 自动合并的依据；
- host alias 改名时，旧部署历史仍保留；tunnel 需要显式 rebind/rename，并重新运行
  `ssh -G` 做 identity 校验。

新建 spec 的端口按 §9.2 的 effective daemon port 解析顺序确定；`CC_CLIP_PORT`
只是其中一个输入，不得覆盖显式 flag 或已安装 daemon
service 的已验证配置。已存在 spec 使用其持久化端口，环境变量变化不得静默重写；若 daemon 当前监听端口与 spec 不一致，status 显示
`local-daemon-down`/port
mismatch，并要求显式 reconfigure，避免 supervisor 和 daemon 各自使用不同端口。

## 11. 认证与安全

- setup 先在前台完成 host
  key 接受和普通 SSH 验证，再使用与最终 LaunchAgent 相同的 ProgramArguments、关键环境和用户上下文执行一次
  `BatchMode=yes`
  one-shot 预检；仅在交互 shell 中预检通过不算成功。应通过临时 LaunchAgent/等价 one-shot
  job 验证，而不是假设交互 shell 的 agent 环境会自动进入 launchd。
- 最低保证的无人值守认证范围是：明确 `IdentityFile`
  的无口令私钥，或在 LaunchAgent 上下文中已经能以 `BatchMode=yes` 使用的 macOS
  keychain/agent 密钥。依赖交互密码、keyboard-interactive、临时 shell
  agent 或每次都要输入私钥口令的配置不受支持。
- 任意外部 agent 只有在 socket 路径对 service 稳定、setup 能在 service
  上下文复验且重启后仍可重新发现时才受支持；不得把当前 shell 的临时
  `SSH_AUTH_SOCK` 路径盲目固化到 plist。
- service 环境预检失败时禁止安装/启用 KeepAlive
  job。已安装 tunnel 在登录、重启或 keychain 状态变化后认证失败时进入
  `auth-required`，停止高频重试并给出 `cc-clip tunnel auth <host>` 修复路径。
- `tunnel auth` 在前台允许 OpenSSH 完成必要的 keychain/agent 解锁、首次 host
  key 接受或 host key 变更诊断，随后再次运行 service-context `BatchMode=yes`
  预检；只有复验成功才 restart
  supervisor。它不收集、保存或转发用户密码，也不自动接受或替换 host key。
  当前服务 PR 使用临时 LaunchAgent 执行复验；失败时保留已有服务设置。
  `auth` 只恢复原先已启用的服务，已禁用或尚未配置的服务保持禁用。
- 不设置 `StrictHostKeyChecking=no`，不自动删除 `known_hosts` 条目。
- 不启用 agent forwarding、X11 forwarding 或远端 shell；master 使用 `-N -T`。
- reverse listener 显式绑定远端 `127.0.0.1`，不得依赖服务端 `GatewayPorts`
  默认值。
- 所有本地 SSH 进程调用使用 argv，host 参数置于 `--` 后；远端固定 probe 脚本通过
  `shim.WrapRemoteShell` 进入 `/bin/sh`，不得拼入 host、token 或其他用户输入。
- 日志不得包含 token、私钥路径内容或完整环境变量。
- control socket 目录必须是用户私有目录，路径长度需满足 Unix domain
  socket 限制。
- 健康检查与重连不得绕过现有 clipboard token/notification nonce 边界。

Phase 1A 的 authenticated identity probe 曾使用普通 token 校验，并可能触发
30 天 sliding expiration，当时按维护者决定先文档化接受。当前 Phase 1B 分支
已将 identity 端点改为认证但不续期（§0.2），实际剪贴板请求保留滑动续期语义。

托管 reverse forward 会让远端 loopback
listener 从“某个交互 SSH 存活期间”扩展为“用户启用 tunnel 的整个期间”，因此暴露窗口更长；在多人共享的远端主机上，其他本机用户或进程可能持续尝试访问该端口。公开
`/health`
必须只返回最小服务状态，clipboard、identity 和 notification 操作继续要求
token/nonce，不得因为 listener 绑定 loopback 就降低认证要求。

`tunnel disable` 必须在 shutdown budget 内尝试 cancel
forward、退出 master 并终止自己记录的 SSH
child。child 退出后，远端 sshd 通常会随 TCP 连接关闭 listener，但不能把它当作同步保证；disable 应通过独立的短命 SSH
session 做一次 best-effort
listener/identity 复查。若端口仍存在或复查不可用，本地 spec/LaunchAgent 清理
继续完成，同时在结果和 runtime 诊断中记录远端端口、identity/owner 可见性和
检查时间，明确提示用户远端复查。普通用户无法可靠看到 sshd 子进程时不得伪造
或强制记录远端 PID。troubleshooting 的首选动作应是 status/identity、关闭可
识别的旧客户端连接或等待固定冲突重试，不能再把查找并杀死远端 `sshd`
当作通用恢复步骤。

## 12. update、uninstall 与故障恢复

### 12.1 update

cc-clip 自更新替换二进制后应：

1. 在替换二进制前读取目标版本能力和
   `tunnels.json.schema_version`，拒绝不安全的跨 schema 覆盖；
2. 快照所有已启用 service/spec，通过 `ServiceManager` 先 bootout/unload tunnel
   jobs、再停止 daemon job，不直接 kill 被 launchd 管理的进程；
3. 若目标版本早于 `T`，按 §9.4 准备反向迁移；
4. 替换二进制；失败时恢复旧二进制、SSH 配置和原 service 状态；
5. 通过 `ServiceManager` 启动 daemon 并等待本地
   `/health`；daemon 未健康前不启动 tunnel jobs；
6. 对仍支持托管隧道的目标版本，逐个 bootstrap/load tunnel jobs；对早于 `T`
   的目标版本提交反向迁移并提示重新建立普通 SSH；
7. 等待每个已启用 tunnel 的四项 health 契约或打印精确失败状态；
8. 如果当前没有 tunnel specs，只提示 legacy
   host 后续通过 setup 迁移，不在 update 中静默修改 SSH config；
9. post-update
   checklist 继续按 registry 的 target 状态生成远端 redeploy 命令，但不再要求已迁移 host 通过新开 SSH 来“激活”
   tunnel。

登录启动不依赖 LaunchAgent job 顺序；即使 tunnel
job 先运行，§6.2 的本地 daemon 门闩也必须阻止 SSH
child 启动。update 则主动采用 daemon-first 顺序，以缩短已知维护窗口。

macOS `ServiceManager` 应在系统支持时优先使用
`launchctl bootstrap/bootout`，仅为旧系统保留 `load/unload`
兼容分支，并用 capability 检测选择；测试应断言
install/start/stop/restart/uninstall 的生命周期结果，不应把某个已经弃用的
launchctl 子命令锁成永久 golden argv。

### 12.2 uninstall

- `cc-clip tunnel disable <host>`
  停止该 host 的 service、取消 forward 并清理 control
  socket；若 journal 证明 enable 曾移除 legacy
  directive，则精确恢复这些行后再归档 spec，新安装没有可恢复行时只禁用托管 service；
- journal 缺失或损坏时，disable 不伪造原配置；它完成 managed service 清理并明确
  提示运行 `cc-clip tunnel restore-legacy <host> [--port <port>]`，由保守重建路径
  恢复一条最小 legacy forward；
- 全局 uninstall 通过 `ServiceManager` 先 bootout/unload 所有 tunnel
  jobs、按各自 journal 恢复可安全恢复的 legacy
  directive，再停止 daemon；无法安全定位时停止并要求确认，不覆盖整份 SSH
  config；
- 不删除用户非 cc-clip SSH 配置；
- control socket 或 state 残留可由 `doctor --repair` 在确认归属后清理。

### 12.3 机器睡眠与网络切换

server-alive 检测负责让失效 SSH
child 退出，supervisor 负责重连。本机网络恢复后无需用户重开交互 SSH。若远端旧 listener 尚未释放，则进入
`port-conflict` 并低频复查，不进行 kill/restart 风暴。

### 12.4 与现有命令和文档的契约

| 现有入口                        | 托管模式下的行为                                                                                                                                                                                                                                  |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `connect`/`setup` tunnel verify | 若该 host 有 enabled tunnel spec，读取 supervisor 状态并执行端到端 identity probe；不得再把“请新开 SSH”作为成功后的常规指导。没有 spec 的 legacy host 才保留旧提示。部署用临时 SSH master 继续使用 `ClearAllForwardings=yes`，不接管长期 tunnel。 |
| `tunnel status`                 | 分开显示 ownership 与 health，例如 `ownership: managed (committed)` 和 `health: identity-helper-missing`；非 healthy 状态在下一行给出单一首选修复命令。不得用 `committed` 暗示端到端已经 healthy。SSH identity 配置漂移按 §10 显示变化字段。      |
| `hosts.json` / `hosts list`     | `hosts.json` 继续记录部署历史；展示层可按 §10.1 关联 tunnel status，但 enable/disable 不反向删除部署记录。                                                                                                                                        |
| `update`                        | 通过 `ServiceManager` 停启 daemon/tunnel jobs；旧 updater 输出的 checklist 不作为新 tunnel 状态来源。                                                                                                                                             |
| `doctor`                        | 同时报告 daemon、LaunchAgent、SSH master、remote listener、identity 和迁移 phase；明确区分权限不足、identity helper 版本不兼容、认证失败与真正的 tunnel 不可达。                                                                                  |
| README/troubleshooting          | 已迁移 host 删除“新开 SSH 激活 tunnel”的常规说明；legacy 模式、迁移中和托管模式分别给出不同指导。                                                                                                                                                 |
| `CC_CLIP_PORT`                  | 按 §9.2 的优先级参与新 spec 的 effective port 解析；已有 spec 端口变化必须显式 reconfigure，不能随环境变量静默漂移。                                                                                                                              |

## 13. 备选方案评估

### 13.1 只改成用户 `ControlMaster auto` + `ControlPersist`

优点是改动较小，多个终端可以共享同一 master 和同一条 RemoteForward。但不推荐作为最终方案：

- 隧道是否存在仍取决于“第一个 master 建立时采用了什么配置”；
- 用户已有 ControlPath/ControlPersist 策略可能冲突；
- master 超时退出后，其他仍在运行的远端进程会失去 cc-clip；
- 网络恢复和结构化健康状态仍需额外管理；
- cc-clip 会继续修改用户普通 SSH 的全局行为。

它可以作为短期文档 workaround，但不满足“setup 一次、普通 ssh 即可”的稳定性目标。

### 13.2 每个 SSH 会话使用不同 remote port

这能消除 bind conflict，却会把端口选择传播给远端 token/config、shim、clipboard
bridge 和已经运行的远端进程。端口随会话变化后，“哪个 endpoint 是当前值”比现在更复杂，因此不采用。

### 13.3 让 `connect` 的临时 master 永久保持

部署会话和运行时隧道的职责、日志、更新和错误恢复会耦合；任何一次 connect 结束逻辑变化都可能关闭生产隧道。应复用其 SSH 执行经验，但不能直接延长该对象寿命。

### 13.4 用 autossh 做原型对照

开发阶段可在隔离环境中用 autossh 对照验证
`-R`、server-alive 和重连参数；这属于测试手段，不属于产品安装或运行方案。正式产品采用 §8 的内建 supervisor。

## 14. 实施阶段

各阶段只增加能力，不重复实现 supervisor 或迁移器。Phase 1A 已交付；按维护者
2026-10-01 的决定，Phase 1B 再拆成两个 PR。

| 阶段     | 触发入口                                              | SSH config 范围                                                | journal/回滚范围                                                                                     |
| -------- | ----------------------------------------------------- | -------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| Phase 1A | 手工/前台驱动 `tunnel run` 或测试 harness             | 不修改 SSH config                                              | 只持久化 spec/runtime；无迁移副作用                                                                  |
| Phase 1B 服务 PR | 显式执行六个实验性 service 命令                       | 不编辑 SSH config；可手动注释 legacy forward 或试用空闲端口    | 无迁移 journal；持久 enabled spec 与独立 runtime；已实现并完成适用实机验收，见 §15.4                     |
| Phase 1B 迁移 PR | 在服务 API 上接入显式迁移                             | 仅精确定位的主配置 `Host` block；effective 残留则停止          | 完整 identity 检查后才提交，此前失败回滚；journal、backup、disable 精确恢复及 `restore-legacy`；尚未实现 |
| Phase 2  | setup 复用同一个 enable API，扩展 doctor/历史版本适配 | 增加旧版本 fixture、Include/通配来源诊断；仍不盲目改写未知文件 | 复用 Phase 1B journal，补齐幂等恢复和诊断，不另建 migrator                                           |
| Phase 3  | update/downgrade 联动                                 | 跨 `T` 恢复 legacy directive                                   | `rollback-pending`、二进制/service/config 联合事务恢复                                               |

### Phase 1A：可手工驱动的 supervisor 内核（PR #165 已实现并获批准）

- 已新增 tunnel spec/store、runtime store、SSH backend 和 supervisor 状态机；
- 以前台 `tunnel run <host> [--port N] [--reset]` 驱动，不安装 LaunchAgent，也不修改
  用户 SSH config；
- 已复用 `tunnel.ProbeHealth`、`RemoteHealthProbeCommand` 和
  `ClassifyRemoteProbeOutput`，保留已有五态并增加 authenticated identity 层；
- 已实现私有 control socket、`ClearAllForwardings=yes`、`-O forward/check/cancel/exit`、
  本地 daemon 门闩、重连退避、shutdown budget 和 crash-loop 状态持久化；
- 已实现 `remote-token-invalid` 保留同一 SSH master、输出 token-only 恢复指引并在
  凭据同步后原地恢复；
- 已将 identity helper 缺失、endpoint 不可用和 instance mismatch 保留为三个独立
  fail-closed 状态，并使 instance ID 发布、远端 shell 包装、master probe timeout、
  forward failure 熔断和短 control socket 目录满足最终审查要求；
- 只在 #135 已完成的文件边界上按 supervisor 的真实调用需求做了最小解耦，未进行无
  调用方的推测式重构；
- 已通过自动化和真实 Linux host 证据验证核心生命周期、fail-closed 状态分类、升级
  兼容性及 token-invalid 恢复；验证边界详见 §15.0。

### Phase 1B 服务 PR（当前分支）

- 已实现 `tunnel enable/status/restart/disable/auth/logs` 和 `run --id`；
- 已实现 per-host macOS LaunchAgent、真实 service-context 认证预检及后台可观测性；
- 已实现 enabled spec、独立 runtime、并发/幂等控制、有界清理及诊断；
- identity probe 认证但不续期，保留实际 clipboard 请求的滑动续期；
- 已补齐配置差异、本地执行位置、损坏 identity 与硬链接限制的指引及中日文 README；
- 不改写 SSH config，不迁移/恢复 `RemoteForward`，不改变 setup/update 默认行为；
- 已完成适用实机验收，自动化、真实 launchd fixture 与完整实机证据的边界见 §15.4。

### Phase 1B 迁移 PR（尚未实现）

- 复用服务 API，显式确认后仅删除可精确归属的 legacy `RemoteForward`；
- 写入 `none → pending → committed` journal；managed forward 持有端口且完整
  本地/远端 identity 检查通过之后才提交，此前任一失败恢复配置；
- handover 覆盖另一工具的持久 SSH 连接占用端口；冲突分类仅作只读诊断；
- 只回收 PID、启动时间与 control path 均验证的旧 cc-clip 本地 child，永不杀远端 sshd；
- `restore-legacy` 在 journal 完整时精确恢复，丢失/损坏时保守重建最小 legacy
  forward；不触碰无关用户 forward；
- 不在 setup/update 中自动迁移，不要求用户放弃永久支持的 legacy 模式。

### Phase 2：安全迁移与 setup 集成

- 扩展 legacy directive 的历史版本识别、来源诊断和崩溃后幂等恢复，复用 Phase
  1B 的 backup/journal/enable API；
- 覆盖 v0.1.0 手工配置、v0.2.0–v0.6.2 无 registry、v0.7.x–v0.8.x 旧 deploy
  state、v0.9.x target split 四类升级输入；
- 将托管隧道接入 setup，但初期仍要求显式 opt-in；只有在真实主机上观察并验证
  `reconnecting`、`auth-required`、`crash-loop` 后，才能另行评审新 host 是否默认
  启用；PR #165 已满足该证据前提，但没有改变默认值，default-on 仍须单独评审；已有
  host 始终不得静默迁移；
- `doctor`
  增加 owner/conflict/auth/health、Include/effective-config 和权限受限分类；
- 更新 README、troubleshooting 和 upgrade 文档。

### Phase 3：更新与降级联动

- updater 增加 tunnel schema guard、service 重启和跨 `T` 反向迁移；
- 跨能力边界降级时恢复 legacy directive；旧交互模式永久支持，不设置弃用期限。

## 15. 测试计划与验收标准

§15.1 至 §15.3 列出完整设计的测试目标，覆盖服务、迁移和后续阶段。
当前服务 PR 的自动化覆盖、实机记录与执行边界见 §15.4。

### 15.0 Phase 1A 最终验证快照（2026-09-29）

以下结果对应 PR #165 的获批提交 `c736247`，不应与后续阶段的完整交付验收混为一谈。

**自动化验证通过**：

- `go test ./... -race -count=1`、`go vet ./...` 和 `staticcheck`；
- Windows 与 macOS cross-build/vet、`checks / test-windows`、`vulncheck` 和
  `make i18n-check`；
- token-invalid 回归测试确认进入 `remote-token-invalid` 时只启动一个 SSH master，
  control path 不变，状态和日志包含 token-only 修复命令，凭据恢复后同一 master
  转为 `healthy`；
- instance ID 的空/非法文件、并发创建、权限、Windows 行为和 degraded `serve`
  启动；remote shell 包装、forward failure crash-loop、共享 probe timeout、独立
  identity 状态、长 HOME control socket 与合成 SSH 失败诊断均有回归覆盖；
- 关键 race 测试重复运行，完整 `internal/tunnelmgr` race suite 通过。

**真实主机实际验证通过**：

- 在保留既有 legacy tunnel 的同时，以独立端口和隔离 HOME 启动当前 Phase 1A
  daemon/supervisor；未修改 SSH config，也未安装 LaunchAgent；
- supervisor 达到 `healthy`，reverse-forward 数据通路、远端 `/health` 和经过认证的
  identity probe 均成功；托管 SSH 进程被中断后能够进入 `reconnecting` 并恢复；
- 旧远端 token 被拒绝后进入 `remote-token-invalid`，执行
  `connect <host> --token-only` 后自动转为 `healthy`；SSH master PID、control
  socket 和 master 数量均保持不变；
- Phase 1A 测试二进制临时替换旧安装 helper 后，原 session token 未因 instance ID
  初始化发生非预期轮换；instance-id 创建后跨 daemon 重启保持稳定；
- `auth-required` 和真实 SSH master 连续失败后的 `crash-loop` 均在真实主机上得到
  观察，完成 #108 为未来 default-on 讨论要求的关键状态证据；
- PNG 通过 managed tunnel 完成端到端传输，本地剪贴板与远端返回内容逐字节一致，
  当次 SHA-256 为
  `891da0e468a78ce655aa3c82d002efcb7d9453eaeeca17ab9d75721543f78852`；
- 275 字符隔离 HOME 的回归验证选择了 `/tmp/.ccs-…` 下长度 50 的 control socket，
  目录由当前用户拥有且权限为 `0700`，停止后 socket 和运行时目录均被移除；
- 原 connect、legacy tunnel、文本/图片剪贴板、通知和低风险 daemon 功能的升级
  smoke 通过；测试结束后本机和远端二进制、token/session 文件、端口及进程状态均
  恢复到测试前基线；
- 未执行产品环境 `--reset`，未重跑完整 setup/installer/service-manager 场景。

**仍属于后续阶段或尚未形成完整实机证据**：

- 睡眠/切网后的恢复；
- ProxyJump、Phase 1B service context 和 LaunchAgent 生命周期；
- notification 通过 managed tunnel 的完整应用层 round-trip；
- Phase 1B/2 的迁移、回滚、status/logs、setup 编排和 default-on 决策。

上述剩余项必须在相应 Phase 1B/2 能力落地后继续验收。关键失败状态的真实主机记录
已经满足未来评审 setup 默认启用的证据前提，但并不等于默认启用已经获得批准。

### 15.1 单元测试

- supervisor 状态机：首次失败、连续失败、稳定窗口重置、jitter 上下界、signal 终止；
- 直接复用 `RemoteHealthProbeCommand`/`ClassifyRemoteProbeOutput`，断言
  `ok/down/stale/unverified/unknown` 分别映射到独立 supervisor 路径，只有完整 health
  契约通过才进入 `healthy`，未知结果不得折叠；
- SSH argv：无本地 shell、回环 bind、私有 socket、无 agent/X11 forwarding；
- `ClearAllForwardings` 后通过 `-O forward` 添加唯一转发；bind
  conflict 且 master healthy 时保留并重试，其他 `-O forward`
  失败必须清理 master；
- `cancel`/`exit` control
  command 超时后执行 PID 精确的 TERM→grace→KILL，总 shutdown 不超过 8 秒；重启清理必须校验 PID 与启动时间；
- launchd plist 语义测试、service-context auth、crash-loop circuit
  breaker 和多 host
  label 唯一性；ServiceManager 测试生命周期结果而非锁死某个具体 launchctl 子命令；
- daemon-first update 顺序与 boot 时 tunnel-first 的本地 health gate；
- registry 文件锁、atomic
  replace、schema 迁移、enable/disable 并发与幂等；disabled spec 即使遇到 stale
  runtime 也必须退出 0；
- supervisor 启动时在覆盖 runtime 前保留旧 snapshot，供 `conflict_class=self`
  校验旧 child/socket；
- advisory lock 使用内核锁语义而不是 stale lock-file existence；
- `tunnel-id`
  domain-separated 序列化、NUL 拒绝、digest 前缀碰撞校验，以及 canonical
  host 相同但 IdentityFile/CertificateFile 不同的 fail-closed 行为；
- IdentityFile/CertificateFile 的
  `~`、绝对路径与 symlink 归一化；解析不完整时拒绝 alias 合并，SSH
  identity 漂移时 status 给出 disable→enable 指引；
- `hosts.json` 与 `tunnels.json` 独立生命周期、只读 join 和 alias rebind；
- legacy SSH config 精确删除、backup 失败时不修改、service 失败时回滚；
- `restore-legacy` 在 journal 完整时精确恢复，在 journal 缺失、损坏和 newer-schema
  时保守重建最小 `RemoteForward`；端口不唯一、Include/通配来源或 backup 失败时
  fail closed，且任何路径都不修改无关 forward；
- enable 必须在改 SSH config/安装 LaunchAgent 前读取本地 identity，并把
  `expected_instance_id` 原子写入 pending spec；
- Include/通配 Host 导致 effective `RemoteForward` 残留时停止自动迁移；
- v0.1.0 无 registry、v0.2.0–v0.6.2 旧 backup、v0.8.x/v0.9.x 相同 literal
  registry 但不同远端 deploy state 的迁移 fixture；
- `tunnels.json` newer-schema fail-closed 和降级时 directive 精确恢复；
- 空 control-client config 必须是实际存在且权限正确的私有普通文件；
- bind/HTTP/identity 组合的表驱动分类：bind 失败后的 identity 401 是
  `owner-unknown`，forward 成功后的 identity 401 是
  `remote-token-invalid`，forward 成功后的 instance mismatch 是
  `identity-mismatch`；
- port conflict 不触发未验证归属的 kill；`conflict_class=self`
  只有在旧 socket、PID 和启动时间均验证后才允许精确 reclaim。

### 15.2 集成测试

1. tunnel healthy 后连续打开三个普通 SSH session，均无
   `remote port forwarding failed`；
2. 关闭任意一个或全部交互 SSH，远端 `/health` 仍可通过托管 tunnel 访问；
3. 在多个普通 SSH 连接中运行需要剪贴板的远端工具，不因其他连接退出而失去 clipboard；
4. kill SSH child，supervisor 在退避后恢复；
5. 本机睡眠/切网后自动恢复；
6. 远端端口被其他进程占用时进入 `port-conflict`；同一 instance、其他 cc-clip
   instance、非 cc-clip listener 和权限不足分别映射到规范
   `conflict_class`；同一 instance 但无法验证进程归属时也不自动 kill；
7. `conflict_class=self`
   且旧 child 归属可验证时执行有界 reclaim 并立即 bind；归属不可验证时保持同一 healthy
   master、仅固定间隔重试。master 断开、认证失效或 host
   key 失败时切换到对应状态；关闭旧连接后无需再次执行命令并自动从 pending 提交为 healthy；
8. 远端没有新版 identity helper 时进入 `identity-helper-missing`，helper 存在但
   endpoint 不可用时进入 `identity-endpoint-unavailable`，两者都不假报 tunnel
   down 或 healthy；部署 helper 后自动恢复；
9. 我方 forward 已成功且公开 health 正常，但 token 失效时进入
   `remote-token-invalid`，token-only 刷新后恢复且 SSH master
   PID 不变；bind 已失败时相同的 401 不得误导为 token-only；
10. daemon 重启与常规更新前后 `instance_id` 保持不变；bind
    conflict 下的 authenticated identity mismatch 分类为
    `other-instance`，我方 forward 成功后的 mismatch 分类为
    `identity-mismatch`，二者都不能标为 healthy；
11. 交互 shell 认证可用但 LaunchAgent 上下文不可用时拒绝 enable；`tunnel auth`
    完成首次 host key 接受/认证并复验成功后才能启动；
12. 使用 `ProxyJump` 的目标能够建立 master、添加 forward 并通过 identity smoke
    test；
13. 更新二进制时通过 ServiceManager 停启 daemon/tunnel
    jobs，所有已启用 tunnel 恢复 healthy；
14. 从无 registry 的旧版本升级时不批量修改未知 host，针对指定 host
    setup 后迁移成功；
15. 降级到 `T` 之前时先恢复 legacy
    `RemoteForward`，且不覆盖迁移后新增的其他 SSH 配置；
16. supervisor 已退出到 `crash-loop` 时，`tunnel restart`
    能重新拉起 LaunchAgent、重建 child 并保留 spec；配置仍无效时保持
    `config-error`；
17. disable 的 control
    command 超时或远端 listener 暂时残留时，本地清理仍完成并输出端口/identity 诊断，不伪造远端 PID；
18. clipboard image/text 与 notification 均通过托管 tunnel 工作。
19. 删除或损坏 migration journal 后运行
    `tunnel restore-legacy <host> --port <port>`，在不恢复无 ownership 证据的
    ControlMaster 指令、不覆盖其他配置的前提下重新得到可用 legacy tunnel；
20. Phase 1A 的 manual driver 能在完全不写 SSH config、不安装 LaunchAgent 的情况下
    建立、探测、重连和停止 tunnel；Phase 1B 再为同一 supervisor 加入 service 生命周期；
21. PR #165 已在真实主机上覆盖 `reconnecting`、`auth-required`、`crash-loop` 的进入
    与恢复；Phase 1B 还须补充对应的 status/logs 输出和 LaunchAgent 恢复证据，任何
    setup 默认值变化仍须独立评审。

### 15.3 用户验收

满足以下条件才认为托管隧道实现达到可交付状态：

- 用户按发布策略完成一次 `tunnel enable` 或由 `setup` 编排启用，之后只输入
  `ssh <host>`；
- 初期 setup 不默认迁移；用户拒绝 opt-in 或 service-context 认证不可用时，legacy
  模式继续正常工作且不显示弃用警告；
- 用户无需理解哪个 SSH 会话持有端口；
- 新开、退出、重连多个 SSH session 不改变 clipboard 可用性；
- `cc-clip tunnel status` 能在一屏内给出可执行的故障原因；
- ownership 已 committed 但 health 尚未通过时，status 必须分两行展示两者，并给出明确修复命令；
- 旧配置迁移可回滚且不影响非 cc-clip SSH forward；journal 丢失时仍可通过一条
  文档化命令恢复最小 legacy 配置。

### 15.4 Phase 1B 服务 PR 验证与验收记录

本节记录服务 PR 的自动化、本地 launchd fixture、真实远端和文件系统验证。
各项结果注明执行方式、对应构建及覆盖边界。迁移 PR 的范围见 §14，托管隧道
仍为 **experimental**。

#### 15.4.1 自动化覆盖与检查要求

现有测试使用隔离状态，按场景替换 SSH 或 launchctl。真实 daemon 的 token 测试
使用实际 HTTP 和 token 文件，但仍替换 SSH，不构成真实主机转发证据。

| 验收要求                                  | 代表性测试                                                                                                                                                                       | 验证方式                |
| ----------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------- |
| enable/disable、启用失败回滚及 host 隔离  | `TestTunnelEnablePreflightPersistenceAndIdempotence`、`TestTunnelEnableFailureDoesNotEnableJob`、`TestTunnelDisablePersistsBeforeStopAndIsolatesHosts`                           | 自动化                  |
| 并发管理及重复 runner 排除                | `TestTunnelConcurrentEnableAndDisableAreSerialized`、`TestRunServiceExcludesDuplicateSupervisor`                                                                                 | 自动化                  |
| 认证或 host-key 失败保持启用意图          | `TestTunnelAuthFailurePreservesServiceAndConfiguration`、`TestTunnelAuthenticationPreflightContextAndCleanup`                                                                    | 自动化                  |
| daemon 健康门闩及低频认证等待             | `TestSupervisorLocalDaemonGatePreventsSSHStart`、`TestSupervisorAuthRequiredUsesAttentionWait`                                                                                   | 自动化                  |
| crash-loop 恢复及无效 runtime/schema 拒绝 | `TestTunnelRestartRecoversOnlyValidatedRuntime`、`TestRunServiceConfigurationErrorDoesNotRelaunch`、`TestSupervisorCrashLoopStopsRestarting`                                     | 自动化                  |
| token 拒绝、原地修复及 probe 不续期       | `TestSupervisorTokenRepairWithRealDaemonDoesNotRenew`、`TestTunnelIdentityDoesNotRenewTokenButClipboardStillDoes`、`TestTunnelIdentityAuthenticationStillFailsClosed`            | 真实 HTTP；替换 SSH     |
| 配置差异及只读 status                     | `TestTunnelStatusReportsRuntimeSpecDifferencesWithoutMutation`、`TestTunnelServiceIdentityMismatchNamesValuesAndLocalMachine`                                                    | 自动化                  |
| 过时健康观测、job/进程区分及实验性帮助    | `TestTunnelStatusIsReadOnlyAndDoesNotTrustStaleHealth`、`TestTunnelServiceHelpDocumentsExperimentalCommands`                                                                     | 自动化                  |
| 有界归属进程清理及禁用意图生效            | `TestBackendStopSignalsOnlyOwnedChild`、`TestRunServiceDesiredStateStopsOwnedChild`、`TestWaitStoppedIsBoundedByContext`                                                         | 自动化                  |
| 配置、日志和 control 资源私有性           | `TestTunnelLaunchAgentRejectsForeignPlistAndSymlinkLog`、`TestRunServiceRejectsUnsafeRuntimeDirectoryBeforeLocking`、`TestTunnelLogsTailAndPrivateFileValidation`                | 自动化                  |
| identity 损坏及硬链接不可用               | `TestLoadOrCreateRejectsCorruptIdentityWithoutReplacingIt`、`TestLoadOrCreateHardLinkFailureLeavesIdentityUnavailable`、`TestLoadInstanceIDOrUnavailablePreservesDegradedDaemon` | 故障注入及 HTTP handler |

服务 PR 需完成全仓 `go test ./... -race -count=1`、`go vet ./...`、`staticcheck`，
以及 Linux、Windows、macOS 各自 amd64/arm64 的 build/vet、适用的格式与 lint 检查
和 `make i18n-check`。跨平台 build/vet 不等于在这些系统上执行测试；普通 suite
保留 Xvfb 和 opt-in launchd 测试的跳过条件，需分别说明实际覆盖。

#### 15.4.2 真实 launchd 的本地 fixture 验证边界

两个 opt-in 测试使用真实 GUI launchd domain，通过 `CC_CLIP_TEST_LAUNCHD=1`
单独运行。

- `TestTunnelAuthenticationRealLaunchAgent` 使用生产认证 helper 和 SSH backend，
  核对 HOME、PATH、umask 及未继承终端临时 agent 环境，再替换为本地 SSH fixture。
  它验证预检上下文及 `ok/auth-required/host-key-error` 结果，不能证明实际密钥、
  keychain/agent 或网络认证可用。
- `TestTunnelLaunchAgentRealLifecycle` 安装独立命名的 job 和隔离的本地可执行文件，
  检查注册、运行 PID、重复安装不产生新进程、已核对归属的 fixture 进程退出后重启、
  显式停止/重新安装、卸载及保留诊断文件。它不运行真实 supervisor 或远端 SSH，
  也不覆盖重启系统后登录、睡眠或切网恢复。

#### 15.4.3 真实远端验收记录（2026-10-02）

2026-10-02 在隔离环境中完成后台服务的实机验收。以下结果对应当时的构建，
重放后分支的自动化复验见 §15.4.5。

##### 本次构建与隔离环境

- 验收构建基于 `6089c56`，另含 UTF-8 剪贴板修复 `734244e`、对应回归测试，
  以及 identity probe 不续期的 CLI 帮助修正。
- 实测发现 `pbcopy` 在缺少 UTF-8 locale 时会返回成功，但中文剪贴板为空。
  该行为也存在于上游基线 `2f60996`。历史构建通过 `LC_ALL=en_US.UTF-8` 修复，并新增中文、
  日文多行文本回归测试。复现和修复记录见 [PR #189](https://github.com/ShunmeiCho/cc-clip/pull/189)。
- 当前分支沿用上游 #195 的 `LC_CTYPE=UTF-8` 实现，代码核对和复验记录见
  §15.4.5。
- 本地 macOS 26.6.2 / arm64；远端 Linux / amd64、OpenSSH 9.6p1；Go 1.25.13。
- 两端最终二进制版本为 `phase1b-acceptance-6089c56-utf8-fix`。
- Darwin 二进制 SHA-256 为
  `eeffd83510f619caa053e1d901c2b3cdd87aebf38cacef8aaa948d0d6b449ab7`。
- Linux 二进制 SHA-256 为
  `087d81e5aa41a859be4581d721047dd7bc1c32fad60e3177c2b4f384b727c001`。

通过 Tailscale 访问 Linux 主机，使用独立的非特权 OpenSSH listener 和隔离的远端
`HOME`，helper 位于该环境的 `$HOME/.local/bin/cc-clip`。复用原 Linux 账号，
没有创建独立系统账号，也没有修改系统 sshd、原 helper 或原凭据。

配套 macOS daemon 作为真实 GUI LaunchAgent 运行，使用隔离的 `HOME`、token、
instance identity、receipt 和日志。daemon 与 managed reverse forward 均使用
28441 端口。服务 job 和认证 job 使用实际生产实现及匹配的 launchd 环境。
原 18339 端口的 daemon 和现有 SSH 连接持续保留；诊断 SSH 清除全部 forwarding，
因此不能在测试端口提供另一条转发链路。

##### 场景要求与实测证据

| 场景                 | 必须观察的行为                                                                                           | 本次实测证据                                                                                                                                                                                                                            | 状态             |
| -------------------- | -------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------- |
| 启动、status 和 logs | 达到完整认证的端到端 `healthy`；区分启用意图、job 注册及 supervisor 运行状态                             | 显式密钥通过真实 launchd 预检；各状态分别可见，日志包含真实转换。重复 enable 保持同一 supervisor 和 SSH master。                                                                                                                        | 通过             |
| 配置差异             | 显示持久配置与请求值的差异，status 不改写配置                                                            | 请求不同端口时显示 `persisted=28441, requested=28442`，持久化 spec 字节不变。                                                                                                                                                           | 通过             |
| restart/disable      | restart 更换 supervisor/SSH child；disable 停止归属连接、listener/control 和 job，保留诊断状态           | restart 更换两类进程并恢复健康；disable 有界完成，移除 job、归属进程和 control 资源，远端测试 listener 消失；清理测试环境之前确认 disabled spec、stopped runtime 和日志仍保留。                                                         | 通过             |
| 真实后台认证         | 服务可用的密钥或 keychain/agent 通过预检；仅终端可用的认证不能启用 job；`auth` 经 launchd 复验后恢复服务 | 显式密钥成功；临时终端 agent 能真实 SSH 登录，但后台 enable 失败且未安装持久 job。实际 `tunnel auth` 完成前台认证、launchd 复验和恢复。本入口未配置 password-only 或 keychain 专属认证，未将它们记为实测通过。                          | 通过（适用配置） |
| host-key 保护        | 未知或变更的 key 被严格拒绝；交互确认后重新验证服务上下文                                                | 两类错误均被预检拒绝，未安装或替换 job。已有服务的实际 `tunnel auth` 提示确认测试 key，独立核对生成的 fingerprint 后确认；后台严格复验及恢复成功。仅使用隔离的 known-host 文件。                                                        | 通过             |
| daemon 健康门闩      | daemon 停止时不反复创建 SSH master；以原端口和 identity 恢复后服务恢复                                   | 停止配套 daemon 并仅终止已核对归属的 SSH child，35 秒内没有新 child；同端口和 identity 的 daemon 重启后恢复健康。                                                                                                                       | 通过             |
| 重连及 crash-loop    | 仅中断归属 child，观察重连；连续启动失败打开 breaker，修复后显式 restart                                 | 归属 child 被终止后新私有 master 恢复健康。测试别名的 transport 故障造成五次 SSH 早退，breaker 打开，已注册 job 的 supervisor 停止而不重新拉起；修复并 restart 后恢复。                                                                 | 通过             |
| 睡眠/切网恢复        | 睡眠唤醒或切网后恢复完整认证健康，保留 enabled 配置                                                      | 第一次实际睡眠仅 2 秒，按短时覆盖记录；第二次 `pmset` 记录 12 秒和 54 秒睡眠，`14:05:05 UTC+08:00` 完整唤醒。唤醒后的新 supervisor 观测和独立远端认证 identity 检查均健康，enabled 配置及 GUI job 保留。本次覆盖睡眠分支，未另行切网。    | 通过             |
| 远端 token 原地修复  | token 失效时保留原 master；token-only 同步后原地恢复                                                     | 仅替换隔离远端 token，进入 `remote-token-invalid`，原 master 保持运行；实际 `connect --token-only --no-notify --port 28441` 恢复健康，supervisor、master PID 和 control path 不变。                                                     | 通过             |
| 本地 token 过期      | 仅 identity probe 不续期；过期拒绝；重启同步可恢复；剪贴板请求保留滑动续期                               | 95 秒 TTL 下持续真实本地/远端 identity 探测及后台监测，token 文件不变且按期过期；过期及错误 token 返回 HTTP 401。恢复正常 TTL、原端口和 identity 并同步、restart 后恢复；60 秒 TTL 的后半段实际远端 `paste` 延长 expiry，token 值不变。 | 通过             |
| 托管链路剪贴板       | 排除无关 SSH forwarding，验证图片到远端和文本到本地，恢复原剪贴板                                        | 远端 `paste` 取得真实 macOS PNG，与本地剪贴板 reader 字节的 SHA-256 一致；修复后远端 `copy` 的中文 UTF-8、多行文本精确匹配。原 pasteboard 的全部 item、类型和字节已恢复并核对。                                                         | 通过             |
| 托管链路通知         | 专用端口发送唯一标题通知，确认实际 UI 和 receipt                                                         | 远端通知经 managed forward 到达，写入对应 host-bound receipt；用户确认通知中心出现了唯一验收标题。未确认横幅是否弹出，也未验证声音；该测试使用默认 urgency=1，未显式指定 `--sound`。HTTP 成功未单独作为 UI 投递证明。                   | 通过             |
| 清理及 legacy 恢复   | 禁用测试服务、恢复配置和 daemon 设置，确认普通 SSH forwarding 可用                                       | 原本地 daemon PID 不变；SSH 配置字节及权限匹配基线；原远端 helper、token、session ID 和 SSH 配置 hash 匹配基线。测试 job、daemon、远端 sshd、listener 和测试文件已清理，原 legacy 端口经普通 SSH 访问仍返回健康响应。                   | 通过             |

##### 自动化检查与复测范围

2026-10-02 使用 Go 1.25.13 完成全仓 race 测试、vet、staticcheck，
以及 darwin/linux/windows 各自 amd64/arm64 的 build/vet，结果均通过。
当时的 `TestDarwinTextClipboardUnderNonUTF8Locale`、`gofmt`、
`make i18n-check` 和差异检查也通过。

服务生命周期、后台认证和 crash-loop 实测先在修复前的构建上完成。
UTF-8 修复后的最终构建再次覆盖真实 enable、剪贴板、通知、token 过期及恢复、
睡眠唤醒、disable 和环境恢复。各场景的观察结果见上表。

#### 15.4.4 真实 exFAT 文件系统补充验证（2026-10-02）

使用服务分支 `569eee6` 构建的 `phase1b-exfat-569eee6`，通过 macOS `hdiutil`
创建并挂载 128 MiB 隔离映像。`diskutil info` 确认文件系统为 ExFAT。
测试二进制和配套 HOME 留在 APFS 上，仅通过 `CC_CLIP_TOKEN_DIR` 将测试状态
指向卷内独立目录；daemon 使用空闲端口 28443，不安装新的 LaunchAgent，
不访问远端主机，不读取或修改真实剪贴板。

| 场景              | 实测结果                                                                                                          |
| ----------------- | ----------------------------------------------------------------------------------------------------------------- |
| 文件系统能力      | 创建文件及 chmod 返回成功；硬链接返回 errno 45（Operation not supported）                                         |
| 无已有 identity   | daemon 继续运行；`/health` 为 200，identity 未认证为 401、有效认证为 503、错误 token 为 401                       |
| 失败诊断与清理    | 日志包含目标 identity 路径、硬链接支持指引及降级说明；没有最终 identity、半写入文件或 identity/token 临时文件残留 |
| 已有有效 identity | 从正常文件系统复制测试 identity 后能加载，认证 identity 为 200；重启后字节保持不变，无重新创建或临时文件残留      |
| 正常文件系统恢复  | 切回 APFS 测试状态目录后能创建有效 identity，权限为 0600，认证 identity 为 200                                    |

该 exFAT 卷上 chmod 返回成功，但探测文件请求 0600 后报告为
0700，因此不能据此声称该卷满足私有凭据目录要求。用户仍应将 token/identity
目录放在支持硬链接且能落实 0700/0600 权限的文件系统上。本测试确认已知限制下
的降级和既有 identity 读取，不扩大 exFAT 支持承诺，也不代表所有 FUSE 实现。
FUSE 未实测。

测试结束后停止全部测试 daemon、确认 28443 无 listener、卸载映像，并删除
映像、二进制、脚本、测试凭据及日志。原 daemon PID 未变；原 LaunchAgent plist、
SSH 配置和安装二进制 hash 与测试前一致，工作区保持干净。

#### 15.4.5 重放后的自动化复验（2026-11-13）

复验对应服务代码提交 `d1a5b80`，上游基线为 `c7e25a1`（#195）。
服务 Go 代码与原服务分支 `569eee6` 的差异核对通过，五个 clipboard/locale
实现及测试文件与上游一致。当前构建直接采用 #195 的 UTF-8 修复。

使用 Go 1.25.13 在 macOS 上执行以下检查，结果均通过。

```bash
go test ./... -race -count=1
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
make i18n-check
git diff --check
```

测试包含上游 #195 的 Darwin 回归测试。六个平台组合
（darwin/linux/windows × amd64/arm64）的 `go build ./...` 与 `go vet ./...`
均通过，修改的 Go 文件通过 `gofmt`。

另使用 Go 1.26.11 和 govulncheck v1.7.0 查询官方漏洞库，扫描通过。
当前代码可达或已导入包中的漏洞数为零，另有一条记录只涉及声明依赖模块中的
未引用代码。

本次执行范围为差异核对与自动化复验。真实远端、睡眠唤醒、剪贴板与通知 UI、
LaunchAgent 生命周期及 exFAT 的实测证据对应 §15.4.3 和 §15.4.4 的历史构建。

## 16. 最终决策摘要

1. 只要同时存在多个交互 SSH 连接，或远端 agent/通知 hook 的生命周期可能长于实际持有 forwarding 的连接，把长期数据通路绑定到该连接就是架构缺陷。
2. 将 reverse
   tunnel 提升为 cc-clip 明确管理的 per-host 资源，并与交互 SSH 解耦。
3. 内部使用私有 ControlMaster/control socket，不改变用户普通 SSH 的复用策略。
4. 使用 macOS LaunchAgent 管 supervisor，使用 Go supervisor 管 SSH
   child、健康和退避。
5. autossh 只作为行为设计参考和原型对照，不进入产品运行路径，也不复制其源码。
6. 自动恢复只重建自己拥有的进程；端口冲突先诊断，绝不自动杀远端 sshd。
7. service-context 无人值守认证和本地 daemon 健康是启动 tunnel 的硬前提；不能用交互 shell 预检替代。
8. 公开 `/health` 层复用 #114 已发布的五态 remote probe，并逐态 fail closed；
   authenticated identity endpoint 再确认 tunnel 指向预期 daemon，不能重写或压缩
   既有探测状态。
9. `tunnels.json`
   保存期望状态和迁移 phase，独立 runtime 文件保存可重建状态；PR #165 的
   Phase 1A 已实现可手工驱动的 supervisor/SSH backend/state store；Phase 1B 的
   服务 PR 交付 LaunchAgent 和 opt-in 生命周期，迁移 PR 再交付最小 journal 与
   回滚。跨版本完整事务在 Phase 3 实现。
10. 托管模式初期显式 opt-in；legacy 交互 forwarding 永久支持；已有 host 永不静默
    迁移。PR #165 已提供关键失败状态的真实主机证据，但新 host 的 setup 是否默认
    启用仍须另行评审。
11. journal 是精确恢复证据而不是单点依赖；`restore-legacy` 在 journal 丢失或损坏时
    仍能保守重建一条最小 legacy `RemoteForward`，并且永不触碰无关用户 forward。
12. #20/#135 是零行为变化的代码整理，只降低后续改动与评审成本，不计为 Phase 1A
    功能实现；实际解耦由 supervisor 的真实调用方需求驱动。
