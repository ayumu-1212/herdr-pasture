# herdr-pasture v0.2 設計書

作成日: 2026-09-08
前提: `2026-09-08-herdr-pasture-design.md`（v0.1）

## 1. 目的

v0.1 は標準サイドバーと併存する前提だった。実際に使うと、標準サイドバーを消して
pasture を左端の唯一の列にしたくなる。そのためには pasture 単体で標準サイドバーの
役割を満たす必要がある。

本版で 2 点を足す。

1. エージェントがいないワークスペースも一覧に出し、クリックで移動できるようにする。
   これが無いと、シェルだけ開いているワークスペースが一覧から消え、切り替え手段が
   キー操作だけになる。
2. ドックの幅をタブ幅への比率ではなく桁数で固定できるようにする。

この 2 点が入った時点で、`~/.config/herdr/config.toml` に
`sidebar_start_collapsed = true` と `sidebar_collapsed_mode = "hidden"` を書けば
標準サイドバーを消せる。設定値が herdr 0.8.0 に受理されることは確認済み。

## 2. スコープ

### 含む
- エージェントを持たないワークスペースの行表示とクリックによるフォーカス移動
- `width_columns` による桁数固定と、フォーカスイベントごとの幅の再適用

### 含まない（YAGNI）
- 標準サイドバーを pasture が自動で消す処理。ユーザーが herdr の設定を書く
- ワークスペースの作成・削除・リネーム
- タブ単位の表示

## 3. エージェントのいないワークスペース

### 3.1 行の種類

`group.Row` に種別を足す。

```go
type RowKind int

const (
    RowAgent RowKind = iota // エージェントペイン
    RowWorkspace            // エージェントを持たないワークスペース
)
```

- `RowAgent`: v0.1 と同じ。状態アイコン + `terminal_title_stripped`。
- `RowWorkspace`: 1 ワークスペースにつき 1 行。アイコンは `◦`、ラベルはワークスペース名
  (`snapshot.Workspace.Label`)。ラベルが空ならワークスペース ID。

エージェントペインを 1 つでも持つワークスペースには `RowWorkspace` を出さない。
エージェント行がそのワークスペースの存在を示しているので、二重に出す意味がない。

### 3.2 入力の変更

`Build` は今まで `Agent != ""` のペインだけを見ていた。全ペインを見るようにする。

- エージェントペイン: 従来どおり `RowAgent` を作る。
- 各ワークスペースについて、`RowAgent` が 1 つも作られなければ `RowWorkspace` を 1 つ作る。

除外設定 (`Options.Exclude`) は両方の行に効く。ワークスペースの cwd が除外に一致すれば
そのワークスペースは出さない。pasture 自身のペイン (`SelfToken` 付き) は
エージェント判定からも、ワークスペースの cwd 決定からも外す。

### 3.3 ワークスペースのディレクトリ

`herdr api snapshot` のワークスペース要素に cwd は無い（`workspace_id`, `label`,
`number`, `focused`, `agent_status`, `pane_count`, `tab_count`, `active_tab_id` のみ）。
確認済み。したがってペイン側の cwd から決める。

ワークスペースに属するペインのうち、ペイン番号が最小のものの `cwd` を採用する。
同一ワークスペース内で cwd が分かれていても結果が安定する。pasture 自身のペインは
候補から外す。候補が 1 つも無ければそのワークスペースは出さない。

決まった cwd は `RowAgent` と同じ `Resolver` に通し、同じ規則でグループキーにする。

### 3.4 並び順

v0.1 の全順序をそのまま使う。`RowWorkspace` は `WorkspaceNumber` を持ち、
`PaneID` は空になるため、同一ワークスペース番号で衝突しないよう
比較キーを `(WorkspaceNumber, Kind, paneNumber(PaneID), PaneID)` とする。
`RowWorkspace` は `Kind` が大きい側に来るので、万一同じワークスペースに両方が
生じても順序は決まる。

### 3.5 クリックの動作

`ui` は行の `Kind` で分岐する。

- `RowAgent`: 既存どおり `herdr agent focus <pane_id>`。
- `RowWorkspace`: `herdr workspace focus <workspace_id>`。

`herdr.Client` に `FocusWorkspace(workspaceID string) error` を足す。
`ui.Fetcher` インターフェースにも同名のメソッドを足す。

## 4. 固定幅

### 4.1 設定

```toml
width_columns = 30   # 0（既定）なら width_ratio を使う
```

`width_columns >= 1` のとき、`width_ratio` は無視される。0 未満は 0 に丸め、警告を出す。

### 4.2 配置時

`pane layout` で得た `area.width` を W とし、目標桁数 C から `ratio = C / W` を計算して
`pane split --ratio` に渡す。v0.1 で実機確認したとおり `--ratio` は分割元が保持する
割合で、`swap` は位置だけ交換してスロットの幅は保持するため、結果としてドックが C 桁になる。

C が極端な場合に備えて、実際に使う桁数を次で丸める。

```
target = clamp(C, 22, max(22, W/2))
```

下限 22 は v0.1 の表示の最小幅。上限はタブの半分。W が 44 未満なら下限が優先され、
ドックがタブの半分を超えることを許す。極端に狭いタブは元から使い物にならないので、
これ以上の分岐は作らない。

### 4.3 フォーカスイベントごとの再適用

`Ensure` がドックの存在を確認したあと、`width_columns` が設定されていれば幅を測る。

- 現在の幅 A と目標 target の差が 1 桁以内なら何もしない。丸め誤差で毎回動くのを防ぐ。
- 差が 2 桁以上なら `pane resize --pane <dock> --direction right --amount (target - A) / W`
  を 1 回だけ呼ぶ。

`--amount` がタブ幅に対する比率の増減であることは実機確認済み。
（54 桁のタブで `--amount 0.1` を与えると 14 桁が 19 桁になった。）

resize の失敗はログに残して続行する。幅が合わないことは表示の破綻ではない。

手動で幅を変えても次のフォーカスイベントで目標に戻る。これは意図した挙動であり、
README の Known limitations に明記する。

### 4.4 width_ratio との関係

`width_columns` が 0 のときの経路は v0.1 と完全に同じにする。既存利用者の挙動を変えない。

## 5. エラー処理

| 事象 | 挙動 |
| --- | --- |
| ワークスペースにペインが 1 つも無い | そのワークスペースは出さない |
| ワークスペースの cwd が git 管理外 | v0.1 と同じく cwd をグループキーにする |
| `workspace focus` が失敗（消えたワークスペース） | 何もしない。次のポーリングで行が消える |
| `pane resize` が失敗 | ログに残して続行 |
| `width_columns` が負 | 0 に丸めて警告 |

## 6. テスト

`internal/group`:
- エージェント無しのワークスペースが 1 行だけ出る
- エージェントが 1 つでもあるワークスペースには行が出ない
- ワークスペースの cwd がペイン番号最小のペインから取られる
- pasture 自身のペインが cwd の候補にならない
- 除外設定がワークスペース行にも効く
- ワークスペース行とエージェント行が混在しても並び順が全順序である

`internal/dock`:
- `width_columns` 指定時の split ratio が `target / W` である
- 幅が目標と 1 桁差なら resize を呼ばない
- 2 桁以上の差で resize を 1 回だけ呼び、amount が `(target - A) / W` である
- `width_columns = 0` のとき従来どおり `width_ratio` を渡し resize を呼ばない
- 狭いタブで下限 22 に丸められる

`internal/ui`:
- ワークスペース行のクリックとエンターが `FocusWorkspace` を呼ぶ
- エージェント行は従来どおり `FocusAgent` を呼ぶ

`internal/herdr`:
- `FocusWorkspace` の argv が `workspace focus <id>` である

## 7. 実装順序

1. `herdr.FocusWorkspace`
2. `group` の行種別とワークスペース行（テスト先行）
3. `ui` の分岐と `Fetcher` 拡張
4. `config.WidthColumns`
5. `dock` の幅計算と再適用
6. README 更新

## 8. 完了後

ユーザーが `~/.config/herdr/config.toml` に以下を書けば標準サイドバーが消え、
pasture が左端の唯一の列になる。設定は herdr の再起動で反映される。

```toml
[ui]
sidebar_start_collapsed = true
sidebar_collapsed_mode = "hidden"
```
