# herdr-pasture スマホ表示対応（開閉するサイドバー）設計書

作成日: 2026-11-20
前提: v0.3.4（`main` a9fce95）、herdr 0.9.0

## 1. 目的

スマホの SSH クライアントから herdr を見る場面が出てきた。herdr は端末幅が
`ui.mobile_width_threshold`（既定 64）以下になるとモバイルレイアウトに切り替わるが、
タブの分割はそのまま残る。45 桁の画面ではドックが 19 桁を取り、エージェントのペインは
26 桁しか残らない。

スマホ表示のときは、pasture を「開いて、選んで、閉じる」サイドバーとして動かす。

1. サイドバーを開くと、pasture が画面いっぱいに出る。
2. 行を選ぶと、そのワークスペース／エージェントに移動し、サイドバーは閉じる。
3. その画面から、キー一つでサイドバーをまた開ける。

PC で見ているときの挙動は変えない。

## 2. 検証済みの herdr の挙動（0.9.0、隔離セッション `pasture-mobile`）

- `api snapshot` の `layouts[].area.width` は、最後に入力・接続したクライアントの幅に
  追従する。スマホ（45 桁）が接続すると全タブの area が 45 になり、PC（120 桁）が
  接続すると 120 に戻る。クライアントが切断しても最後の値が残る。
- `herdr pane zoom <pane> --on` はモバイルレイアウトでもそのペインを全幅に出す。
  ズームは各タブのサーバー状態で、PC で開き直してもズームしたまま残る。
- ズームはそのペインにフォーカスを移す。別タブのペインをズームすると、サーバーの
  フォーカス（`focused_tab_id`）はそのタブに移るが、クライアントの表示は元のタブのまま。
  表示を移すのは従来どおり `tab focus`。
- `pane layout` と `api snapshot` の layout は `zoomed` と `focused_pane_id` を持つ。

## 3. スコープ

### 含む
- スマホ表示の判定（`mobile_width`）
- 行選択でのズーム、`toggle` のスマホ用の挙動、スマホ表示に入ったときの自動オープン
- PC 表示に戻ったときのズーム解除
- スマホ表示中は `width_columns` による幅の再適用をしない

### 含まない（YAGNI）
- スマホ表示用の別レイアウト（行の折り返し、大きなタップ領域など）
- herdr の `mobile_width_threshold` の自動読み取り。値を変えている人は `mobile_width` を
  合わせて書く
- 一度エージェントをズームしたタブに herdr の switch メニューで戻ったときにサイドバーを
  開くこと。そのタブはエージェントのズームのまま見える

## 4. 用語

- **スマホ表示**: 自分のタブの `area.width` が 1 以上かつ `mobile_width` 以下。
  `mobile_width = 0` なら常に PC 表示（機能オフ）。
- **サイドバーを開く**: そのタブのドックをズームする（ズームでフォーカスも移る）。
- **サイドバーを閉じる**: そのタブのドック以外のペイン（戻り先）をズームする。
- **ズーム印**: pasture がそのタブをズームしたことを示すファイル
  `StateDir/zoomed/<tab>`。中身は戻り先ペインの ID（空でもよい）。印があるタブだけを
  PC 復帰時に解除するので、ユーザーが PC で自分でズームしたタブには触らない。

## 5. 構成

### 5.1 新パッケージ `internal/mobile`

UI と dock の両方から使う小さな部品をまとめる。herdr は呼ばない（純粋関数＋ファイル）。

```go
// Phone はタブ幅 width がスマホ表示かを返す。threshold<=0 は常に false。
func Phone(width, threshold int) bool

// Marks は StateDir/zoomed/<tab> を読み書きする。
type Marks struct{ Dir string }
func (m Marks) Set(tabID, returnPane string) error
func (m Marks) Get(tabID string) (returnPane string, ok bool)
func (m Marks) Clear(tabID string) error

// ReturnPane は layout の中から「サイドバーを閉じたときに出すペイン」を選ぶ。
// preferred がドック以外でタブ内に残っていればそれ、無ければドック以外で最も
// 面積の大きいペイン。ドックしか無ければ "" と false。
func ReturnPane(l snapshot.Layout, dockID, preferred string) (string, bool)
```

ファイル名には dock の `sanitizeID` と同じ規則を使う。共有のため `sanitizeID` を
`mobile` 側に移すか、同等の関数を置く（dock からは `mobile.SanitizeID` を呼ぶ）。

### 5.2 snapshot の拡張

- `Snapshot` に `FocusedTabID`、`Layouts []Layout` を追加。
- `Layout` に `Zoomed bool`、`FocusedPaneID string` を追加。
- `Workspace` に `ActiveTabID string` を追加。

### 5.3 herdr.Client

`Zoom(paneID string, on bool) error` を追加（`pane zoom <id> --on|--off`）。

### 5.4 config

`mobile_width int`（既定 64）。負の値は警告を出して 64 に戻す。0 で機能オフ。

## 6. 振る舞い

### 6.1 UI（`pasture ui`）

UI は自分のペイン ID とタブ ID を `HERDR_PANE_ID` / `HERDR_TAB_ID` から受け取る
（`WithSelf`）。どちらかが空なら（herdr の外で動かしたとき）スマホ関連の処理を全部
しない。

ポーリングで得た snapshot から、自分のタブについて次を読み取る。

- `phone`: 自分のタブの layout の `area.width` がスマホ表示か
- `active`: `phone` かつ `focused_tab_id` が自分のタブ
- `zoomed`、`focused_pane_id`

前回のポーリング結果と比べ、変化した瞬間だけ動く（初期値はどちらも false）。

| 変化 | 条件 | 動作 |
|---|---|---|
| `active` が false→true | タブが未ズーム | ズーム印を付け（戻り先 = 今のフォーカスがドック以外ならそのペイン）、自分をズーム |
| `phone` が true→false | 印があり、タブがズーム中 | タブのフォーカス中ペインに `zoom --off`、印を消す |
| `phone` が true→false | 印があり、タブが未ズーム | 印だけ消す |

自動オープンを「入った瞬間」に限るのは、ユーザーが手でズームを外したときに 1 秒後に
再びズームして取り合いになるのを避けるため。

行の選択（Enter／クリック）は、スマホ表示なら移動の前にズームする。

- エージェント行: 行のペインをズームし、行のタブに印（戻り先 = そのペイン）→
  `tab focus` → `agent focus`。ズームを先にするのは、移動先のドックが「未ズームの
  タブに入った」と判断して自分を開くのを防ぐため（移動先のドックが次に見るのは
  ズーム済みのタブ）。
- ワークスペース行: snapshot の `workspaces[].active_tab_id` の layout から
  `ReturnPane`（戻り先候補はその layout の `focused_pane_id`）を選んでズームし、印 →
  `workspace focus`。ペインが選べなければズームせずに移動だけする。
- グループ見出しの折りたたみは今までどおり。

ズームなど herdr 呼び出しの失敗は表示しない（次のポーリングで状態が正しく見える）。

### 6.2 `toggle`（`pasture toggle`）

タブの lock を取ったあと、そのタブの layout を読み、スマホ表示なら次を行う。PC 表示なら
従来どおり（閉じて snooze／開く）。

- ドックが生きていて、タブがズーム中でフォーカスがドック（＝サイドバーが開いている）:
  戻り先を `ReturnPane(layout, dock, 印の戻り先)` で選んでズームし、印を更新する。
  選べなければ何もしない。
- ドックが生きていて、それ以外: 印を付け（戻り先 = 今のフォーカスがドック以外なら
  そのペイン、そうでなければ既存の印の値）、ドックをズームする。
- ドックが無い: snooze を解除して `openLocked` でドックを作り、トークンが立ったら
  上と同じ手順でズームする。

スマホ表示中の `toggle` はドックを閉じない。閉じてしまうと開き直すたびにペインの
起動を待つことになる。

### 6.3 `ensure` の幅調整

`applyWidth` は layout の `area.width` がスマホ表示ならそのまま戻る。スマホ幅で分割比を
書き換えると、その比が PC 表示にも残るため。

## 7. エラー処理

- snapshot に自分のタブの layout が無い: スマホ関連の判定をしない（前回の状態も
  更新しない）。
- 印ファイルの読み書き失敗: UI では無視し、ズームはそのまま行う。PC 復帰時に印が無いと
  そのタブのズームは残るが、`prefix+z` で外せる。`toggle` ではエラーとして返す。
- `zoom` の失敗: UI では無視。`toggle` ではエラーとして返し、終了コード 1。

## 8. テスト

- `mobile`: `Phone` の境界（0、閾値ちょうど、閾値+1、width 0）、`Marks` の往復、
  `ReturnPane` の優先順位。
- `snapshot`: 追加フィールドのデコード。
- `config`: `mobile_width` の既定値、負の値の警告、0。
- `ui`: 自動オープンが入った瞬間だけ起きること、未ズームのときだけ起きること、
  PC 復帰で印のあるタブだけ解除すること、スマホ表示での行選択の呼び出し順
  （zoom → tab → agent）、ワークスペース行のズーム先、PC 表示では zoom を呼ばないこと。
- `dock`: スマホ表示の `toggle` の開く／閉じる／ドック無し、PC 表示の `toggle` が
  変わらないこと、スマホ表示で `applyWidth` が resize を呼ばないこと。
- 実機確認: 隔離セッション `pasture-mobile` を幅 45 と 120 で交互に開き、自動オープン、
  行選択、`toggle`、PC 復帰を画面で確認する。
