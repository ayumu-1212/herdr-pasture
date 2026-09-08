# herdr-pasture 設計書

作成日: 2026-09-08

## 1. 目的

herdr の標準サイドバーは、上段に「開いているワークスペース」、下段に「エージェント一覧」を表示するが、
エージェントをプロジェクト（ディレクトリ）ごとにまとめて見る手段がない。
また、プラグインから標準サイドバーを拡張する API は存在しない。

herdr-pasture は、稼働中のエージェントペインを **git リポジトリ単位でグルーピングして常時表示し、
クリックでそのペインへフォーカスする** ドック型ペインを提供するプラグインである。

### 既存プラグインとの違い

| プラグイン | 型 | 差分 |
| --- | --- | --- |
| fullerzz/herdr-plugin-sesh | 呼び出して選ぶピッカー（overlay） | 常駐しない。ディレクトリを開く用途が中心 |
| cloudmanic/herdr-plus | プロジェクトテンプレートから起動 | 常駐しない。テンプレート定義が必要 |
| alexarthurs/herdr-sidebar | 常駐ドック（ファイルツリー + git） | エージェント一覧ではない。ドック方式は本プラグインの参考実装 |

herdr-pasture は「常駐」「エージェントペインが行の単位」「リポジトリでグルーピング」の 3 点を核とする。

## 2. スコープ

### 含む

- 全タブの左端に自動配置される常駐ペイン
- 稼働中のエージェントペインの一覧（git リポジトリ単位のグループ表示）
- 行クリック / キーボード操作によるペインへのフォーカス移動
- グループの折りたたみ
- エージェント状態（working / idle / blocked / done / unknown）の表示
- toggle / redeploy アクション

### 含まない（YAGNI）

- 未オープンのディレクトリからワークスペースを新規作成する機能（sesh に任せる）
- エージェントがいないシェルペインの表示
- 標準サイドバーの置き換え・非表示化
- Windows 対応（`platforms = ["macos", "linux"]`）

## 3. 前提

- herdr 0.8.0 以上（`min_herdr_version = "0.8.0"`）
- Go 1.22 以上 + bubbletea（開発機には `brew install go` で導入する）
- プラグインが利用する herdr CLI:
  `api snapshot`, `pane list`, `pane split --ratio --no-focus`, `pane swap`, `pane run`,
  `pane rename`, `pane focus`, `pane close`, `pane report-metadata`, `plugin log`

## 4. データ取得とグルーピング

### 4.1 取得

- UI プロセスは `herdr api snapshot` を `poll_interval_ms`（既定 1000）ごとに実行する。
- `result.snapshot.panes` から `agent` が非 null のペインを抽出する。
  使用フィールド: `pane_id`, `workspace_id`, `tab_id`, `cwd`, `agent`, `agent_status`,
  `terminal_title_stripped`, `focused`。
- `focused_pane_id` から現在のフォーカス行を判定する。
- 自分自身（pasture の UI ペイン）は除外する。判定は `pane report-metadata` で刻んだトークン。

### 4.2 リポジトリ正規化

- ペインの `cwd` ごとに以下を実行し、結果をプロセス内でキャッシュする（同じ cwd は再実行しない）。
  1. `git -C <cwd> rev-parse --show-toplevel` → ワークツリーのルート
  2. `git -C <cwd> rev-parse --git-common-dir` → 共通 `.git` の場所。
     これがワークツリー直下の `.git` と異なる場合は worktree と判定し、
     共通 `.git` の親ディレクトリを **本体リポジトリのパス** としてグループキーにする。
  3. worktree のときは `git -C <cwd> rev-parse --abbrev-ref HEAD` でブランチ名を取得し、行に添える。
- git 管理外、または git が失敗した cwd は、cwd そのものをグループキーにする。
- ディレクトリが削除されているなど cwd が解決できない場合も cwd をそのままキーにする。

### 4.3 グルーピングと並び順

- グループ見出し: グループキーのディレクトリ名（`basename`）。
  同名の見出しが複数できる場合は、親ディレクトリ名を付けて区別する（例: `polaris-x/hp`）。
- グループの並び: 見出し文字列の昇順。
- グループ内の並び: ワークスペース番号昇順 → ペイン ID 昇順。
  ポーリングのたびに行が移動しないよう、状態やタイトルでは並べ替えない。
- `exclude` グロブに一致する cwd のペインは一覧から外す。

### 4.4 グルーピングの純粋関数化

`internal/group` は「スナップショット JSON + git 解決結果」を入力に、
「グループ一覧（見出し、行、折りたたみ状態を除く）」を返す純粋関数とする。
git の実行は `Resolver` インターフェースで注入し、テストではフェイクを使う。

## 5. 画面

```
 herdr-pasture
 ▾ global-sourcing-tool
   ● watchdog techcrunch ingestion…
   ○ Notion-research plist設定
 ▾ herdr-pasture
   ◐ Herdr プロジェクトピッカー…
 ▾ polaris-x/hp
   ○ wordpress headless cms mig…
 ▾ polala
   ✓ email feature #206
```

- 1 行 = 1 エージェントペイン。「状態アイコン + ターミナルタイトル（`terminal_title_stripped`）」。
  タイトルが空なら「エージェント名 + ペイン ID」（例: `claude w5:p3`）。
- worktree のペインは「`[ブランチ名]` + タイトル」。
- 状態アイコンと色:

  | agent_status | アイコン | 色 |
  | --- | --- | --- |
  | working | ◐ | 黄 |
  | idle | ○ | 既定色 |
  | done | ✓ | 緑 |
  | blocked | ● | 赤 |
  | unknown | ? | 灰 |

- フォーカス中のペインの行は反転表示。
- 見出しクリックで折りたたみ / 展開。折りたたみ状態はプロセス内で保持する（永続化しない）。
- 行クリックで `herdr pane focus --pane <pane_id>` を実行する。
  別ワークスペースのペインでも herdr がワークスペース・タブごと切り替える。
- キーボード: ↑↓ で選択移動、Enter でフォーカス、Space で折りたたみ、`r` で即時再取得、`q` で終了。
- 幅はタブ幅の `width_ratio`（既定 0.25）、最小 22 桁。タイトルは幅に合わせて末尾を `…` で省略する。
- 上端ヘッダに `herdr-pasture` を表示し、snapshot 取得に失敗している間は `disconnected` を併記する。
- 配色は herdr の既定テーマになじむ ANSI 16 色のみを使う。

## 6. ドックのライフサイクル

### 6.1 自動配置（ensure）

マニフェストの `[[events]]` で以下を購読し、いずれも `bin/pasture ensure` を起動する。

- `pane.focused`
- `tab.focused`
- `tab.created`
- `workspace.created`

ensure の手順:

1. `auto_open = false` なら何もせず終了。
2. `HERDR_TAB_ID`（無ければフォーカス中のタブ）を対象タブとする。
3. 状態ディレクトリ `HERDR_PLUGIN_STATE_DIR/ensure.lock` を `mkdir` で取得する。
   取得できず、かつロックが 30 秒以上古ければ stale として奪う。それ以外は終了。
4. `herdr pane list` で対象タブのペインを列挙する。
   - トークン付き pasture ペインが生きていれば何もしない。
   - 名前は pasture だがトークンが無いペイン（サーバ再起動で復元された死骸）は `pane close` する。
5. タブ ID のスヌーズファイルが存在すれば終了（手動で閉じたタブには再配置しない）。
6. `herdr pane layout` で対象タブの左端・全高のペインを特定する。
7. `herdr pane split <左端ペイン> --direction right --ratio <width_ratio> --no-focus --cwd <元ペインのcwd>`
   で新規ペインを作り、`herdr pane swap --source-pane <新規> --target-pane <左端>` で左へ移す。
8. `herdr pane run <新規> "<bin>/pasture ui"` で UI を起動し、`herdr pane rename <新規> pasture` で命名する。
9. UI プロセスが `pane report-metadata` でトークンを刻むまで最大 6 秒待つ（0.2 秒 × 30 回）。
10. ロックを解放して exit 0。

### 6.2 toggle アクション

- フォーカス中のタブに pasture ペインがあれば `pane close` し、タブ ID のスヌーズファイルを作る。
- 無ければスヌーズファイルを消して ensure を実行する。
- `config.toml` の `[[keys.command]]` から
  `herdr plugin action invoke herdr-pasture.toggle` を呼ぶことでキー割り当てできる。

### 6.3 redeploy アクション

全ワークスペースの pasture ペインを閉じ、スヌーズをすべて消す。
次の focus イベントで最新ビルドの UI が再配置される。

### 6.4 UI プロセスの終了

- `q` または SIGTERM で終了する。終了時にペインは herdr が回収する。
- UI プロセスの終了はスヌーズを作らない（ユーザーが明示的に閉じたのは toggle 経由のみ）。

## 7. 設定

`herdr plugin config-dir herdr-pasture` 配下の `config.toml`。すべて任意。

```toml
auto_open = true          # false でイベント時の自動配置を停止
width_ratio = 0.25        # タブ幅に対する比率（0.1〜0.5）
poll_interval_ms = 1000   # snapshot 取得間隔
exclude = ["~/tmp/**"]    # 一覧から外す cwd のグロブ（~ と $VAR を展開）
```

範囲外・不正な値は既定値に戻し、stderr に警告を出す。

## 8. エラー処理

| 事象 | 挙動 |
| --- | --- |
| `api snapshot` 失敗 | 前回の表示を保持し、ヘッダに `disconnected` を出す。終了しない |
| `git rev-parse` 失敗 | cwd をそのままグループキーにして続行 |
| フォーカス先ペインが消えていた | 何もしない。次のポーリングで行が消える |
| ensure 中の CLI 失敗 | 静かに exit 0 し、理由を stderr に出す（`herdr plugin log` で確認可能） |
| ロック競合 | 後発は終了。stale ロックは 30 秒で奪う |
| 設定ファイル不正 | 既定値で続行し、警告を stderr に出す |

## 9. 構成

```
herdr-plugin.toml
scripts/build.sh              go build -o bin/pasture ./cmd/pasture
cmd/pasture/main.go           サブコマンド: ui / ensure / toggle / redeploy
internal/config/              config.toml の読み込みと検証
internal/herdr/               herdr CLI ラッパ（インターフェース + 実装）
internal/snapshot/            api snapshot の型とデコード
internal/group/               リポジトリ正規化、グルーピング、並び順（純粋関数）
internal/ui/                  bubbletea モデル、描画、マウス処理
internal/dock/                ensure / toggle / redeploy のロジック（ロック・スヌーズ含む）
docs/superpowers/specs/       設計書
```

### マニフェスト（案）

`[[panes]]` の宣言は `herdr plugin pane open` から手動で開くための入口であり、
通常のドック配置は ensure が `pane split` + `pane run` で行う（左端配置と swap が必要なため）。

```toml
id = "herdr-pasture"
name = "Pasture"
version = "0.1.0"
min_herdr_version = "0.8.0"
description = "Agents grouped by repository, docked on the left of every tab"
platforms = ["macos", "linux"]

[[build]]
command = ["sh", "scripts/build.sh"]

[[panes]]
id = "ui"
title = "Pasture"
placement = "split"
command = ["./bin/pasture", "ui"]

[[actions]]
id = "toggle"
title = "Pasture: toggle"
contexts = ["global"]
command = ["./bin/pasture", "toggle"]

[[actions]]
id = "redeploy"
title = "Pasture: redeploy panes"
contexts = ["global"]
command = ["./bin/pasture", "redeploy"]

[[events]]
on = "pane.focused"
command = ["./bin/pasture", "ensure"]

[[events]]
on = "tab.focused"
command = ["./bin/pasture", "ensure"]

[[events]]
on = "tab.created"
command = ["./bin/pasture", "ensure"]

[[events]]
on = "workspace.created"
command = ["./bin/pasture", "ensure"]
```

## 10. テスト

- `internal/group`: テーブル駆動テスト。worktree の本体への集約、同名見出しの区別、
  並び順の安定性、`exclude` の適用、git 失敗時のフォールバック。
- `internal/dock`: herdr CLI をフェイクにして「既存あり → 何もしない」「死骸あり → 閉じて開き直す」
  「ロック競合 → 終了」「stale ロック → 奪う」「スヌーズあり → 開かない」を検証。
- `internal/ui`: bubbletea の `Model.Update` を直接呼び、クリック座標から行への対応、
  折りたたみ時の座標ずれ、幅に応じた省略を検証。
- `internal/config`: 既定値、範囲外の値、`~` 展開。
- 実機確認: `herdr plugin link <repo>` の後、named session（`herdr --session pasture-dev`）で
  タブ作成・ワークスペース切替・クリック移動・toggle を手動確認する。本番セッションでは行わない。

## 11. 開発の進め方

- 実装は git worktree 上で行う（ユーザーの運用ルール）。
- 順序: config → snapshot → group（テスト先行） → herdr ラッパ → ui → dock → マニフェスト・ビルド → 実機確認。
