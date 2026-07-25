# 規約（CONVENTIONS）

このテンプレートで一貫して守る Go とドメイン駆動設計の規約をまとめます。
新しいコンテキストやユースケースを足すときは、この規約に沿ってください。

## パッケージ / ファイルの構成

- **1 ディレクトリ 1 パッケージ**。
- パッケージ名は**短く・小文字・単数形**。冗長な繰り返し（stutter）を避けます。
  例: `inventory.SKU` とし、`inventory.InventorySKU` とはしません。
- **1 ファイル 1 主要型**を基本とし、ファイル名は型名の snake_case にします。
  例: `StockItem` → `stock_item.go`、`SKU` → `sku.go`。
- **生成ファイルは隔離**します。ogen は `internal/adapter/inbound/openapi/` に、
  sqlc は `internal/adapter/outbound/postgres/sqlcgen/` に出力し、**手で編集しません**。

## 命名

- 型・エクスポートされる識別子は **PascalCase**、非公開は **camelCase**。
- **頭字語は全て大文字**にします。例: `OrderID`, `SKU`, `HTTP`, `URL`。
  `OrderId` や `Sku` とはしません（ただし外部の生成コードの命名はそのツールに従います）。
- コンストラクタは **`New<Type>(...) (Type, error)`** の形にします。
  検証を伴う値オブジェクトや集約は、不正値を弾いてから返します。

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
| E1 | リクエストのデコード／契約検証 | ogen `ErrorHandler` | `internal/adapter/inbound/http/problem.go` |
| E2 | ルーティング不一致（未定義パス） | ogen `NotFound` | 同上 |
| E3 | メソッド不許可 | ogen `MethodNotAllowed` | 同上 |
| E4 | ハンドラ戻り値のエラー | 生成された `NewError` | `internal/adapter/inbound/http/errmap.go` |

E1〜E3 は **`Handler.ServerOptions()` を `NewServer` に渡すことで注入**します。渡し忘れると
ogen の既定ハンドラが `{"error_message": "operation placeOrder: decode request: ..."}` を返し、
内部実装の詳細が外部の観測面に漏れます。本番の合成ルート（`ordering.go` / `inventory.go`）も
テストも、必ず同じ `ServerOptions()` 経由で組み立てます。

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

台帳の実体は `shared/problem/types.go` です。`title` は種別と **1 対 1** で対応させ、
`title` から `type` を逆引きできる状態を保ちます（404 が 2 つ、409 が 2 つあるので、
HTTP の理由句をそのまま使うと逆引きできなくなります）。

**利用者による差し替え手順**: URI の名前空間は各コンテキストの
`internal/adapter/inbound/http/problem.go`（内部 API は `internalhttp/problem.go`）にある
**`problemTypeBase` 定数 1 箇所**です。自分の名前空間へ書き換えてください。URI は識別子
であり、解決可能な文書ページを公開する必要はありません。

### `detail` に何を書いてよいか

`detail` は**経路ごとの定型文**です（`shared/problem/types.go` の `Detail*` 定数）。
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

### 検証規則は `Rule` に 1 つだけ書く

ドメインの検証規則は **`Rule` 型 1 つ**にまとめます。フィールド名・`code`・番兵が
バラバラの定数リストに分かれていると、ほぼ 1 対 1 の語彙を 3 つ並行して保守することになり、
規則を 1 つ足すだけで 4 箇所を編集する羽目になります。

```go
// internal/domain/order/errors.go
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

**新しい規則を足すコストは 2 箇所の編集です。** `Rule` を 1 行、インターフェース層の
`domainReasons` を 1 行。それ以上は要りません（規則 R-19）。

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
- **ポートはアプリケーション層に定義**し、**アダプタは adapter/outbound 層で実装**します。
  リポジトリのポートも、翻訳（腐敗防止）のポートも、application 層に置きます。
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
- テスト用の外部依存（testify / gomock）は**本番コードに持ち込みません**。テストファイルと
  `internal/mock` だけが import してよく、とりわけドメイン層の純粋性を保ちます。

## 整形・静的解析

- `gofmt` と `goimports` で整形し、`go vet` と `golangci-lint` を通します（CI ゲート）。
  層／seam の境界は `depguard` で機械的に強制します。
- 生成コード以外は原則コメント付きで、意図が読み取れるようにします。

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
