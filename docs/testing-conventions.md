# テストの規約

テストの**名前・日本語の書き方・コメント**を定めます。テストの**道具立て**（testify / gomock /
インメモリアダプタ / `httptest` / build tag `integration` / カバレッジ床）は
[../CONVENTIONS.md](../CONVENTIONS.md) の「テスト」節にあり、こちらとは役割を分けています。

識別子・ファイル単位・言語ポリシー・機械強制の規約は [../CONVENTIONS.md](../CONVENTIONS.md) を参照してください。

各ルールの末尾の【 】は**出典**です。〔Go〕は Go の公式・準公式文書または標準ライブラリの実測に
裏づけがあるもの、〔家〕はこのプロジェクトのハウスルールです。機械強制されるものは
「→ `<ツール名>`」で強制手段を書いてあります。強制手段のないルールは**人間のレビュー観点**です。

---

## C. テスト関数名

### C-1 形は 2 つだけ

**`Test<主題>`** と **`Test<主題>_<修飾>`** の 2 つです。`<修飾>` は「その主題のどの側面・シナリオを見るか」を表します。

```go
func TestPlaceOrder(t *testing.T)                          // 主題を独占する 1 つだけ
func TestPlaceOrder_EnqueuesOrderPlacedSameTx(t *testing.T) // 側面を名指しする
func TestPlaceOrder_ReservationRejectedIsConflict(t *testing.T)
```

### C-1b 主題の一意性（機械検査できる形の規則）

**同一ディレクトリ内で同じ `<主題>` を持つ Test 関数が 2 つ以上あるなら、そのすべてが `_<修飾>` を持つ**こと。
裏返すと、`Test<主題>` という修飾なしの名前は**その主題を扱う唯一の関数**にだけ許されます。
→ `scripts/convention-gate.sh`（検査 1）

集計の単位は**ディレクトリ**（= `go test` が 1 つのテストバイナリとしてリンクする単位）です。
`<pkg>` と `<pkg>_test` が同じディレクトリに同居していても**同じ単位として数えます**。
宣言された `package` 名で分けると、内部テストと外部テストに同名の主題が分かれて取りこぼしになるためです。

> **既知の限界**: この検査は**関数名だけを見て、関数本体を見ません**。
> 「サブテストを 2 件以上持つなら修飾なし」という規則にはしていません。テーブル駆動テストは
> `for` ループの中で `t.Run` を**1 箇所書いて多数のケースを回す**ため、`t.Run` の出現回数で
> サブテストの多寡を判定すると誤ります。C-1b は「主題の一意性」で形を決めるので、この問題が起きません。

### C-2 主題は Go 識別子そのまま、修飾は英語の断定形

主題は検証対象の Go 識別子をそのまま書きます（`NewOrder` / `PlaceOrder` / `Locate`）。
修飾は英語の UpperCamel の断定形（`_EnqueuesOrderCancelledSameTx`）にします。

**下線は [../CONVENTIONS.md](../CONVENTIONS.md) の A-9b（MixedCaps を使い下線で語をつなげない）の
例外ではなく、Go が認めた別の規則です。**

【Go: Google Go Style Guide が「Test, Benchmark and Example function names within `*_test.go` files
may include underscores.」と明記。標準ライブラリにも 151 件（uniq 144）の実例がある —
`grep -rhoE '^func Test[A-Za-z0-9]+_[A-Za-z0-9_]*\(' "$(go env GOROOT)/src" --include='*_test.go'`。
修飾部の小文字始まりも実在する（`TestBuffers_consume`）。かつ `revive var-naming` /
`staticcheck ST1003` はこの形を報告しない】

### C-3 ヘルパーの命名と `t.Helper()`

ヘルパーは役割で接頭を分けます。

| 接頭 | 役割 |
| --- | --- |
| `must<X>` | 失敗したら即中断して組み立てる（`mustLine`） |
| `new<X>` | 組み立てる（失敗しない、または呼び出し側が判断する） |
| `assert<X>` | 検証する |

いずれも先頭で **`t.Helper()` を呼びます**。→ `thelper`

【Go: `testing.T.Helper`、Go Wiki TestComments の `readFile` ヘルパーの例】

> **ただし Go Wiki TestComments は「アサーション・ライブラリの実装に `t.Helper` を使うな」と留保しています**
> （失敗と原因の対応が見えなくなるため）。本規約が `t.Helper()` を要求するのは
> **ドメイン固有のヘルパー**（`mustLine` のように特定のテスト対象を組み立て・検証するもの）に対してであり、
> 汎用のアサーション抽象を自作する用途には使いません。

### C-4 外部テストパッケージを既定にする

`<pkg>_test` を既定にします。内部実装の検証が必要なときは **`*_internal_test.go`** に隔離します
（`main` パッケージは例外）。→ `testpackage`

内部シンボルを外部テストパッケージへ橋渡ししたいときは **`export_test.go`**（`package <pkg>`）に
薄い別名だけを置きます。ヘルパーを内部・外部の両方から使いたい場合、これがヘルパーを重複させない唯一の方法です。

```go
// shared/serve/export_test.go
package serve

// RunWithGrace は run を外部テストパッケージへ公開する。
var RunWithGrace = run
```

【家。ただし `testpackage` linter の既定 skip-regexp が `(export|internal)_test\.go` であり、
ツール既定と一致する命名を選んでいる】

### C-4b `Example<主題>` 関数を書く

`go doc` に載り、コンパイルされ、`// Output:` があれば実行・検証される実行可能例です。
**新しいパッケージと公開ファサードには Example を用意します。**

これはテスト関数の 3 つ目の形であり、**C-1 の 2 形の制約は `Test` 接頭のものにのみ適用されます**。

【Go: Go Code Review Comments "Examples"（「新しいパッケージには完全な呼び出し手順を示す実行可能な
Example 関数か単純なテストを含めよ」）、Effective Go（標準ライブラリ自身が使用例として書かれていること）】

### C-4c 失敗メッセージは Go の書式に従う

**`YourFunc(%v) = %v, want %v`** — **実測値を先、期待値を後**に置きます。
`t.Errorf` を直接使う場合はこの書式にします。

**testify を使う場合は引数順が `(t, want, got)` で Go のメッセージ順とは逆**です。取り違えると
diff の `-want +got` が反転して診断が嘘になります。→ `testifylint` の `expected-actual`

【Go: Go Code Review Comments "Useful Test Failures"、Go Wiki TestComments】

### C-4d エラーの検証は `require`、エラー以外の独立した検証は `assert`

道具立ての規約（[../CONVENTIONS.md](../CONVENTIONS.md)）は「`require` は前提が崩れたら即中断する
致命的検証、`assert` は独立した検証を続行」と定めています。その線引きを**エラーについてだけ**具体化します。

**後ろに別のアサーションが続くなら、エラーを検証するアサーション（`Error` / `NoError` / `ErrorIs` /
`ErrorAs`）は `require` を使います。** エラーの取り違えは後続の診断をすべて無意味にする
（`ErrorAs` なら取り出し先が nil のまま次の行へ進む）ため、**常に前提**として扱います。
ブロックの最後の 1 行なら続くものが無いので `assert` のままでかまいません。
→ `testifylint` の `require-error`

`assert.Nil(t, err)` ではなく **`assert.NoError(t, err)`** を使います（診断が
「nil ではない」ではなく実際のエラー文言になります）。→ `testifylint` の `error-nil`

### C-5 `t.Parallel()` は純粋テストで必須

`internal/domain/**` と `internal/application/**` の**外部依存を持たない純粋テスト**では、
テスト関数とサブテストの両方の先頭で `t.Parallel()` を呼びます。→ `paralleltest`（パス限定）+ `tparallel`（全体）

アダプタ・統合テスト・fuzz ターゲットは対象外です（fuzzing 中は `T.Parallel` が無効 —
ランタイムのコメント "T.Parallel has no effect when fuzzing" が根拠）。

サブテストを並列にすると壊れる場合、**共有状態を持っているということなのでテストを直します**
（`t.Parallel()` を諦めるのではありません）。ハーネスやストアの構築を `t.Run` の閉包の内側へ移し、
ケースごとに組み立てます。

【家 + Go の実装挙動】

---

## D. `t.Run` サブテスト名の日本語

### D-1 形

`<分類>: <観測できる事実>` の形にします。区切りは**半角コロン + 半角スペース 1 つ**です
（全角コロンは使いません — 表記を 1 つに固定します）。→ `scripts/convention-gate.sh`（検査 2）

### D-2 分類は 8 語の閉じた語彙

**正常系 / 異常系 / 境界 / 冪等 / 並行 / 契約 / 回帰 / 性質**

**語彙を勝手に増やしません。** 増やすときは本規約の更新を伴います。

分類が迷うときは、**上から順に評価して最初に該当したものを採ります**（具体的なものが優先）。
この順序が固定されているので、判定が人によって揺れません。

```text
1. 生成器で任意の入力を振っているか（rapid / fuzz）        → 性質
2. 並行実行・競合・版衝突を観測しているか                  → 並行
3. 同じ操作を 2 回以上して結果が変わらないことを観測しているか → 冪等
4. 過去に壊れた挙動の再発防止か（issue / PR に由来）        → 回帰
5. 契約（OpenAPI・イベントスキーマ・型の存在）との一致か     → 契約
6. 入力が定義域の端か（0・空・上限・境界値）                → 境界
7. 期待がエラー・拒否・失敗か                              → 異常系
8. それ以外（期待が成功）                                  → 正常系
```

境界とエラーが同時に成り立つケース（「数量 0 は `ErrInvalidQuantity`」）は
**6 が 7 より上なので「境界」**になります。

### D-3 事実は断定形で書く

「〜する」「〜になる」で書きます。**「〜を確認する」「〜のテスト」は書きません**
（何を観測したかが読めないため）。

```go
t.Run("境界: 数量 0 は ErrInvalidQuantity", ...)   // 良い
t.Run("数量のテスト", ...)                          // 悪い
t.Run("数量 0 を確認する", ...)                     // 悪い
```

### D-4 名前に `/` を含めない

`go test -run` のサブテスト階層区切りになるためです。`・` か「と」に置き換えます。
→ `scripts/convention-gate.sh`（検査 3）

【Go の実装挙動 — 実測: `t.Run("異常系: 在庫項目が無い / 在庫不足は対象外")` は
`異常系:_在庫項目が無い_/_在庫不足は対象外` として階層に割れる。空白は `_` に変換される】

### D-5 Go 識別子・センチネル名・ステータスコードは原文のまま

`ErrInvalidSKU` / `409` のように、コード上の表記をそのまま埋めます。

### D-6 テーブル駆動の `name` フィールドも同一規約に従う

ケースが 3 件以上で構造が同一ならテーブル駆動、そうでなければ `t.Run` を並べます。

テーブル駆動にするときは**必ず `name` という名前のフィールドを持つ構造体**にします。
→ `scripts/convention-gate.sh`（検査 2' / 3'）

```go
cases := []struct {
    name string          // 位置指定の第 1 要素ではなく、name フィールドとして持つ
    segs []string
    want string
}{
    {name: "正常系: 添字はドットを挟まない", segs: []string{"lines", "[0]"}, want: "lines[0]"},
}
```

`map[string]T` をテーブルにして**キーをサブテスト名にするのは避けます**。名前フィールドが無いので
機械検査できず、加えて **map の反復順が非決定的**なのでサブテストの実行順が毎回変わります。

裏返して、`*_test.go` の **`name` フィールドはケース名に予約**します。ケース名ではない表示用の文字列
（比較対象のサーバ名など）には `label` のような別の名前を使ってください。検査 2' は `name:` に続く
文字列リテラルを悉皆的に拾うので、ケース名でないものを `name` に入れると**規約と検査の意味がずれます**。

> **既知の限界**: `t.Run(fmt.Sprintf(…), …)` のように**サブテスト名を実行時に組み立てる場合、
> 機械検査できません**（名前が静的に存在しないため）。この形を使うときは、書式文字列そのものに
> 分類を織り込んでください。
>
> ```go
> t.Run(fmt.Sprintf("正常系: サーバ %d 本でもグレースフルに停止する", n), ...)
> ```

---

## E. テストの日本語コメント

### E-1 doc コメントは宣言名で始める

```go
// mustLine はテスト用に注文明細を組み立てるヘルパー。
func mustLine(t *testing.T, sku string, qty int) order.OrderLine {
```

句点「。」で終えます。→ `godot`（実測: `godot` は日本語の「。」を句点として認識する）

【Go: go.dev/doc/comment】

### E-2 本文のコメントは why のみ

コードの逐語訳を書きません。計算の根拠（`// 小計 3600 = 単価 1200 × 数量 3`）や、
なぜこの値なのかを書きます。

### E-3 決定的アサーション対を張るときは、対である理由を残す

**正 + 負を 1 回の観測で同時に満たす**アサーション対を書くときは、
**片方だけでは壊れた実装も通ってしまうこと**をコメントで明示します。

```go
// この 2 つは対で意味を持つ。outbox が空だけなら「全部消す」実装でも通り、
// events に 2 行あるだけなら「消さない」実装でも通る。両方を同じ観測で
// 満たして初めて「配送キューから履歴へ責務が移った」と言える。
assert.Equal(t, 0, countRows(t, db, "ordering.outbox"))
assert.Equal(t, 2, countRows(t, db, "ordering.events"))
```

【家】

### E-4 `require` / `assert` のメッセージは期待した事実を日本語で書く

省略してよいのは自明な `NoError` のみです。

### E-5 `TODO` の書式

`TODO(担当者 or issue 番号): 内容` の形にします。理由は本文に書きます。

【Go: 標準ライブラリの慣習（`TODO(rsc):` 等）】

### E-6 語彙は GLOSSARY に合わせる

該当コンテキストの `contexts/<ctx>/GLOSSARY.md` に合わせます。
境界を跨ぐ同名語の対比は [glossary.md](./glossary.md) にあります。

---

## 機械強制の一覧

| 規約 | 強制手段 | 重大度 |
| --- | --- | --- |
| C-1b 主題の一意性 | `scripts/convention-gate.sh` 検査 1 | fail |
| C-3 ヘルパーの `t.Helper()` | `thelper` | fail |
| C-4 外部テストパッケージ | `testpackage` | fail |
| C-4c testify の引数順など | `testifylint` | fail |
| C-5 `t.Parallel()` | `paralleltest`（domain・application のみ）+ `tparallel`（全体） | fail |
| D-1 / D-2 8 語語彙 | `scripts/convention-gate.sh` 検査 2 | fail |
| D-4 `/` 不在 | `scripts/convention-gate.sh` 検査 3 | fail |
| D-6 テーブルの `name` | `scripts/convention-gate.sh` 検査 2' / 3' | fail |
| E-1 doc コメントの句点 | `godot` | fail |
| C-1 / C-2 の形、C-4b、D-3、D-5、E-2〜E-6 | **人間のレビュー観点**（機械強制なし） | — |

すべて `make conventions` と `make lint` で走り、`make ci` に含まれます。CI も同じターゲットを呼びます。

**追加した rule は必ずカナリア検証してください** — 一時的に違反を注入して当該 rule が報告することを確認し、
**注入した違反がその linter に到達したことまで確かめて**から revert します。別のエラーに飲まれると
偽陰性になります。詳しくは [../CONVENTIONS.md](../CONVENTIONS.md) の G-1 / G-2 を参照。
