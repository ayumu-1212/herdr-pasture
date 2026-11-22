# herdr-pasture スマホ表示: 閉じたサイドバーを細い帯にする／worktree 作成行 設計書

作成日: 2026-11-22
前提: v0.4.0（`main` c066756）、herdr 0.9.0。`2026-11-20-mobile-sidebar-design.md` の続き。

## 1. 目的

v0.4.0 のスマホ表示では、サイドバーを閉じると別のペインがズームされて全画面になり、
開き直す手段は `prefix+g` しかない。スマホではキー入力が面倒で、画面右上の herdr の
「switch」をタップすると herdr 標準のメニューが出る。

1. 閉じたサイドバーを、タップすれば開ける細い帯として画面左端に残す。
2. スマホから新しい git worktree をタップ一つで作れるようにする。

## 2. 検証済みの herdr の挙動（0.9.0、隔離セッション `pasture-mobile`）

- 「switch」は herdr のクライアント内のメニュー（spaces / tabs / menu）。設定項目にも
  プラグインのマニフェスト（build / startup / actions / events / panes / link_handlers）
  にも差し替える手段は無く、開いたことを知らせるイベントも無い。
- `pane resize` で縮めると、分割の比は 0.1 で止まる。45 桁ならドックは 5 桁
  （枠を除いて 3 桁）、120 桁なら 12 桁。これが最も細い帯。
- モバイルレイアウトでも、ズームしていないタブは分割をそのまま描くので、5 桁の帯と
  40 桁のペインが並ぶ。帯の中をタップすると、マウスイベントはドックの UI に届く。
- ズーム中のタブでも `pane resize` は分割の比を変える（ズームが外れたときに効く）。
- `agent focus` はエージェントのいないペインを受け付けない（`agent_not_found`）。
  任意のペインにフォーカスを移すには `pane zoom <id> --on` のあと `--off` する。
- API 経由のズームは `pane.focused` を発火させ、`ensure` の `applyWidth` が走る。
  PC 幅ならドックは `width_columns` に戻る（スマホ幅では何もしない）。
- `herdr worktree create --cwd <repo> --no-focus` は、ブランチ名を渡さなくても
  `worktree/brave-forest-b758` のような名前を自動で付け、worktree とそのワーク
  スペースを作る。結果の JSON に `workspace.workspace_id`、`tab.tab_id`、
  `root_pane.pane_id` がある。

## 3. スコープ

### 含む
- 閉じた状態を「ズームなし＋ドックを最小幅」に変える（UI の行選択、自動オープン、
  `toggle`、PC 復帰）
- 帯の描画とタップで開く
- スマホ表示のときだけ、git リポジトリのグループに `+ worktree` 行を出す

### 含まない
- herdr の switch の差し替え（不可能）
- 帯にエージェントの状態を出すこと（`›` だけ）
- worktree のブランチ名の入力、作成後のエージェント起動（シェルを開くだけ）
- 状態ファイル（印、lock、snooze）をセッションごとに分けること。タブ ID はセッション
  ごとに振られるので、二つのセッションで同じ ID のタブがあると干渉しうる。以前から
  ある問題で、今回は扱わない。

## 4. 状態

タブの状態は herdr のサーバー状態と印ファイルで決まる。

| 状態 | ズーム | ドックの幅 | 印 |
|---|---|---|---|
| 開いている | ドックがズーム中でフォーカスもドック | 何でもよい | あり |
| 閉じている（帯） | なし | 最小（帯） | あり |
| PC 表示 | 触らない | `width_columns` など | なし |

**印**（`StateDir/zoomed/<tab>`）は JSON にする。

```json
{"back": "w1:p2", "width": 0.42}
```

- `back`: 閉じたときに前に出すペイン。
- `width`: 最初に印を付けたときのドックの幅（タブの幅に対する割合）。分割の比は全
  クライアント共通なので、これは PC で見ていたときの比と同じ。PC 復帰でこれに戻す。
  0 は不明。
- 既に印があるときは `width` を上書きしない（帯の幅で上書きすると戻せなくなる）。
- v0.4.0 の印（中身はペイン ID だけ）は `back` として読み、`width` は 0。

## 5. 構成

### 5.1 `internal/mobile`

herdr を呼ぶ処理をここにまとめ、UI と `toggle` の両方から使う。

```go
type Herdr interface {
	Zoom(paneID string, on bool) error
	Resize(paneID string, amount float64) error
}

type Mark struct { Back string; Width float64 }
func (m Marks) Get(tabID string) (Mark, bool)
func (m Marks) Set(tabID string, mk Mark) error
func (m Marks) Remember(tabID, back string, l snapshot.Layout, dockID string) error
func (m Marks) Clear(tabID string) error

func Open(h Herdr, m Marks, l snapshot.Layout, tabID, dockID string) error
func Close(h Herdr, m Marks, l snapshot.Layout, tabID, dockID, target string) error
func Restore(h Herdr, m Marks, l snapshot.Layout, tabID, dockID string, fallbackColumns int) error
func Wide(l snapshot.Layout, dockID string) bool
```

- `Remember`: 印の `back` を更新する（空なら前の値を残す）。印が無ければ、`l` での
  `dockID` の幅の割合を `width` として記録する。
- `Open`: `Remember`（`back` = 今のフォーカスがドック以外ならそれ）→ ドックをズーム。
- `Close`: `Remember(back = target)` → ドックが `Wide` なら `Resize(dock, -0.9)`（herdr が
  最小の比で止める）→ `target` があれば `Zoom(target, on)` → `Zoom(target, off)`（ズームを
  外しつつフォーカスを target に移す）。`target` が無くタブがズーム中なら、フォーカス中の
  ペインのズームを外す。
- `Restore`: ズーム中なら外す → ドックを `width × area` 桁（`width` が 0 なら
  `fallbackColumns`、それも 0 なら幅は触らない）に戻す → 印を消す。
- `Wide`: ドックの幅がタブの幅の 1/5 を超えている。帯（1/10）との間に余裕を取り、
  丸めで直し続けることがないようにする。

### 5.2 `herdr.Client`

`CreateWorktree(cwd string) (Worktree, error)` を追加する（`worktree create --cwd <cwd>
--no-focus`）。`Worktree` は workspace / tab / root pane の ID。worktree の作成は git を
動かすので、5 秒のタイムアウトとは別に 60 秒のクライアントを main で作って UI に渡す。

### 5.3 `group`

`Group` に `Repo bool`（キーが git リポジトリのルート）を追加する。worktree 行を出すか
どうかに使う。

## 6. 振る舞い

### 6.1 UI

前回からの変化で動く部分（`observe`）:

| 変化 | 条件 | 動作 |
|---|---|---|
| `active` が false→true | 印なし、未ズーム | `Open` |
| `phone` が true→false | 印あり | `Restore` |

状態で動く部分（毎回のポーリング）:

- スマホ表示、印あり、未ズーム、自分が `Wide`: `Resize(self, -0.9)`。ensure が後から
  作ったドックや、herdr が分割比を変えたときに帯へ戻す。

印のあるタブに入っても開かないので、herdr の switch で移動した先は帯のまま見える。

行の選択（スマホ表示）:

- エージェント行: 行のタブの layout とドックを最後の snapshot から取り、`Close(target =
  行のペイン)` → `tab focus` → `agent focus`。`Close` を移動より先にするのは、移動先の
  ドックが印を見て開かないようにするため。
- ワークスペース行: アクティブタブで `ReturnPane` を選んで `Close` → `workspace focus`。
- `+ worktree` 行: `CreateWorktree(グループのキー)` → 新しいタブに印（`back` = ルート
  ペイン）→ `workspace focus`。新しいタブのドックは ensure が作り、そのドックの UI が
  印を見て帯にする。失敗したらタイトルバーに `worktree failed` をポーリング 5 回分出す。

帯:

- スマホ表示で UI の幅が 6 桁以下なら帯として描く。1 行目に `›` だけを出す。
- 帯のどこをタップしても、Enter でも `Open`。

`+ worktree` 行はスマホ表示のときだけ、`Repo` のグループの末尾（折りたたみ中は出さない）
に出る。

### 6.2 `toggle`

スマホ表示のとき:

- ドックがズーム中でフォーカスもドック: `Close(target = ReturnPane(layout, dock,
  印の back))`。選べなければ何もしない。
- それ以外: `Open`。
- ドックが無い: 今までどおり作ってから `Open`。

## 7. エラー処理

- UI の herdr 呼び出し（ズーム、resize）の失敗は表示しない。次のポーリングで正しい状態
  が見える。帯への resize は失敗しても次のポーリングで再び試す。
- `CreateWorktree` の失敗だけはタイトルバーに出す。ボタンを押して何も起きないと、
  押せていないのか失敗したのか分からないため。
- `toggle` ではエラーを返す（終了コード 1）。

## 8. テスト

- `mobile`: 印の JSON の往復と旧形式、`Remember` が `width` を上書きしないこと、
  `Open` / `Close` / `Restore` の herdr 呼び出し順、`Wide` の境界。
- `group`: `Repo`。
- `herdr`: `CreateWorktree` の argv と結果のデコード。
- `ui`: 自動オープンが印のないタブだけで起きること、帯の維持、帯のタップで開くこと、
  帯の描画、スマホ表示の行選択の呼び出し順、`+ worktree` 行の表示条件と動作、失敗表示。
- `dock`: スマホ表示の `toggle` の開く／閉じる（帯になる）。
- 実機確認: 隔離セッションを 45 桁と 120 桁で開き、帯、帯のタップ、行選択、worktree 行、
  PC 復帰での幅の復元を画面で見る。
