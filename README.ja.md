<!-- i18n-source: README.md @ 7694090fe90162db9cb66e4a2087ce0b4fab8e7f -->

<p align="center">
  <a href="README.md">English</a> ·
  <a href="README.zh-CN.md">简体中文</a> ·
  <b>日本語</b>
</p>

<p align="center">
  <img src="assets/readme/hero.svg" width="100%" alt="cc-clip はループバック限定の SSH トンネル経由でローカルクリップボードをリモートの AI coding agent に転送します">
</p>

> これは英語版の日本語訳です。内容に差異がある場合は [English 原文](README.md) を正とします。この翻訳は英語版のメインラインより遅れている場合があります。

<p align="center">
  <a href="https://github.com/ShunmeiCho/cc-clip/releases"><img src="https://img.shields.io/github/v/release/ShunmeiCho/cc-clip?color=F97316" alt="最新リリース"></a>
  <a href="https://github.com/ShunmeiCho/cc-clip/actions/workflows/ci.yml"><img src="https://github.com/ShunmeiCho/cc-clip/actions/workflows/ci.yml/badge.svg" alt="CI ステータス"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-18181B.svg" alt="MIT ライセンス"></a>
</p>

<p align="center">
  <b>SSH 経由のリモート Claude Code、Codex CLI、opencode、Cursor セッションに画像を貼り付け、さらにターミナルの折り返し改行なしでテキストをローカルへコピーバックできます。</b><br>
  オプションの統合により、完了通知と承認通知をデスクトップへ届けられます。
</p>

<p align="center">
  <a href="#クイックスタート">クイックスタート</a> ·
  <a href="#ターゲットを選ぶ">ターゲットを選ぶ</a> ·
  <a href="#仕組み">仕組み</a> ·
  <a href="#ドキュメント">ドキュメント</a>
</p>

<p align="center">
  <img src="docs/marketing/demo-quick.gif" alt="cc-clip のインストール、セットアップ、リモート画像貼り付けを示すターミナルデモ" width="720">
  <br>
  <em>インストール → セットアップ → SSH を開く → 貼り付け。</em>
</p>

> **v0.8.x からアップグレードしますか？** v0.9.0 では、`--codex` が Codex 専用になりました。同じホストで Claude 統合も必要な場合は
> `--all` を使用してください。詳しくは
> [アップグレードガイド](docs/upgrading.md#upgrading-from-v08x-to-v090)を参照してください。

## クイックスタート

これは安定版の macOS から Linux への経路です。以下が必要です。

- macOS 13 以降
- `curl`、`bash`、および `xclip` または `wl-paste` がある Linux リモート
- `~/.ssh/config` 内の名前付き `Host` エントリ

### 1. インストール

```bash
curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh | sh
cc-clip --version
```

インストーラーから求められた場合は、続行する前に `~/.local/bin` を `PATH` に追加してください。

### 2. 1 台のホストをセットアップ

```bash
cc-clip setup myserver
```

デフォルトのターゲットは Claude Code です。セットアップでは、ローカル依存関係の確認、ループバック
`RemoteForward` の追加、ローカルデーモンの起動、リモート shim（互換レイヤー）のデプロイを行います。
Codex、opencode、通知には、次のセクションにあるターゲット flag を使用してください。

### 3. 新しい SSH セッションを開く

```bash
ssh myserver
```

coding agent を起動し、通常どおり貼り付けてください。新しい SSH 接続が重要です。
この接続がリバーストンネルを開いた状態に保ちます。

### 4. 経路全体を検証

画像を Mac のクリップボードへコピーし、ローカルで次を実行してください。

```bash
cc-clip doctor --host myserver
```

## ターゲットを選ぶ

セットアップごとに selector を 1 つ選んでください。selector がない場合、cc-clip は Claude Code を設定します。

| リモートワークフロー | セットアップコマンド | 画像貼り付け | デスクトップ通知 | 追加要件 |
|---|---|:---:|:---:|---|
| Claude Code | `cc-clip setup myserver` | はい | はい | `xclip` または `wl-paste` |
| Codex CLI のみ | `cc-clip setup myserver --codex` | はい | はい | Xvfb。セットアップにリモートの `sudo` が必要な場合があります |
| すべての統合 | `cc-clip setup myserver --all` | はい | はい | Codex には Xvfb |
| opencode | `cc-clip setup myserver --opencode` | はい | はい | `xclip` または `wl-paste` |
| Antigravity | `cc-clip setup myserver --agy` | いいえ | はい | 通知統合のみ |
| Cursor CLI | `cc-clip setup myserver --cursor` | はい | いいえ | Cursor を実行する shell に `DISPLAY` または `WAYLAND_DISPLAY` が設定されていること |

Codex ターゲットでは、cc-clip は `apt` または `dnf` を使って Xvfb のインストールを試みます。
passwordless `sudo` が使えない場合は停止し、正確なインストールコマンドを表示します。
そのコマンドを手動で実行してから、セットアップを繰り返してください。

Claude、opencode、Cursor の経路では、リモートの `xclip` または `wl-paste` shim を使用します。Codex は
X11 を直接読み取るため、そのターゲットでは代わりに Xvfb と `cc-clip x11-bridge` を追加します。

Cursor にはデプロイでは満たせない前提条件が 1 つあります。Cursor を実行する shell に
`DISPLAY` または `WAYLAND_DISPLAY` が設定されている場合のみ、Cursor はクリップボードを
読み取ります（`echo $DISPLAY` で確認）。`ssh -X myserver` で接続するか、既存のディスプレイを
エクスポートしてください。cc-clip が意図的に自前の値を注入しないのは、背後に X サーバーの
ない `DISPLAY` は、その shell のほかのすべてのツールのクリップボードフォールバックを
確実に壊してしまうからです。また Cursor は約 4 秒でクリップボードヘルパーの待機を
打ち切るため、遅いリンクで大きな画像を転送する場合はリモート shell の rc に
`export CC_CLIP_FETCH_TIMEOUT_MS=3000` を追加してください。Cursor の通知統合は未対応です。

リモートの `cc-clip` がすでにパッケージマネージャの管理下にある場合は、
`cc-clip setup myserver --use-remote-bin` でその所有権を保てます。セットアップはリモートの
**ログイン shell** の PATH で `cc-clip` を解決し（そのため `~/.nix-profile/bin` や pipx、
asdf のインストールも見つかります）、バージョンとハッシュを記録した上で、代替バイナリを
アップロードせずに通常の統合セットアップを行います。

このモードはホストのデプロイ状態に記録されます。以降の `cc-clip connect` 実行は——
`cc-clip update` が提示する `connect <host> --force` の行も含めて——フラグなしで
パッケージ管理バイナリを使い続けます。`--local-bin` でデプロイすると、ホストは
アップロード方式へ戻ります。同一実行内でこのフラグと `--local-bin` は併用できません。

> opencode と Antigravity の統合生成はテストでカバーされていますが、代表的なマシンでの
> ホストイベント配信はまだ smoke test されていません。
> 結果を [報告してください](https://github.com/ShunmeiCho/cc-clip/issues)。

### その他のローカルプラットフォーム

| ローカルマシン | リモート | サポートレベル | 推奨経路 |
|---|---|---|---|
| macOS 13+ | Linux | 安定版 | `cc-clip setup HOST` |
| Windows 10/11 | Linux | 実験的 | [`send` / `hotkey` クイックスタート](docs/windows-quickstart.md) |
| Linux | Linux | 手動デーモン | `cc-clip serve` を実行し、別の shell で `cc-clip setup HOST` を実行 |

Windows サポートは引き続き実験的です。まずは [Windows クイックスタート](docs/windows-quickstart.md)の
明示的なアップロードと貼り付けのワークフローを使用してください。任意で有効にする
直接 RemoteForward 転送もあります（v0.9.1 以降）が、これはデフォルトではありません。

## 仕組み

cc-clip は transport を狭く保ち、SSH 接続内に限定します。

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

1. ローカルデーモンは、リモート側から要求されたときだけクリップボードデータを読み取ります。
2. SSH はそのデーモンをリモートのループバックで公開します。public listener は作成されません。
3. Claude Code と opencode は、透過的なクリップボード shim を経由してアクセスします。
4. Codex は `xclip` を呼び出さず X11 を直接読み取るため、Xvfb のクリップボード owner を経由してアクセスします。
5. 認識されない `xclip` / `wl-paste` 呼び出しは、リモートの実体ツールへそのまま渡されます。

## 通知

クリップボードデータと agent event は SSH トンネルを共有しますが、別々の認証情報を使用します。
`cc-clip connect` は次を接続できます。

| ソース | 統合 | イベント例 |
|---|---|---|
| Claude Code | 管理対象フック | 停止、承認リクエスト、画像貼り付け |
| Codex CLI | `notify` コマンド | タスク完了 |
| opencode | 生成された plugin | セッション idle |
| Antigravity | 生成された plugin | agent 停止 |

adapter の詳細、手動設定、nonce 登録、診断については、
[SSH 通知](docs/notifications.md)を参照してください。

## セキュリティモデル

| 境界 | 保護 |
|---|---|
| ネットワーク | デーモンと転送ポートはループバックのみに bind |
| クリップボード | 30 日間の sliding expiration を持つ Bearer token |
| 通知 | 接続ごとに別の nonce |
| プロセス一覧 | token と hook payload をコマンドライン引数に置きません |
| fallback | 無関係なクリップボード呼び出しは、リモートの実体 binary へそのまま渡されます |

ループバックは、同じリモートホスト上のユーザー間で共有されます。token file の mode は
`0600` ですが、cc-clip は同じ Unix account として動作する別プロセスや、ファイルを読み取る
別プロセスからの防御は行いません。共有または信頼できないホストで cc-clip を使用する前に、
明示的な [threat model](SECURITY.md) を確認してください。

## 主要コマンド

| コマンド | 用途 |
|---|---|
| `cc-clip setup HOST [target]` | 初回の依存関係、SSH 設定、デーモン、デプロイ |
| `cc-clip setup HOST --use-remote-bin` | リモートバイナリがパッケージ管理されているホストを設定 |
| `cc-clip connect HOST --force [target]` | ホストの修復または完全な再デプロイ |
| `cc-clip connect HOST --token-only` | rotation または期限切れになった token の同期 |
| `cc-clip doctor --host HOST` | end-to-end 診断 |
| `some-command \| cc-clip copy`（リモートで実行） | リモートの出力をターミナルの折り返しを経ずにローカルのクリップボードへコピー |
| `cc-clip status` | ローカルコンポーネントの状態 |
| `cc-clip hosts list` | 既知のホスト registry |
| `cc-clip update --check` | 公開済み release channel の確認 |
| `cc-clip update` | 最新の公開済みリリースをインストール |

正確なコマンド一覧は `cc-clip --help` を実行して確認してください。
[コマンドガイド](docs/commands.md)では、一般的な flag と environment variable を説明しています。

### 設定

| 設定 | デフォルト | environment variable |
|---|---:|---|
| トンネルポート | `18339` | `CC_CLIP_PORT` |
| token の有効期間 | `30d` | `CC_CLIP_TOKEN_TTL` |
| debug logging | オフ | `CC_CLIP_DEBUG=1` |

## トラブルシューティング

まず組み込み診断を実行してください。

```bash
cc-clip doctor --host myserver
```

最も一般的な 3 つの修正方法は次のとおりです。

- **トンネルを利用できない:** 新しい `ssh myserver` セッションを開いたままにしてください。
  `RemoteForward` は、SSH 接続が所有している間だけ存在します。
- **デーモン再起動後に token が拒否される:**
  `cc-clip connect myserver --token-only` を実行してください。
- **Codex にクリップボードがない:** 注入された `DISPLAY` を読み込むため、新しい SSH セッションを開いてください。
  Xvfb または x11-bridge がない場合は、
  `cc-clip connect myserver --codex --force`（または `--all --force`）を実行してください。

新しい SSH tab に `remote port forwarding failed for listen port 18339` と表示される場合、
別の live または stale SSH セッションが固定 remote port をすでに所有しています。
動作しているセッションを使用するか、古いセッションを閉じるか、
[トラブルシューティングガイド](docs/troubleshooting.md)の port cleanup 手順に従ってください。

## cc-clip を使わない方がよい場合

適している場合は、より単純な選択肢を使用してください。

- ワークフロー全体が editor 内にある場合は、editor 組み込みのリモートクリップボードを使用します。
- text-only のクリップボード同期には OSC 52 を使用します。
- 画像転送の頻度が低く、貼り付け動作を維持するためにデーモンと SSH forward を使う価値がない場合は、`scp` を使用します。
- 限定的な agent workflow ではなく、広範で双方向のクリップボード同期が必要な場合は、汎用クリップボードブリッジを使用します。
- リモートのローカルユーザーがユーザースコープのループバックトンネルにアクセスしてはならない、信頼できない共有ホストでは cc-clip を使用しないでください。

## ドキュメント

| ガイド | 内容 |
|---|---|
| [Windows クイックスタート](docs/windows-quickstart.md) | Windows の upload、paste、ホットキーワークフロー |
| [アップグレード](docs/upgrading.md) | breaking change と version-specific migration |
| [コマンド](docs/commands.md) | 一般的な command、flag、environment variable |
| [通知](docs/notifications.md) | hook と plugin の統合 |
| [トラブルシューティング](docs/troubleshooting.md) | symptom ごとの診断 |
| [セキュリティ](SECURITY.md) | threat model と trust boundary |

## コントリビュート

bug report と焦点を絞った pull request を歓迎します。大きな機能については、最初に
[issue](https://github.com/ShunmeiCho/cc-clip/issues)を開き、進め方を議論してください。

source から build するには、`go.mod` に宣言された Go version が必要です。

```bash
git clone https://github.com/ShunmeiCho/cc-clip.git
cd cc-clip
make build
make test
```

commit message には [Conventional Commits](https://www.conventionalcommits.org/) を使用してください
（`feat:`、`fix:`、`docs:` など）。

## ライセンス

[MIT](LICENSE)
