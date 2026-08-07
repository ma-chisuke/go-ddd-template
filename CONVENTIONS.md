# 規約（CONVENTIONS）

このテンプレートで一貫して守る Go とドメイン駆動設計の規約をまとめます。
新しいコンテキストやユースケースを足すときは、この規約に沿ってください。

**テストの名前・日本語の書き方・コメントの規約は
[docs/testing-conventions.md](docs/testing-conventions.md) にあります**（この文書はテストの
道具立て — testify / gomock / カバレッジ床 — だけを「テスト」節で扱います）。

各ルールの末尾の【 】は**出典**です。〔Go〕は Go の公式・準公式文書または標準ライブラリの実測に
裏づけがあるもの、〔家〕はこのプロジェクトのハウスルールです。機械強制されるものは
「→ `<ツール名>`」で強制手段を書いてあります。**強制手段のないルールは人間のレビュー観点**です。

## パッケージ / ファイルの構成

### 基本

- **1 ディレクトリ 1 パッケージ**。
- パッケージ名は**短く・小文字・単数形**。冗長な繰り返し（stutter）を避けます。
  例: `inventory.Module` とし、`inventory.InventoryModule` とはしません。
- **層のパッケージは層の名前で命名**します（B-8）。`internal/domain` は `package domain`、
  `internal/application` は `package application` です。
- **生成ファイルは隔離**します。ogen は `internal/adapter/inbound/openapi/` に、
  sqlc は `internal/adapter/outbound/postgres/sqlcgen/` に出力し、**手で編集しません**。

### B-1 ファイル名

snake_case にし、**主要型名に対応させます**。例: `StockItem` → `stock_item.go`。

### B-2 基準は「1 ファイル = 1 つの読み単位（凝集）」

**行数の下限は設けません。** 短いファイルそのものを禁止しません。

【Go: 「1 ファイル 1 型」は Go の慣習ではない — `net/url/url.go` は 1309 行に公開型 6 個、
`time/time.go` は 4 個、`net/http/server.go` は 9 個。かつ標準ライブラリの 4505 ファイルのうち
**26.8%（1207 件）が 40 行未満**である（build tag 付き 673 / `doc.go`・`generate.go` 42 /
生成物 36 / それ以外 456）】

> **以前の版では「1 ファイル 1 主要型を基本とし」と書いていましたが、凝集ベースに改めました。**
> 集約・エンティティは 1 型 1 ファイルを維持しますが、**相互に依存して常に一緒に読む小さな型**
> （値オブジェクト群）は束ねます。

### B-3 束ね方は次の 3 則で決める

結果が入力から**一意に決まる**ようにしてあります。美意識ではなく規則が決めます。

1. **型はそれが属する概念のファイルに置く** — **技術的な種類**（識別子・値オブジェクト）で
   まとめません。`OrderID` は注文の一部なので `order.go` へ、`SKU` は明細が指す商品なので
   `order_line.go` へ置きます
2. **それ以外の小さな型は、それを最も使う型のファイルに同居させる** —
   明細の数量は明細と、集約の状態機械は集約と同じファイルへ
3. **40 行以上ある型は単独ファイルのままでよい** — 束ねる圧力は「無駄に小さいファイル」にだけかかる

> **以前の版の則 1 は「識別子は族でまとめる（`identifiers.go` に集める）」でしたが、撤回しました。**
> `identifiers.go` は「文字列を包む識別子型」という**技術的な種類**による容れ物であり、
> B-4 が禁じる `value_objects.go` と同じ species だったからです。B-2 の基準は
> 「1 ファイル = 1 つの読み単位（凝集）」であって「似ている型を集める」ではありません
> — `OrderID` を読むのは `Order` を触っているときであって、`CustomerID` や
> `ReservationRef` と読み比べるときではありません。再発は検査 4 の禁止語幹
> （`identifiers` を追加済み）が機械的に止めます。

この 3 則の帰結として、注文の `Quantity`（28 行）は `order_line.go` に束ねられ、
在庫の `Quantity`（55 行）は `quantity.go` に残ります。**同じ規則に異なる入力を通した結果**です
（どちらも `package domain` の型で、属する境界だけが違います。B-8）。

### B-4 カタログ的なファイル名を禁止する

`value_objects.go` / `types.go` / `models.go` / `utils.go` / `helpers.go` / `common.go` / `misc.go` /
`identifiers.go` は使いません。ファイル名は中身の**概念を名指し**します。`*_test.go` 版も同じく禁止です。
（`identifiers.go` は B-3 則 1 の撤回に伴って追加しました — 「文字列を包む識別子型」は
**技術的な種類**であって概念ではなく、`value_objects.go` と同じ誤りです。）
→ `scripts/convention-gate.sh`（検査 4。生成物は除外 — sqlc の `models.go` のように
ツールが名前を決めるものは改名できないため）

`errors.go` と `ports.go` は「番兵」「ポート」という**技術的な種類**で束ねているのに許されます。
なぜ許されるのかは次の B-5 を参照してください（species が違うからではなく、**名前が強い約束を
背負っている**からです）。B-5 の表は**閉じて**おり、そこに載っていない容れ物は B-4 が禁じます。

【家 — A-8 の同じ理由（意味のない容れ物を作らない）をファイル粒度に適用したもの】

### B-5 特別なファイル名の役割

| ファイル名 | 役割 |
| --- | --- |
| `doc.go` | package doc のみ |
| `errors.go` | 番兵と検証規則 |
| `ports.go` | そのパッケージの outbound ポート宣言（`interface`）**だけ**を含み、かつ非テストファイルにおけるポート宣言は**全て**ここにある（双方向の約束。下記） |
| `generate.go` | `//go:generate` のみ |
| `<subject>_test.go` | 通常のテスト |
| `*_internal_test.go` | 内部テスト（`package <pkg>`） |
| `export_test.go` | 内部シンボルを外部テストパッケージへ橋渡しする薄い別名だけ |
| `*_integration_test.go` | `//go:build integration` |
| `*_property_test.go` | 性質テスト |
| `*_fuzz_test.go` | fuzz ターゲット |

**この表は閉じています。** 新しい役割名を足すには規約の改定が要ります。ここを開いておくと
`services.go` / `stores.go` / `stubs.go` が際限なく生えて、B-4 が骨抜きになるからです。

#### `ports.go` の双方向の約束

この表に載るファイルは「技術的な種類で束ねた容れ物」でありながら B-4 の禁止を免れます。
免れる条件は **species が違うこと**ではなく、**「この中身しか入らない／この中身は全部ここにある」
という強い約束を名前が背負っていること**です。`ports.go` は次の 2 文でその条件を満たします。

- **(a) 純度** — `ports.go` は、そのパッケージの outbound ポート宣言（`interface`）**だけ**を
  含みます。`func` 宣言は置きません（概念を名指したファイルへ移します）。
  型エイリアス（`type UnitOfWork = uow.UnitOfWork[Repos]`）はエイリアス先がインターフェースであり、
  ポート語彙の一部なので (a) の例外として置けます
- **(b) 全数性** — そのパッケージの outbound ポート宣言は、**非テストファイルにおいては全て**
  `ports.go` にあります。テストローカルな `interface` はポートではない（そのパッケージが外部に
  要求する依存ではなく、テストが自分の都合で作る足場にすぎない）ので (b) の対象外です

(b) が破れた瞬間に「ここを開けば外部依存が一望できる」というこの名前の価値はゼロになります。
だから 2 本の検査で機械強制します（検査 10 = (b) / 検査 11 = (a)。下の一覧表の #17 / #18）。

`ports.go` の中身は、トランザクション境界でコメント見出しにより 2 群へ割ります。集約の代償として
「tx に載る／載らないがファイル名で見える」性質が失われるので、その区別を内部に残すためです。

- **UoW に束ねられるポート** — `Repos` 経由でのみ取得し、トランザクションの内側で使う
- **UoW の外で呼ぶポート** — 外部同期呼び出し（ACL）・プロセス内配信・時刻。tx には載らない

> **既知の限界（検査 10）**: 型引数制約の `interface`（`type Number interface { ~int | ~float64 }`
> のような、ポートではない純粋な generics 制約）を application 層に書くと、検査 10 はこれも
> 違反として報告します。除外規則は**設けません** — 制約とポートは同じ構文で書かれるため
> 除外の判定を機械化できず、「rule はあるが何も検査していない」状態を生みやすいからです。
> fail 自体が正しい合図でもあります（制約を domain 層へ移すか、B-5 の表を改定して新しい役割名を
> 足すかを人間が選べる）。将来これが実際に起きたら、除外を足すのではなく**B-5 の改定として
> 扱ってください**。

### B-6 ファイル内の並び順

package doc → import → 定数 → 型 → コンストラクタ → 公開メソッド → 非公開メソッド → ヘルパー。【家】

### B-7 上限の目安（機械強制しない）

1 パッケージ 12 ファイル / 1 ファイル 400 行。超えたらサブパッケージ化かファイルの束ね直しを検討します。【家】

> なお `scripts/convention-gate.sh` の検査 7 は「公開型 1 個だけを含む 40 行未満のファイルが
> 同一パッケージに 3 つ以上」を **warn** で報告します。B-2（短いファイルを禁止しない）と矛盾しません
> — 前者は「短いファイルの乱立」を人間の判断に委ねて警告するだけで、fail にはしません。

> **ドメイン層にはこの逃げ道がありません。** B-8 がサブパッケージ化を禁じるので、
> ドメインパッケージが膨らんだときの手は**ファイルの束ね直し**（B-3）だけです。
> それでも 12 ファイルに収まらないなら、疑うべきはファイル分割ではなく**境界の引き方**です
> — 1 つの境界づけられたコンテキストに 2 つのモデルが同居している可能性があります
> （`docs/why-these-boundaries.md`）。

### B-8 層のパッケージは層の名前で命名する

パッケージ名は**中身ではなく層**で決めます。`internal/domain` は `package domain`、
`internal/application` は `package application` です。`internal/domain/order`（`package order`）の
ように中身で名づけると、同じ 4 層のうち application だけが層で・domain だけが中身で命名される
**非対称**になります。

**1 コンテキストにドメインパッケージは 1 つ**とし、サブパッケージには割りません。
集約・値オブジェクト・ドメインイベントは 1 つのモデルとして相互に参照し合うので、割れば
**相互 import**（Go では循環 import になり、そもそもコンパイルが通らない）か、
それを避けるための不自然な型の押し出しかのどちらかを強いられます。**ひとつのモデルは
ひとつのパッケージ**です。型が増えたら**ファイル**を足します（束ね方は B-3 が決めます）。
→ `scripts/convention-gate.sh`（検査 9）

呼び出し側にこの命名が現れることで、**依存の向きがコードを読むだけで見える**という副産物も
あります。`domain.NewOrder(...)` / `domain.StockItem` の `domain.` は「外側から内側を呼んでいる」
という印であり、逆向き（ドメインから `application.` や `httpapi.`）は depguard の
`domain-purity` が禁じています。

**名前の衝突も同時に消えます。** 従来は公開ファサード `contexts/inventory/inventory.go`
（`package inventory`）とドメイン（`package inventory`）が同名で、ファサードの中に書かれた
`inventory.StockItem` が「自分自身」ではなく「import したドメイン」を指す、という罠がありました。

【家 — この 1 階層は子が常に 1 つだけの素通しディレクトリでした。他の階層
（`adapter` 2 / `inbound` 2〜4 / `outbound` 2〜4）は実際に複数を束ねているので畳みません】

### B-9 集約ルートを型で名指し、非ルートをポインタで漏らさない

各コンテキストの `internal/domain` は、集約ルートの契約 `AggregateRoot` を定義します。

```go
type AggregateRoot interface {
	Version() int
	MarkPersisted(version int)
	PullEvents() []DomainEvent
}
```

**3 つのメソッドは検査のために足したものではありません。** 集約ルートは整合性の境界だから
楽観的排他制御の版を持ち（`Version` / `MarkPersisted`）、ドメインイベントの発生源だから
イベントを引き出せます（`PullEvents`）。契約は既に在る事実に名前を与えているだけです。

`DomainEvent` がコンテキスト固有なので、この契約も**コンテキストごとに定義**します
（`shared` には置きません。値オブジェクトをコンテキストごとに所有するのと同じ理由です）。

各集約ルートには**コンパイル時表明**を、その集約ルートを宣言したファイルに置きます。

```go
var _ AggregateRoot = (*Order)(nil)
```

lint ではなく**コンパイル**で固定します。3 メソッドのいずれかが失われればビルドが落ちます。
この表明は「どの型が集約ルートか」の**唯一の情報源**でもあり、検査 12 / 13 / 14 はここから
集約ルートの集合を得ます。規約に列挙したリストや doc コメントの語を根拠にしません
（リストは集約を足すたびに手で直す必要があり、doc への `grep` は実体ではなく主張の検証に
なってしまいます）。

そのうえで、**集約ルートでないドメイン型へのポインタを、公開シグネチャに出しません**。

> `contexts/*/internal/domain/` の公開メソッドおよび公開関数のシグネチャ（レシーバを除く
> 引数と戻り値）に、`AggregateRoot` を満たさない同パッケージの型へのポインタ
> （`*T` / `[]*T`）が現れてはならない。

→ `scripts/convention-gate.sh`（検査 12）

**目的は「集約の操作は集約ルートのみを通じて行う」を、検査ではなく型で保証すること**です。
外部が受け取るのは値のコピーであり、コピーに何をしても集約の状態は変わりません。
`StockItem.Reservations()` は `[]Reservation` を返し、`Order.Lines()` は `[]OrderLine` を
返します。集約の内部表現はポインタのままで構いません（`Confirm` / `Release` は子の状態を
直接書き換えます）。**問題は外へポインタが出ることだけ**です。

判定に使わないもの: レシーバの種別、引数の個数、戻り値の個数、メソッド本体。
**型の構造だけ**を見ます。レシーバを見ないのは、集約ルートを `*Order` で扱うのが正しく、
子も内部ではポインタでよいからです。

**これは Go の規則ではありません。** Go Code Review Comments にも Google Go Style Guide にも
内部状態の返却・防御的コピーに関する項は存在せず、禁止も推奨もされていません（一次資料に
当てて確認済み）。出自は「内部の可変状態への参照を外へ渡さない」という言語非依存の
カプセル化の原則で、Java の `Collections.unmodifiableList` や C# の `IReadOnlyList<T>` が
解いているのと同じ問題を、DDD の「集約の内部は集約ルートを通してのみ」に重ねたものです。

**正直な限界**: 子が内部にポインタ・slice・map のフィールドを持つ場合、値コピーでもその
参照は共有されます。現在の `Reservation`（`ref` / `qty` / `status` / `expiresAt`）は
いずれも非ポインタなので該当しません。将来子にコレクションを持たせるときは、この限界が
顕在化します。そのときに深いコピーか検査の拡張かを判断してください（現ツリーに該当が
無いので、先んじて縛りません）。

### B-11 集約ストアの実装は `<集約名>_store.go` に置く

> 集約ストアポートのコンパイル時表明（`var _ application.<X>Store = (*<t>)(nil)`）を含む
> ファイルは、`<集約名>_store.go` と名づけなければならない。

→ `scripts/convention-gate.sh`（検査 14）

期待するファイル名は表明が名指すポート名から機械的に導きます
（`OrderStore` → `order_store.go`、`StockStore` → `stock_store.go`）。

インメモリアダプタでは、1 つの集約について 3 つの型が同じファイルに同居します。

| 役割 | 型 | ポートの実装か |
| --- | --- | --- |
| 確定済みデータの保持（backing store） | `OrderRows` / `StockItemRows` | **いいえ** |
| トランザクション束縛のポート実装 | `txOrderStore` / `txStockStore` | はい |
| 読み取り専用のポート実装 | `readOrderStore` / `readStockStore` | はい |

**`Store` の語はポートとその実装にだけ使います。** backing store を素朴に `OrderStore` と
名づけると `application.OrderStore`（ポート）と同名になり、読み手が「これはポートの実装か」と
誤読します。`orderRecord`（行）を保持するものが `Rows`（行の集まり）という対応も自然です。

トランザクション機構そのもの（`UnitOfWork` / `txState` / `applyGroup` / `txOutbox`）は
`uow.go` に残します。これは検査 14 を通すための便宜ではなく**凝集の改善**です — 注文ストアの
backing・tx 実装・読み取り実装は同じ読み単位に属し、以前は「注文ストアを読むのに `uow.go` と
`store.go` を行き来する」状態でした。結果として `uow.go` は表明を持たなくなり、
除外リストなしで検査 14 の対象外になります。

**これは B-1 全体の機械強制ではありません。** 素朴な B-1 検査（ファイル名 = 主要型名）を
全ツリーに当てると約 90 件が「違反」になり、その大半は真の違反ではありません（生成物・
パッケージ名と同名の主ファイル・概念を名指ししたファイル）。B-1 の字義は普遍規則ではなく
一例であり、実際に効いている B-4 の肯定形「ファイル名は中身の概念を名指しする」は機械判定
できません。検査 14 は集約ストアという 1 つの族だけを対象にします。

## 命名

### Go 由来（強制）

- **A-1** 変数名の長さは**スコープの長さに比例**させます。1〜3 行のスコープなら `i` `s` `o` でよく、
  パッケージ横断なら説明的にします。【Go: Go Code Review Comments "Variable Names"】
- **A-2** レシーバ名は 1〜2 文字で、**同じ型の全メソッドで一貫**させます。`this` / `self` は使いません。
  【Go: Go Code Review Comments "Receiver Names"】→ `revive receiver-naming`
- **A-3** getter に **`Get` を付けません**（`o.Status()`。`o.GetStatus()` とはしません）。
  生成コード（ogen の `GetName()` 等）は例外です。
  【Go: Effective Go「Getters」（「it's neither idiomatic nor necessary to put `Get` into the
  getter's name」）+ Google Go Style Guide（「should not use a Get or get prefix」）】
- **A-4** インタフェースは能力を表す **`-er`**（`StockReserver` / `EventPublisher`）。1 メソッドを優先します。
  【Go: Effective Go "Interface names"】
- **A-5** 予期される失敗は **`Err<Reason>`** センチネルにし、ラップは **`%w`** で行います。
  【Go: 標準ライブラリ `io.EOF` / `os.ErrNotExist`、`errors.Is`】
- **A-6** エラー文言は**大文字始まりにせず、句読点で終えません**。
  【Go: Go Code Review Comments "Error Strings"】→ `stylecheck` ST1005
  （**日本語の文言は誤検出されません** — 実測: 「在庫が不足しています。」「…は必須です」は無指摘、
  英語の `"Stock is insufficient."` のみ指摘）
- **A-7** **パッケージ名 = ディレクトリ名**。標準ライブラリと名前が衝突する場合は
  **ディレクトリ名を変えます**（`http/` に `package httpapi` を置くのではなく `httpapi/` にします）。
  **import 別名で回避しません**（→ I-8）。
  【Go: Effective Go "Package names"、Go Blog "Package names"】→ `scripts/convention-gate.sh`（検査 5）
- **A-8** `util` / `common` / `misc` / `helpers` のような**ゴミ箱パッケージを作りません**。
  【Go: Go Blog "Package names"】
- **A-9** 頭字語は**一貫した大小**にします（`URL` か `url`。`Url` にしません。`ServeHTTP` であって
  `ServeHttp` ではありません）。【Go: Go Code Review Comments "Initialisms"】
- **A-9b** **MixedCaps / mixedCaps を使い、下線で複数語をつなげません。** これは**定数にも及びます** —
  非公開定数は `maxLength` であって `MaxLength` でも `MAX_LENGTH` でもありません（他言語の慣習を
  破ってよい）。【Go: Effective Go「MixedCaps」、Go Code Review Comments "Mixed Caps"】
  **テスト関数名は別規則** → [docs/testing-conventions.md](docs/testing-conventions.md) の C-2
- **A-10** コンストラクタは **`New<Type>(...) (Type, error)`** の形にします。
  検証を伴う値オブジェクトや集約は、不正値を弾いてから返します。【Go: 標準ライブラリの慣習】
- 型・エクスポートされる識別子は **PascalCase**、非公開は **camelCase**。

### ハウスルール（推奨・非強制）

- **A-11** 述語（bool を返す関数・フィールド）は**真であることが読める名前**にします
  （`IsZero` / `HasPrefix` / `CanCancel`）。**否定形の名前を避けます**（`notFound` ではなく `found`）。【家】
- **A-12** スライスは複数形、map は **`<値>By<キー>`**（`eventsByName`）。【家】
- **A-13** 変換関数は役割で使い分けます: **`Parse<T>`**（文字列 → 値、`error` を返す）/
  **`To<T>`**（値 → 値、失敗しない）/ **`As<T>`**（ポインタ経由の取り出し）/
  **`Must<T>`**（失敗時に panic。**テストと初期化のみ**）。
  【Go: 標準ライブラリ `strconv.ParseInt` / `strings.ToUpper` / `errors.As` / `template.Must` に倣う】
- **A-14** 略語はドメイン用語（`SKU` / `qty`）と Go 慣習（`ctx` / `err` / `req` / `res` / `cfg`）のみ
  許可し、**独自の省略**（`inv` / `ordr` / `mgr`）は作りません。【家】
- **A-15** 定訳を固定します: `ctx context.Context` / `err error` / `tx` / `repos`（UoW のリポジトリ束）/
  テストの期待値と実測値は **`want` / `got`**。【家、`want`/`got` は Go: Go Wiki "TableDrivenTests"】

## Go 由来の実装作法

命名以外の Go 由来の作法です。**いずれも出典があります**が、**この節は機械強制していません**
（I-2 のみ既に `errcheck` で強制されています）。人間のレビュー観点として読んでください。

- **I-1 Indent Error Flow** — 正常経路を最小インデントに保ちます。エラーを先に処理して
  `return` / `continue` し、`else` に正常経路を入れません。【Go: Go Code Review Comments "Indent Error Flow"】
- **I-2 Handle Errors** — エラーを `_` で捨てません。検査し、処理し、返し、または
  （真に例外的な場合のみ）panic します。【Go: 同 "Handle Errors"】→ `errcheck`
- **I-3 In-Band Errors** — `-1` や `""` のような帯域内エラー値を返さず、`(value, ok)` または
  `(value, error)` を返します。【Go: 同 "In-Band Errors"】
- **I-4 空スライスの宣言** — `var s []T`（nil スライス）を既定にし、`[]T{}` は
  「非 nil であること」に意味があるときだけ使います。【Go: 同 "Declaring Empty Slices"】
- **I-5 Named Result Parameters / Naked Returns** — naked return を使うためだけに結果に名前を
  付けません。naked return は短い関数に限ります。【Go: 同 "Named Result Parameters" / "Naked Returns"】
- **I-6 インタフェースは使う側で定義する** — インタフェースは**その値を使うパッケージ**に置き、
  実装側に「モックのために」置きません。関数は具体型を返します。【Go: 同 "Interfaces"】
  **このテンプレートの「ポートは application 層に置き、アダプタが実装する」は、この Go の作法と
  そのまま一致します**（DDD の依存性逆転と Go の慣習が同じ結論に着く点は覚えておく価値があります）。
- **I-7 ゼロ値を有用にする** — 型のゼロ値がそのまま使える設計を優先します
  （`bytes.Buffer` の「ゼロ値は使用可能な空のバッファ」、`sync.Mutex` にコンストラクタがないこと）。
  【Go: Effective Go の "Data > Allocation with `new`" 節】
  このテンプレートでは `Quantity{}` が数量 0 として機能している例がこれに当たります。
- **I-8 import の別名は名前衝突の回避に限る** — それ以外の目的で import をリネームしません。
  【Go: Go Code Review Comments "Imports"】**このルールが `http/` ではなく `httpapi/` という
  ディレクトリ名を支持します**（別名で回避するのではなく、衝突しない名前を最初から与える）。
- **I-9 doc コメントの網羅** — 公開されている全てのトップレベル名と、非自明な非公開の型・関数宣言に
  doc コメントを付けます。【Go: 同 "Doc Comments"】
- **I-10 レシーバ型** — 迷ったらポインタレシーバ。小さく不変な struct や基本型は値レシーバ。
  【Go: 同 "Receiver Type"】
- **I-11 Pass Values** — 「バイトを節約するため」だけの理由でポインタを渡しません。【Go: 同 "Pass Values"】
- **I-12 panic しない** — 通常のエラー処理に panic を使わず、`error` と多値返却を使います。
  【Go: 同 "Don't Panic"、Effective Go】（A-5 と対をなします）

> **なぜ機械強制しないのか**: `revive` の `indent-error-flow` / `early-return` で I-1 の一部は
> 強制できますが、**既存コードへの影響範囲が未計測**であり、影響を測らずに fail を増やすと
> 是正作業が発散します。機械強制は次の改善候補として記録してあります。

## 言語ポリシー

- **F-1 日本語**: doc コメント・行コメント・エラー文言・ログメッセージ・`t.Run` 名・ドキュメント
- **F-2 ASCII**: 識別子・パッケージ名・ファイル名・テスト関数名の修飾部・ログの属性キー・
  イベント名（`ordering.order_placed`）・DB 識別子・contracts の記述子
- **F-3** 日本語と英数の間に**半角スペース 1 つ**を入れます。
  → `scripts/convention-gate.sh`（検査 6）

  これは **JTF 日本語標準スタイルガイド 3.1.1（全角と半角の間に空白を入れない）とは意図的に
  異なる**選択です。根拠: この `CONVENTIONS.md` を
  `grep -o '[ぁ-んァ-ヶ一-龠] [A-Za-z0-9]'`（空白あり）と
  `grep -o '[ぁ-んァ-ヶ一-龠][A-Za-z0-9]'`（空白なし）で数えると **178 箇所 / 0 箇所**でした。
  **textlint 系ツールは既定で逆を指摘します**（実測: エディタの textlint 拡張が
  `jtf-style/3.1.1` を severity Error で報告した）。明記しないと**ツール既定で 178 箇所が
  一括反転されうる**ので、規約に固定して機械検査で守ります。【家】

## エラー

- 予期される失敗は **`Err<Reason>` センチネルエラー**として定義し、値で返します
  （`panic` しません）。例: `ErrStockItemNotFound`, `ErrInvalidQuantity`。
- ラップするときは **`%w`** を用いてセンチネルを保持し、`errors.Is` による判定を
  壊さないようにします。
- 回復不能・想定外の異常（暗号乱数源の故障など）に限り `panic` を許容します。

## HTTP エラー応答（RFC 9457 / Problem Details）

エラー応答は **`application/problem+json`** で返し、本文は契約の `ProblemDetails`
（ogen 生成型）で組み立てます。手書き JSON は書きません。

### エラーが生まれる 4 つの経路

| # | 経路 | 発火点 | 実装 |
| --- | --- | --- | --- |
| E1 | リクエストのデコード／契約検証 | ogen `ErrorHandler` | `internal/adapter/inbound/httpapi/problem.go` |
| E2 | ルーティング不一致（未定義パス） | ogen `NotFound` | 同上 |
| E3 | メソッド不許可 | ogen `MethodNotAllowed` | 同上 |
| E4 | ハンドラ戻り値のエラー | 生成された `NewError` | `internal/adapter/inbound/httpapi/errmap.go` |

E1〜E3 は **`Handler.ServerOptions()` を `NewServer` に渡すことで注入**します。渡し忘れると
ogen の既定ハンドラが `{"error_message": "operation placeOrder: decode request: ..."}` を返し、
内部実装の詳細が外部の観測面に漏れます。本番の合成ルート（`ordering.go` / `inventory.go`）も
テストも、必ず同じ `ServerOptions()` 経由で組み立てます。

### 契約に宣言するステータスコード（明示コード + `default`）

**公開契約**（`ordering/openapi.yaml` / `inventory/openapi.yaml`）では、各オペレーションが
**自分が実際に返しうる 4xx/5xx を明示的に宣言**し、加えて `default` を宣言します。宣言済みの
どのコードにも当てはまらない未処理のエラーは `default`（= 500 Internal Server Error。
`errmap.go` の `classify` の `default` 分岐）へ落ちます。契約が「このオペレーションはどの
ステータスを返すか」を正直に語るのが目的で、各オペレーションのコードはそのコンテキストの
`classify` から導きます（`grep` の羅列ではなく、実際に到達するものだけ）。

| コンテキスト | オペレーション | 宣言するコード（+ `default`=未処理→500） |
| --- | --- | --- |
| ordering | `placeOrder` | 400, 409, 422, 503 |
| ordering | `getOrder` | 400, 404, 422 |
| ordering | `cancelOrder` | 400, 404, 409, 422 |
| inventory（公開） | `replenishStock` | 400, 409, 422 |
| inventory（公開） | `getStock` | 400, 404, 422 |

`getOrder` / `cancelOrder` の 422 は、パスパラメータ `id` がドメイン検証
（`domain.NewOrderID` → `ErrInvalidOrderID`）を通るため実際に返りうります。415（メディアタイプ
非対応）と、経路レベルの 404（未定義パス）/ 405（メソッド不許可）は E1〜E3 のトランスポート層
（`ServerOption` ハンドラ）が生成するもので、オペレーション単位では宣言できません（同じ
`ProblemDetails` 形状は共有します）。

明示コードを宣言すると、ogen はハンドラの戻り型を成功応答とエラー応答をまとめた `<Op>Res`
**union**（`PlaceOrderRes` など）にします。`default` が残るので `NewError` も生成され、経路は
変わりません。手書きハンドラは戻り型を union に変えるだけ（成功応答型は ogen のマーカーで
union を満たすので `return toOrderView(view), nil` はそのまま、エラーは `return nil, err` で
`NewError` へ委譲）で、本体ロジックは変えません。宣言した各コードごとに ogen が生成する変種型
（`PlaceOrderBadRequest` など）はハンドラからは使いません（実行時のステータスは `classify` →
`ProblemResponseStatusCode` が動的に決めます。明示コードは契約の正直さのための宣言）。

**内部契約**（`inventory/internal.openapi.yaml`）は例外で、エラーは `default` だけにまとめ、
オペレーション単位の明示コードは**あえて宣言しません**。この契約から生成されるのはサーバでは
なく**クライアント**（`clients/inventory`）で、その唯一の利用者は注文コンテキストの腐敗防止層
（ACL / `aclhttp`・`eventhttp`）だからです。ACL はステータスコードの区分（4xx か 5xx か）
だけを見て自分の番兵へ翻訳します（規則 R-16）。ogen は `default` だけのとき、非 2xx をすべて
1 つの型付きエラー（`ProblemResponseStatusCode`）としてクライアントへ返します。ここに明示
コードを足すと、宣言済みコードがエラーではなく union の**値**（`ReserveStockConflict` など、
`err == nil`）としてクライアントに返るようになり、ACL の「非 2xx = エラー」翻訳が壊れます。
ACL はコード別分岐を必要としないので、明示コードは利益ゼロで結合だけを増やします。よって
内部契約は `default` 集約のままにします。

### `type` URI（問題種別）

`type` は `about:blank` ではなく **種別ごとの安定した URI** です。クライアントは `status` では
なく `type` で分岐します。同じ `status` でも原因が異なれば別の URI を与えます。

| type サフィックス | status | 意味 |
| --- | --- | --- |
| `validation-error` | 400 | リクエストが API 契約に適合しない |
| `unsupported-media-type` | 415 | `Content-Type` がサポート外 |
| `not-found` | 404 | **そのようなエンドポイントが無い**（URL の誤り） |
| `method-not-allowed` | 405 | メソッド不許可 |
| `invalid-input` | 422 | ドメインの検証規則違反 |
| `resource-not-found` | 404 | **エンドポイントはあるが対象が無い**（ID の誤り） |
| `conflict` | 409 | 現在の状態と矛盾する操作 |
| `reservation-rejected` | 409 | 在庫予約の拒否（注文コンテキストのみ） |
| `service-unavailable` | 503 | 依存サービス不達（注文コンテキストのみ） |
| `internal-error` | 500 | 予期しないエラー |

台帳の実体は `shared/problem/type_uri.go` です。`title` は種別と **1 対 1** で対応させ、
`title` から `type` を逆引きできる状態を保ちます（404 が 2 つ、409 が 2 つあるので、
HTTP の理由句をそのまま使うと逆引きできなくなります）。

**利用者による差し替え手順**: URI の名前空間は各コンテキストの
`internal/adapter/inbound/httpapi/problem.go`（内部 API は `internalhttp/problem.go`）にある
**`problemTypeBase` 定数 1 箇所**です。自分の名前空間へ書き換えてください。URI は識別子
であり、解決可能な文書ページを公開する必要はありません。

### `detail` に何を書いてよいか

`detail` は**経路ごとの定型文**です（`shared/problem/type_uri.go` の `Detail*` 定数）。
次のものを応答本文に含めてはいけません。

- `err.Error()` の結果をそのまま載せること
- ogen / Go 由来の文言（`operation ...`、`decode request`、`unexpected byte`、`callback:`）
- Go の型名・パッケージパス・スタックトレース
- **問題となった受信値のエコーバック**（SKU・数量・利用可能在庫・予約参照など）

排除した情報は失わせません。4xx は `WarnContext`、5xx は `ErrorContext` で元のエラーを
サーバ側ログに残し、相関 ID（`CorrelationMiddleware`）で運用者が追跡できるようにします。

### `invalid-params`（違反フィールドの一覧）

RFC 9457 の拡張メンバーとして、違反したフィールドを機械可読に伝えます。

```json
{
  "type": "https://github.com/example/go-ddd-template/problems/invalid-input",
  "title": "Unprocessable Entity",
  "status": 422,
  "detail": "入力値がドメインの規則を満たしていません",
  "invalid-params": [
    { "name": "lines[0].unitPrice.amount", "code": "invalid_money_amount", "reason": "0 以上の値を指定してください" }
  ]
}
```

- `name` は**ドット + 角括弧記法**のフィールドパス（`lines[0].unitPrice.amount`）。
- `code` は機械可読な安定識別子。クライアントはこれで分岐します。
- `reason` は `code` から引く定型文（人間向けの補助）。**受信値も閾値も含みません**。

クライアントが依存すべき 2 つの限界を明示します。

1. **400（契約検証）では配列の添字が付きません。** ogen が `Decode()` 経路のエラーに位置を
   残さないためで、実装の手抜きではありません。422（ドメイン検証）では添字が付きます。
   **クライアントは添字の有無に依存した解析をしてはいけません。**
2. **`invalid-params` は網羅を保証しません。** 判明した違反のみを含みます。`jx` の
   ストリーミングデコーダはコールバックが最初にエラーを返した時点で走査を打ち切るため、
   配列の別要素や別の枝にまたがる `Decode()` 失敗は列挙できません。列挙できるのは
   「同一オブジェクト内の兄弟の必須欠落」と「`Validate()` 経路の複数制約違反」です。

フィールドを特定できない場合（不正 JSON、サポート外 Content-Type など）は、
**`invalid-params` をキーごと省略**します。空配列は返しません（「違反フィールドが 0 件」と
「特定できなかった」を区別するため）。

例外が 1 つあります。**リクエストボディそのものが空**のときは、個々のフィールドではなく
ボディ全体が問題なので `name` に擬似パス **`body`**（`ogenproblem.BodyParamName`）を使い、
`code` は `body_required` になります。

`code` の実際の粒度は ogen が提供する型情報に制約されます（`shared/problem/ogenproblem` の
特性テストが実測値を固定しています）。ogen v1.23.0 では次の通りです。

- `minItems`（配列長）と `minLength`（文字列長）は同じ `*validate.MinLengthError` になるため、
  どちらも `min_length` です。
- `enum` 違反と `uniqueItems` 違反は専用のエラー型にならず、**受信値を文言に含む**素の
  エラーになります。文言ごと捨てて汎用の `invalid` へ落とします（受信値を漏らさないため）。
  ogen が専用型を導入したら特性テストが落ち、語彙を細かくできると分かります。

### `code` は 2 系統の語彙

| 語彙 | いつ | 置き場所 |
| --- | --- | --- |
| 契約検証（`type: validation-error`） | ogen が契約違反を検出した（400） | `shared/problem/vocab.go` |
| ドメイン検証（`type: invalid-input`） | ドメインの規則に反した（422） | 各コンテキストの `internal/domain/<ctx>/errors.go` |

契約検証語彙（`required` / `type` / `min_length` / `max_length` / `pattern` /
`unique_items` / `invalid_param` / `body_required` / `invalid`）はどのコンテキストでも
意味が同じなので共有します。**ドメイン検証語彙は共有しません。** 同名の `invalid_quantity`
でも、注文コンテキストは「1 以上」、在庫コンテキストは「0 以上」を意味します。値域の違いは
`reason` の文言差として現れます。クライアントは `type` を見ればどちらの語彙かを判別できます。

**この 2 系統の語彙は、各契約（OpenAPI）の `InvalidParam.code` に `enum` として列挙して
あります**（契約検証語彙 ∪ そのコンテキストのドメイン検証語彙）。契約が機械可読な語彙台帳を
兼ねるので、契約を読むだけで取りうる `code` が分かります。ogen は `enum` から
`InvalidParamCode`（`string` の別名）を生成し、応答を復号するときに `Validate()` で enum
membership を検査します（`InvalidParam` を組み立てるインターフェース層の境界で、素の文字列を
この生成型へ写します — ドメイン／アプリ層は素の文字列のまま）。**新しいドメイン `code` を
足したら、対応する契約の `enum` にも足してください**（在庫は公開・内部の 2 契約が同じドメイン
語彙を載せるので両方）。実装（`Rule` / `domainReasons`）と契約の `enum` が乖離しないよう、
`problem_test.go` の網羅テスト `TestProblem_InvalidParamCodeEnumCoversVocabulary` が全語彙を
`enum` と突き合わせ、`readProblem` は実応答を生成型 `Validate()` に通します。この結合を壊すと
CI が落ちます（規則 R-19）。

対して `type` は `code` と違い OpenAPI の `enum` にしていません（素の `format: uri` のまま）。
ogen v1.23.0 は `enum` + `format: uri` の組み合わせで壊れた Go（`url.URL` を基底にした型に
文字列定数を代入し、JSON 生成も失敗）を出すためです。したがって `type` の機械可読な台帳は
OpenAPI の `enum` ではなく `shared/problem/type_uri.go`（サフィックス台帳）であり、
`problemTypeBase` を書き換えても契約側の更新は不要です。

### 検証規則は `Rule` に 1 つだけ書く

ドメインの検証規則は **`Rule` 型 1 つ**にまとめます。フィールド名・`code`・番兵が
バラバラの定数リストに分かれていると、ほぼ 1 対 1 の語彙を 3 つ並行して保守することになり、
規則を 1 つ足すだけで 4 箇所を編集する羽目になります。

```go
// internal/domain/errors.go
var (
    VQuantity      = Rule{Field: "quantity", Code: "invalid_quantity",       Err: ErrInvalidQuantity}
    VMoneyAmount   = Rule{Field: "amount",   Code: "invalid_money_amount",   Err: ErrInvalidMoney}
    VMoneyCurrency = Rule{Field: "currency", Code: "invalid_money_currency", Err: ErrInvalidMoney}
)
```

呼び出し側は 1 行です。番兵の文言は自動で後ろに連結されるので繰り返しません。

```go
func NewQuantity(n int) (Quantity, error) {
    if n < 1 {
        return Quantity{}, VQuantity.Violated("注文行の数量は 1 以上でなければなりません（指定値: %d）", n)
    }
    return Quantity{value: n}, nil
}
```

**番兵は残します。** `errors.Is` の判定単位であり、既存の公開 API だからです。`Rule` は
それを指すだけで置き換えません。`Rule` は番兵より細かくてよく、`ErrInvalidMoney` が
`VMoneyAmount` と `VMoneyCurrency` の 2 つに分かれるのがその実例です（規則 R-6）。

**新しい規則を足すコストは 3 箇所の編集です。** `Rule` を 1 行、インターフェース層の
`domainReasons` を 1 行、そして**その `code` を契約（OpenAPI）の `InvalidParam.code` の
`enum` に 1 行**（在庫は公開・内部の両契約）。`code` は契約で `enum` 化されており、足し忘れると
生成型 `InvalidParamCode.Validate()` が弾いて CI が落ちます。再生成（`go generate`）も忘れずに。
それ以上は要りません（規則 R-19）。

### フィールド識別情報は 3 層で組み立てる

```
[domain]                    [application]                [interfaces]
「数量は 1 以上」            「入力 DTO のどの位置か」      「JSON のどの名前か」

FieldViolation{             ValidationError{             InvalidParam{
  Rule:  VQuantity     →      Path: "Lines[0].Quantity"    Name: "lines[0].quantity"
  Index: nil }                Code: "invalid_quantity" }   Code: "invalid_quantity"
                                                           Reason: "1 以上の値を..." }
```

各層は**自分が知っていることだけ**を足します。

- **ドメイン**（`Rule` / `FieldViolation`）— 自分の語彙でフィールドを名乗るだけ。HTTP の
  フィールドパスは知りません。番兵エラーを包み `Unwrap` するので、`errors.Is` は従来どおり
  機能します。
- **アプリケーション**（`ValidationError` / `locate`）— 入力 DTO 上の位置（添字を含む）を前置します。
  ドメインの違反でなければ**元のエラーをそのまま透過**させます（リポジトリの失敗や版衝突が
  「入力検証エラー」に化けてはいけません）。
- **インターフェース**（`toJSONPath` / `domainParams`）— DTO の識別子を JSON 名へ翻訳します。
  Go の識別子を応答へ露出させてはいけません。

層をまたぐ 2 つの表（アプリ層の `dtoPaths`、インターフェース層の `jsonNames`）は
**上書き表**です。既定は機械的な変換（大文字化 / 小文字化）で足り、そこに書くのは
それでは正しくならないものだけです（例: 入力 DTO は `UnitPriceAmount` と平らなのに
API は `unitPrice.amount` と入れ子、注文 ID のパスパラメータ名は `id`）。したがって
**規則を 1 つ足しても通常この 2 つの表は触りません**。

### コレクションの位置は `ViolatedAt` が運ぶ

集約やドメインサービスが**自分でコレクションを走査している**場合、何番目で失敗したかを
知っているのはそのループだけです（アプリケーション層の走査は別物で、そこには位置が
残りません）。在庫の `ReservationService.Allocate` がその実例です。

```go
for i, l := range lines {
    if l.Quantity.IsZero() {
        return VQuantity.ViolatedAt(i, "SKU %q の予約数量は 1 以上でなければなりません", l.SKU.String())
    }
}
```

「渡された何番目か」はドメイン自身の知識であり、HTTP のパス（`lines[1].quantity`）では
ないので純粋ドメインの原則に抵触しません。位置を `Lines[i]` というパスへ組み立てるのは
アプリケーション層です。この機構は**両コンテキストで同一**であり、片方だけが余分な
フィールドを持つといった非対称はありません。

### 単一フィールドに帰着しない違反

集約レベルの規則でも、単一の入力フィールドに帰着しないものは `FieldViolation` にしません。
素の番兵のまま返し、`invalid-params` を省略します。

- 通貨をまたぐ加算（`Money.Add`）— 2 つの明細行の矛盾であり、どちらが「悪い」とは言えない
- 状態の矛盾（`ErrOrderNotConfirmed`）や在庫不足（`ErrInsufficientStock`）— 409 であり、
  「入力が悪い」のではなく「今その操作ができない」。直すべきフィールドが存在しない

### 3 サーバの一貫性

`ProblemDetails` スキーマは 3 契約に重複定義されたままです（各コンテキストを単体で切り出せる
独立性を優先）。ドリフトは `cmd/dev/problem_parity_test.go` が検出します。同じ種類の契約違反を
3 サーバへ送り、応答の形（`Content-Type` / `type` / `title` / `detail` / キー集合）が一致する
ことをテーブル駆動で確認します。契約 YAML の同値比較ではなく**振る舞いの一致**を見るのは、
契約が同一でも実装がずれれば意味が無いからです。

### 将来の拡張点（現在はスコープ外）

- **401 / 403**（`ogenerrors.SecurityError`）— 契約にセキュリティスキームが無いため未対応。
  追加するときは `errorHandler` の `switch` に `http.StatusUnauthorized` /
  `http.StatusForbidden` の分岐と、対応する `type` サフィックスを足します。
- **429 / `Retry-After`** — レート制限を導入するときに `type` を足します。
- **多言語化** — `title` / `reason` / `detail` は現在すべて日本語固定です。`Accept-Language` に
  応じて切り替えるなら、`shared/problem` の表と各コンテキストの `domainReasons` を
  言語別に引けるようにします（`code` と `type` は言語に依存しないので変えません）。
- **契約への値域制約の追加** — 現在は検証をドメインに一元化しているため、契約側には
  `minimum` / `minItems` などを書いていません。書けば ogen の `Validate()` 経路が発火し、
  `invalid-params` に**添字付きの**パスが出るようになります（この経路は
  `shared/problem/ogenproblem` のフィクスチャ契約で既にテスト済みです）。
- **依存の自動更新（dependabot 等）** — 未導入。ogen の版を人手で上げても
  `shared/problem/ogenproblem/extract_test.go` の特性テストが CI で落ちるため、
  安全網としては機能します。

## context.Context

- IO を伴うメソッドは **`ctx context.Context` を第 1 引数**に取ります。
- **ドメイン層は `context.Context` を受け取りません**（純粋に保つため）。
- `context.Context` に載せてよいのは**リクエストスコープの付帯情報**（相関 ID など）
  だけです。**トランザクションハンドルのような制御関心を context に隠して運びません**。

## 層の分離（ヘキサゴナル / DDD）

コードは 4 層に分けます。アダプタは方向で対称に **adapter/inbound（入口＝駆動側）** と
**adapter/outbound（出口＝被駆動側）** に分けます。ポートは application 層に置きます。

- **純粋なドメイン**: ドメイン層はリポジトリ（そのポート interface も含む）や
  永続化・IO・フレームワーク・アダプタを import しません。この純粋性は静的解析（depguard）
  で機械的に強制しています。
- **ポートはアプリケーション層の `ports.go` に定義**し、**アダプタは adapter/outbound 層で実装**
  します。リポジトリのポートも、翻訳（腐敗防止）のポートも、時刻のような IO のポートも、
  例外なく application 層の `ports.go` に置きます（B-5 の `ports.go` 行 — 「それだけが入る／
  それは全部そこにある」の双方向の約束。検査 10 / 検査 11 が機械強制します）。
  application 層はアダプタに依存しません（依存はポート経由で逆転させます）。
- **入口（inbound）と出口（outbound）は互いを直接 import しません**。両者の結線は
  合成ルート（ファサード / cmd）だけで行います。方向ルールは depguard で強制しています。
- ユースケースは **「読み込み → ドメイン操作 → 保存」** を作業単位の内側で行います。
  ドメインサービスはユースケースから受け取ったデータで動き、自分でリポジトリを
  参照しません。
- **業務ルールはドメイン層**（オーケストレーションはアプリケーション層）に置きます。
  adapter（inbound / outbound）層に業務ロジックを置きません。

## 値オブジェクト

- 値オブジェクト（`SKU`, `Quantity` など）は**境界づけられたコンテキストごとに独立して
  所有**します。他コンテキストと安易に共有しません。共有は「意図的で正当化された例外」で
  あって、既定ではありません。
- `shared` モジュールにはドメイン非依存の建材（ID 生成、相関 ID、作業単位など）だけを
  置き、ドメインの値オブジェクトは置きません。

## `shared` モジュール（共有インフラ）の規約

`shared` は**ドメインに依存しない技術的建材**だけを収める共有モジュールです。どのコンテキスト
からでも安全に共有できる「配管」を集約し、コンテキスト間の純インフラ重複を防ぎます。

- **置いてよいもの / いけないもの**: 置いてよいのは Go 標準ライブラリと `shared/` 内の他パッケージ
  だけに依存する、ドメイン非依存の建材です（ID 生成 `id`、相関 ID `correlation`、作業単位 `uow`、
  構造化ログ `logging`、背景ワーカー `worker`、アウトボックス機構 `outbox`、プロセス内イベント
  配信 `event`、HTTP サーバランナー `serve`）。**置いてはいけないもの**: ドメイン値オブジェクト
  （`SKU` / `Quantity` / `Money` など。コンテキストごとに独立所有する）、DB ドライバ・HTTP
  フレームワーク・特定コンテキストの型への依存。
- **判断手順**（新しく何かを `shared/` へ置きたくなったとき、上から順に見ます）:
    1. その型・関数はドメイン語彙（`SKU` / `Quantity` / `Money` / `Order` / `StockItem`）を
       **名前にも構造にも**含まないか → 含むなら置きません。
    2. 型パラメータで受けるだけで済み、コンテキストの package を import せずに書けるか →
       import が必要なら置きません。
    3. `shared/go.mod` に新しい `require` を足さずに書けるか → 足すなら置きません。
    4. 呼び出し元が 2 つ以上あるか → 1 つなら置きません（先回りの共通化をしない。
       `shared/worker` が ticker ループを持たない理由がこれです）。
- **一方向依存は lint で強制**します。depguard の `shared-purity` rule が `shared/` 配下から
  `contexts/`・`clients/` への import を禁じ、依存が常に「コンテキスト → `shared`」の一方向で
  あることを機械的に検証します（`_test.go` も対象。規約コメントではなく実行される規則です）。
  逆方向（ドメイン層が外側を知らないこと）は `domain-purity` rule が塞ぎます。
- **ポートと実装の分離**: 依存を増やしうる実装は、ポートパッケージ本体ではなく**サブパッケージ**
  へ隔離します。ポート本体（`shared/outbox`・`shared/correlation`・`shared/uow`）は現在の依存を
  増やさず純粋に保ち、`net/http` や DB ドライバを持ち込みません。
    - `shared/uow`（純粋な契約 + 再試行）↔ `shared/uow/pgxuow`（pgx 実装）
    - `shared/outbox`（ポート + Runner）↔ `shared/outbox/memory`（インメモリ backing store
      `Stores`）/ `shared/outbox/logpub`（no-op Publisher）
    - `shared/correlation`（ctx キー + traceparent コーデック。純粋な文字列処理）↔
      `shared/correlation/corrhttp`（`net/http` ミドルウェア）
- **コンテキストの単独切り出し**: 各コンテキストモジュールは `shared` に `replace`（相対パス）で
  依存します。コンテキストをワークスペースから単独モジュールとして切り出すときは、**`shared` も
  併せて持ち出し**、`replace` 先を維持してください（`shared` を残置するとビルドできません）。
  共通化を進めるほど、この持ち出しに同伴する `shared/` の面積は増えます。これは**設計上の後退
  ではなく明示的なトレードオフ**です — 重複を各コンテキストに残せば同伴面積は減りますが、
  「間違えやすい機構が 1 箇所にある」という利点を失います。切り出し時は `shared/` をまるごと
  持ち出す（未使用パッケージが混じっても Go のビルドには無害）のが最も手数の少ない方法です。

### プロセス内イベント配信（`shared/event`）

- `event.go` が**型なしのコア**（`Event` / `Handler` / `Dispatcher` / `InProcess`）、`typed.go` が
  各コンテキストのドメインイベント型で使う**型付きファサード**（`Occurred` / `Sink[E]` /
  `TypedHandler[E]` / `Typed[E]`）です。合成ルートは `event.NewTyped[domain.DomainEvent](log)` の
  ように型引数を綴って直接生成します。
- **ドメイン層はこのパッケージを import しません**。各コンテキストは自分の `DomainEvent` を
  独自に定義し、それが `EventName() string` と `OccurredAt() time.Time` を持つことで
  `event.Occurred` を**構造的に**満たします。`shared/event` 側もコンテキストを import しません。
  双方向に import が無いまま型が噛み合う — これが Go の構造的型付けを活かした境界です。
- `Dispatch` は**エラーを返しません**。永続化成功後の後処理であり、コミット済みトランザクションを
  巻き戻せないためです（ハンドラのエラーは警告ログに残します）。各コンテキストの
  `EventDispatcher` ポートもこの契約で宣言します。
- **ポートはコンテキストのもの、実装は共有機構**です。`EventDispatcher` ポートは各コンテキストの
  `contexts/*/internal/application/ports.go`（他の outbound ポートと並んで）に interface として
  宣言され、実装は `*event.Typed[E]` が構造的に満たすのでアダプタは不要です。per-context の
  委譲コンストラクタは置きません。「機構は共有・型はコンテキスト固有」という境界の引き方が
  呼び出し側から見えている状態を保つためです。

### HTTP サーバランナー（`shared/serve`）

- `serve.Run(ctx, log, servers ...Server)` が 1 個以上の HTTP サーバを起動し、`ctx` の完了か
  いずれかのサーバのエラーまで待ち、**全サーバ**をグレースフルシャットダウンします。
  本数に依存しません（`ordering` は公開 1 本、`inventory` は公開 + 内部の 2 本）。
- 抽出の原則は「**間違えやすい部分を共有し、自明な部分は残す**」です。共有するのは goroutine の
  ファンアウト、`http.ErrServerClosed` の除外、エラーチャネルの容量、停止経路の合流、猶予つき
  `context` の採り方。既定値（`DefaultReadHeaderTimeout` 10s = Slowloris 対策、
  `DefaultShutdownTimeout` 15s）もここ 1 箇所に集めます。
- **ランナーが意図的に持たないもの**（いずれも `main.go` に残します）:
    - **シグナル受信** — `pgxpool.New(ctx, ...)` と `mod.StartWorkers(ctx)` がランナー起動より
      **前**に同じ cancellation を必要とします。どのシグナル（`SIGINT` / `SIGTERM`）を扱うかが
      合成ルートに見えていること自体、コンテナ実行環境との契約として読者に伝わるべき情報です。
    - **資源の解放** — `defer pool.Close()` は取得の隣に残します。ランナー到達前に早期 return
      する経路があるため、解放をランナーへ移すとその経路で漏れます。
    - **ヘルスチェック** — 運用契約なので各サービスの mux に載せます。採用者はすぐ DB 疎通や
      liveness/readiness の分離を求めるため、ランナーが持つとその時点で捨てられます。
- 依存は**標準ライブラリのみ**です。特定の DB ドライバやフレームワークを知りません。

### 共通化しない重複（生成境界の意図的な重複）

`shared/` へ寄せる判断の物差しは**重複行数ではなく「抽出後にテンプレートが読みやすくなるか」**
です。差の**量**ではなく**性質**で決めます。

| 差の性質 | 判断 | 例 |
|---|---|---|
| 差が**型だけ**で、generics が型境界を越えられる | 抽出する | `application/ports.go` の `EventDispatcher`（→ `shared/event`） |
| 差が**配線だけ**で、機構と配線を分離できる | 機構だけ抽出する | `cmd/*/main.go`（→ `shared/serve`） |
| 差が**生成された型の名前**で、橋渡しに抽象層かスキーマ再編が要る | **抽出しない** | `problem.go`・`postgres/outbox.go` |
| 共有するとコンテキスト間に**ドメイン結合**が生まれる | **抽出しない**（構造上の禁止） | `errmap.go` |

- **生成型を包むアウターリングのアダプタコードは、per-context に重複してよい**とします。
  `problem.go`（RFC 9457 の応答組み立て。機構 約 200 行が 3 コピーでほぼ共通）と
  `postgres/outbox.go`（2 コピーで**論理差 0** — 差は `sqlcgen` の import パスだけ）が該当します。
  どちらも per-context のコード生成（ogen は契約ごと、sqlc はスキーマごと）が「**構造は同一・
  名前だけ別パッケージ**」の型を吐くことが原因で、共有するには型境界の橋渡しが必要です。
  橋渡しの手段は generic / interface / 契約とスキーマの再編のいずれかで、**抽象層が増えるか、
  「1 コンテキスト 1 スキーマ・1 契約」という読み下しやすい像が崩れます**。直読性を優先し、
  この重複は意図的に残します。共通化できる**純インフラ部分**（語彙・型 URI・`invalid-params` の
  組み立て、アウトボックスの `Message` / `Runner`）は既に `shared/problem`・`shared/outbox` へ
  出し切っており、残っているのは生成型に触る薄い層だけです。
- **`errmap.go`（ドメイン番兵 → HTTP ステータス → 問題種別の写像）は共有しません。** これは
  重複の性質ではなく**構造上の禁止**です。写像表は各コンテキストのドメイン番兵エラーを名指しする
  ため、共有すると `shared/` がドメイン語彙を持ち、コンテキスト間にドメイン結合が生まれます
  （値オブジェクトを共有しないのと同じ理由）。前記の判断手順 1 で落ちます。

### `shared/` の粒度（監査結論）

- 現行の分割は**過粗・過細なし**と判断しています。極小パッケージ（`id` / `worker` / `logging`）も
  「1 概念 1 パッケージ」としてテンプレートの教材性に寄与するため、統合しません。
- 唯一の欠けは「`shared/event` が型なしコアしか提供せず、両コンテキストが必要とする型付き
  dispatcher を提供していない」点で、これは `typed.go` で解消しました。
- `shared/worker` に ticker ループを取り込むか、という論点は当該パッケージの doc コメントで
  決着しています（呼び出し元が 1 つしかないループを先回りして共通化しない）。
- `cmd/dev`（インプロセス統合ハーネス）は HTTP サーバ・シグナル受信・graceful shutdown を
  いずれも持たない一方向のシナリオランナーです。したがって `shared/serve` の適用対象では
  ありません（走らせる HTTP サーバが無い）。ライフサイクル機構が二重化する懸念は、そもそも
  競合が存在しないため自明に満たされます。

## モジュール境界

- 各コンテキストは 1 つの Go モジュールとして独立します。
- コンテキストは 4 層を `internal/` 配下に隠し、**薄い公開ファサード**
  （ルートパッケージ: `New(Deps) (*Module, error)`, `HTTPHandler()` など）だけを公開します。
- 他コンテキストや合成ルートは、この公開ファサード（および公開 `port` パッケージ）だけに
  依存し、他モジュールの `internal/` には触れません（Go のコンパイラが強制します）。
- コンテキスト間で受け渡すのは**翻訳済みの公開型**（`port` の DTO、`contracts/events/` の
  メッセージ契約、生成クライアントの wire 型）だけです。内部のドメイン値オブジェクトは
  渡しません。相手コンテキストの番兵エラーも自コンテキストの番兵へ翻訳します（`port` に置いた
  `ErrReservationRejected` など）。

## 作業単位（UnitOfWork）とトランザクション境界

- **トランザクション境界は明示的に所有**します。トランザクションハンドルを
  `context.Context` に隠して引き回しません。書き込みは必ず `UnitOfWork.Within` の内側で、
  コールバック引数のリポジトリ束から取得したリポジトリを使って行います。
- 作業単位は Go のジェネリクスで **`uow.UnitOfWork[R]`** として型付けし、各コンテキストは
  自分のリポジトリ束 `R`（例: `Repos { Stock() ...; Outbox() ... }`）で特殊化します
  （`type UnitOfWork = uow.UnitOfWork[Repos]`）。ユースケースはこの束からしかリポジトリを
  取得できないため、「トランザクション外の書き込み」が構造的に起こり得ません。
- 楽観的排他制御の衝突（`uow.ErrConcurrencyConflict`）は `uow.Run` が指数バックオフで
  再試行します。集約はバージョンを**保持するだけ**で、比較（compare-and-set）はリポジトリが
  担います。ユースケースは「読み込み → ドメイン操作 → 保存」をクロージャ内で完結させ、
  再試行時に最新状態を読み直せるようにします。
- クロスコンテキスト送信は**同一トランザクション**でアウトボックスへ積みます
  （`repos.Outbox().Enqueue(...)`）。集約の保存とメッセージ Enqueue を原子的にコミットして
  二重書き込みを避けます。一方、在庫予約の同期 ACL 呼び出しは**トランザクションの外**で
  行います（HTTP 呼び出しが DB トランザクションを跨いで保持されるのを避けるため）。
- **`outbox` と `events` は役割を分けます**。`outbox` は**一時的な配送キュー**で、
  送信中継が送出に成功した行は**削除**します（delete-after-publish）。したがって
  `outbox` に存在する行は常に「まだ送っていないもの」だけです。何を発行したかの
  **恒久的な記録**は `events` テーブル（追記専用のイベントログ）が担い、`Enqueue` の実装が
  outbox 行と events 行を**同一トランザクションで両方**書きます。ユースケース側の呼び出しは
  `repos.Outbox().Enqueue(...)` のままで、片方だけ残ることは構造的に起こりません。
  配送は `events` を読みません（`Runner` が見るのは `outbox` だけ）。
- **`events` は無制限に増え続けます**。このテンプレートは単純さを優先して保持ジョブを
  持たないため、本番採用時はアーカイブ・パーティション・保持（リテンション）ジョブの
  いずれかを足してください。`outbox` 側は送信後に消えるので肥大化しません。
- 作業単位の**ドライバ実装は `shared/uow/<driver>uow` サブパッケージ**に置きます
  （dir=package。現行は pgx 版の `pgxuow`、将来 database/sql 版を足すなら `sqluow`）。
  Begin/Commit/Rollback といったドライバ固有のトランザクション・ライフサイクルはこの 1 箇所へ
  集約し、各コンテキストの outbound アダプタ（`postgres`）だけが import します
  （`NewUnitOfWork` は buildRepos クロージャを供給する薄い factory に縮小します）。
  一方、純粋な `shared/uow` パッケージは契約（`UnitOfWork[R]` インターフェース）と
  再試行機構（`Run`）だけを持ち **driver 非依存**に保つため、application 層は `shared/uow` を
  import してもドライバ（pgx）を直接にも推移的にも引き込みません。パッケージ名をドライバ名
  そのもの（`pgx`）にしないのは、`pgx.Tx` を使う import 側と識別子が衝突するのを避けるためで、
  `<driver>uow` と名付けます。

## SQL と宣言的 DB

- 各コンテキストは自分の `db/` を所有します。**1 つの物理 DB を schema-per-context で論理
  分割**し、他コンテキストのスキーマを直接読み書きしません。
- `db/schema.sql` は「あるべき最終状態」を宣言する DDL で、**psqldef（sqldef）で incremental・
  非破壊に適用**します（drop-and-recreate をしない。破壊的差分は `--dry-run` でプレビュー）。
  `target_tables` で当該スキーマに限定し、psqldef が他コンテキストのオブジェクトに触れない
  ようにします（`db/sqldef.yml`）。
- **psqldef の DDL パーサ制約**: 列 CHECK 制約に `IN (...)` は使えません（パースエラーになる）。
  列挙は `CHECK (status = 'a' OR status = 'b')` で書きます。これは PostgreSQL が保持する形と
  一致するため psqldef の適用も冪等になります（`IN (...)` は内部的に `= ANY(ARRAY[...])` へ
  正規化される）。
- `db/queries.sql` は sqlc の入力で、そこから型安全な Go を生成します。**生成物はコミットし、
  手で編集しません**。
- 権限は宣言的な**冪等ロール/GRANT SQL**（`db/roles.sql`）で与え、各サービスが**自スキーマ
  だけにスコープしたロール**で接続します（実行時 superuser なし・no-cross-schema-reads）。
- **本番参照データ**（`db/seed.sql`、冪等 upsert）と **dev/test フィクスチャ**
  （`db/fixtures.sql`）は別経路にし、fixtures を本番の適用経路に混ぜません。
- 適用順は **schema → roles/GRANT → seed →（dev のみ）fixtures**（GRANT/seed はテーブルの
  存在を前提とするため schema の後）。この順序は bring-up の init コンテナが担います。

## テスト

> **テストの名前・日本語の書き方・コメントの規約は
> [docs/testing-conventions.md](docs/testing-conventions.md) にあります**（テスト関数名の 2 形、
> `t.Run` の 8 語語彙、`t.Parallel()` の適用範囲、日本語コメントの書き方）。
> この節は**道具立て**だけを扱います。

- テストランナーは標準の `testing`。アサーションは **testify**（`require` は前提が崩れたら
  即中断する致命的検証、`assert` は独立した検証を続行）で書きます。
- **ポート相互作用の検証には uber-go/mock（gomock）** を使います。application 層のポート
  （interface）から `go generate`（`go tool mockgen`）でモックを生成し、`internal/mock`
  パッケージに**コミット**します（手編集しない・再生成で冪等）。「use case がポートを正しい
  順序・回数で呼ぶか」を `EXPECT()` で表明する用途です。カバレッジ計測（domain + application）
  を汚さないよう、生成モックは計測グロブの外（`internal/mock`）へ置きます。
- **インメモリ実装（`adapter/outbound/memory`）はモックではなく本物のアダプタ**です。擬似
  トランザクションや楽観的排他制御まで含めた統合的な振る舞いを、DB 非依存で高速に検証する
  ために使います（gomock とは役割が別で、置き換えではなく併用）。
- コンテキストを跨ぐ **seam（腐敗防止層）は `httptest`** でピア契約どおりのスタブを立てて
  検証します。
- **PostgreSQL アダプタの統合テスト**は build tag `integration` を付けたときだけ実行します
  （ローカル DB / docker-compose 前提）。
- ドメイン層とアプリケーション層は**行カバレッジ 80% 以上**を維持します
  （`scripts/coverage-gate.sh`、CI のマージ前ゲート）。生成コード（ogen / sqlc / mockgen）と
  アダプタ結線はこの閾値の対象にしません。
- **代数的法則と集約の不変条件には pgregory.net/rapid**（プロパティベーステスト）を使い、
  **受信アダプタの入口の全域性には Go ネイティブの fuzz**（`testing.F`）を使います。役割が
  違うので一方には寄せません。詳しくは
  [docs/testing-conventions.md](docs/testing-conventions.md) の H 群にあります。
- マージゲートに載るのは、既定回数・既定シードの `rapid.Check` と fuzz の seed corpus
  （`testdata/fuzz/` にコミット済み。通常のテスト実行で毎回走ります）だけです。**長時間の
  `-fuzz` は載せません** — 打ち切り時刻でしか止まらず結果が実行ごとに変わるためで、探索は
  任意実行の `make fuzz` で行います。
- テスト用の外部依存（testify / gomock / rapid）は**本番コードに持ち込みません**。テストファイルと
  `internal/mock` だけが import してよく、とりわけドメイン層の純粋性を保ちます。

## 整形・静的解析

- `gofmt` と `goimports` で整形し、`go vet` と `golangci-lint` を通します（CI ゲート）。
  層／seam の境界は `depguard` で機械的に強制します。rule は 7 本で、`domain-purity`（ドメインの
  純粋性）、`application-no-adapter`（依存性逆転）、`inbound-not-outbound` / `outbound-not-inbound`
  （アダプタの方向）、`ordering-no-inventory-context`（コンテキスト間 seam）、
  `dev-harness-public-only`（公開ファサードのみ）、`shared-purity`（`shared` → コンテキストの
  逆流禁止）です。
- depguard の rule を足したら、**一時的に違反する import を差し込んで実際に報告されることを
  確認**してください。「rule が在る + lint が green」だけでは、`pkg:` の綴り間違いや `files:`
  グロブの誤りで **rule が黙って何も検査していない**状態を検出できません（ツリーに違反が
  無ければ green は自動的に通ります）。
- 生成コード以外は原則コメント付きで、意図が読み取れるようにします。

### 機械強制の一覧（fail / warn の線）

`make lint`（golangci-lint）と `make conventions`（`scripts/convention-gate.sh`）の 2 つが
規約を強制します。両方とも `make ci` に含まれ、CI も同じターゲットを呼びます。

| # | 検査 | 実装 | 重大度 |
| --- | --- | --- | --- |
| 1 | 命名（下線・頭字語・レシーバ一貫性） | `revive`（var-naming / receiver-naming）**のみ** | **fail** |
| 2 | エラー文言（大文字始まり・句読点終わり） | `stylecheck` ST1005 | **fail** |
| 3 | doc コメントの句点 | `godot`（scope: declarations） | **fail** |
| 4 | 外部テストパッケージ | `testpackage`（既定 skip-regexp が `export_test.go` と `*_internal_test.go` を除外） | **fail** |
| 5 | ヘルパーの `t.Helper()` | `thelper` | **fail** |
| 5b | testify の落とし穴（引数順 `(want, got)` の取り違え、`Error` と `ErrorIs` の混同など） | `testifylint` | **fail** |
| 6 | `t.Parallel()`（domain・application のみ） | `paralleltest`（**パス限定**）+ `tparallel`（全体、誤用検出） | **fail** |
| 7 | 層・seam の境界 | `depguard`（7 rule） | **fail** |
| 8 | テスト関数名の主題の一意性（C-1b） | `scripts/convention-gate.sh` 検査 1 | **fail** |
| 9 | `t.Run` の 8 語語彙 | `scripts/convention-gate.sh` 検査 2 | **fail** |
| 10 | `t.Run` 名の `/` 不在 | `scripts/convention-gate.sh` 検査 3 | **fail** |
| 11 | テーブル駆動の `name` フィールドの 8 語語彙と `/` 不在（D-6） | `scripts/convention-gate.sh` 検査 2' / 3' | **fail** |
| 11b | `*_test.go` の複合リテラルを位置指定で書かない（D-6。#11 の**視界**を担保する） | `scripts/convention-gate.sh` 検査 8 | **fail** |
| 12 | カタログ的ファイル名の不在（B-4） | `scripts/convention-gate.sh` 検査 4 | **fail** |
| 13 | package 名 = ディレクトリ名（A-7） | `scripts/convention-gate.sh` 検査 5 | **fail** |
| 13b | ドメインパッケージの単一性（B-8。#13 と対で「層の名前で 1 つ」を成す） | `scripts/convention-gate.sh` 検査 9 | **fail** |
| 14 | 規約系 Markdown の半角スペース境界（F-3） | `scripts/convention-gate.sh` 検査 6 | **fail** |
| 15 | ファイル凝集（公開型 1 個のみを含む 40 行未満のファイルが同一パッケージに 3 つ以上） | `scripts/convention-gate.sh` 検査 7 | **warn** |
| 16 | CI 時間 | **計測して記録するだけ** | なし |
| 17 | ポート宣言の全数性（B-5 の `ports.go` (b)。application 層の非テストファイルに `ports.go` 以外の `interface` 宣言が無い） | `scripts/convention-gate.sh` 検査 10 | **fail** |
| 18 | `ports.go` の純度（B-5 の `ports.go` (a)。`ports.go` に `func` 宣言が無い） | `scripts/convention-gate.sh` 検査 11 | **fail** |
| 19 | 非ルートのドメイン型をポインタで漏らさない（B-9） | `scripts/convention-gate.sh` 検査 12 | **fail** |
| 20 | 集約ストア実装のファイル名（B-11） | `scripts/convention-gate.sh` 検査 14 | **fail** |

**ST1003 は追加していません** — `revive` の `var-naming` と重複して二重報告になるためです
（役割は 1 つの linter に寄せます）。

凝集（#15）を warn に留めるのは、経験則であって機械的に 0 にできる指標ではないからです
（標準ライブラリにも build tag 以外の 40 行未満が 456 件あります）。判断を人間に委ねます。

CI 時間（#16）に**閾値は置きません**。根拠のない閾値は CI マシンの性能変動で偽陽性になるためです。

### G-1 追加した rule は必ずカナリア検証する

違反を注入して当該 rule が報告することを確認し、**注入した違反がその linter に到達したことまで
確かめて**から revert します。別のエラー（import cycle・コンパイルエラー・先に走る linter）に
飲まれると**偽陰性**になり、rule が正しいのか不発なのか区別できません。

### G-2 warn 側も対で検証する

凝集違反を注入したとき「**warn として報告される（正）**」と「**終了コードは 0 のまま fail しない（負）**」を
**1 回の観測で同時に**満たすことを確認します。報告だけを見ると warn 規則が死んでいる場合と区別できず、
終了コードだけを見ると warn のつもりが fail になっている場合と区別できません。

同じ考え方を**除外ロジック**にも適用します。検査 6（F-3）は「本文の 1 件は報告され（正）、
コードフェンス内の同じ文字列は報告されない（負）」を 1 回の観測で満たすことで、
除外が広すぎ／狭すぎのどちらでもないことを示します。

## 意図的な逸脱の記録

規約が Go の一次資料と食い違う箇所は**隠さず、理由と緩和策を書きます**。書かないと、後から読んだ人
（や AI）が「Go の作法に反している」と判断して勝手に戻してしまいます。

### アサーション・ライブラリ（testify）を使う

**Go の指針**: Go Wiki TestComments は「Avoid the use of 'assert' libraries to help your tests.」と
明確に避けるよう勧めています。理由は (a) テストを早期に終了させ、何が正しかったかの情報を落とす
(b) Go 自体を使う代わりにミニ言語を作る。代替として `cmp.Diff` を推奨しています。

**このテンプレートの選択**: **testify を使います**（`require` / `assert`）。

**理由と緩和策**:

- Go の懸念 (a) には、**既存の使い分け規約がすでに答えています** — 「`require` は前提が崩れたら
  即中断する致命的検証、`assert` は独立した検証を続行」。つまり「早期終了で情報が落ちる」ケースを
  `require` に限定し、独立に検証できるものは `assert` で全件報告させる運用になっています
- Go の懸念 (b) には、**`testifylint` を CI の fail 対象に入れる**ことで、ミニ言語の落とし穴
  （引数順の取り違え、`Error` と `ErrorIs` の混同など）を機械的に塞ぎます
- `cmp.Diff` は採りません（既存の約 50 テストファイルの書き換えは別の作業。将来の候補として記録）
- **失敗メッセージの実質**（実測値と期待値の両方を示し、対象関数と入力を明示する）は Go の要求どおり
  満たします → [docs/testing-conventions.md](docs/testing-conventions.md) の C-4c

### 日本語と英数の間に半角スペースを入れる

**Go とは無関係ですが同じ構図です**: JTF 日本語標準スタイルガイド 3.1.1 は「全角文字と半角文字の
間にスペースを入れない」と定めており、textlint 系ツールは既定でそれを指摘します。
このテンプレートは**入れます**（既存 178 箇所 / 逆 0 箇所の実測に基づく）→ F-3。
`scripts/convention-gate.sh` の fail 対象にして、ツール既定による一括反転を止めます。

### テスト関数名に下線を使う

**逸脱ではありません**（一次資料に許可があります）。A-9b は下線を禁じますが、
Google Go Style Guide が「Function names may include underscores in test files
(e.g., `TestFunctionName_SubCase`)」と明記しており、標準ライブラリにも 151 件（uniq 144）の実例が
あります。**したがってテスト関数名の下線は例外規定ではなく、Go が認めた別の規則です**
（「例外」と書くと逸脱に見えてしまうため、この言い方を選んでいます）。

## 版管理（ツールのバージョン固定）

ツールの版は**ちょうど 1 箇所**に置き、それ以外へはハードコードしません（単一情報源）。用途で
固定先を 2 段に分けます。

- **コード生成ツール（ogen / sqlc / mockgen）は go.mod の `tool` ディレクティブ**で固定します。
  `//go:generate` は `go tool <tool>` で呼び、`go.sum` で完全再現されるため、開発者のローカル環境に
  依存せず同一の生成物になります（`go generate ./...` だけで足り、手動 install は不要）。
- **横断／Docker ツール（golangci-lint / oasdiff / govulncheck / goimports / psqldef）は
  `tools/versions.env`**（リポジトリ直下の `KEY=VALUE`）を単一情報源にします。CI・スクリプト・
  compose は `set -a && . ./tools/versions.env && set +a` で読み込み、Dockerfile は `ARG` で受けます
  （既定値は置かない = 未指定ならビルドを失敗させ、第 2 の版情報源を作らない）。
- CI・スクリプト・Dockerfile・compose・README・AGENTS・docs に**版番号を直接書きません**。
  `ogen@… / sqlc@… / golangci-lint@… / oasdiff@… / govulncheck@… / psqldef@…` のようなツール名アンカーの
  grep で、`tools/versions.env`・`go.mod`・`go.sum`・`*.baseline.*` 以外にヒットが無いことを保てます。
- Go 言語版は `go.work` と各 `go.mod` の `go` ディレクティブ（現行 `go 1.26.0`）を情報源にし、
  CI の `go-version` と各 Dockerfile のベースイメージ（`golang:1.26-alpine`）を一致させます。
