# 集約ルートを 1 つ足すレシピ

新しい**集約ルート**（整合性の境界を自分で持ち、自分のリポジトリを持つ型）を足す手順です。
ユースケースを既存の集約に足すだけなら [add-a-use-case.md](./add-a-use-case.md) を見てください。
違いは「整合性の境界を新しく引くかどうか」です。境界を引かないなら集約は増えません。

**実物は `Shipment`（出荷）です。** この文書に出てくる箇所はすべて `Shipment` の実装として
リポジトリに在るので、迷ったら実物を読んでください。

## 0. その前に —— 本当に新しい集約ルートか

次の 2 つを両方満たすときだけ、集約ルートを足します。

- **整合性の境界を自分で持つ**。トランザクション 1 回で守るべき不変条件が、既存のどの集約
  とも別である
- **ライフサイクルが別である**。既存の集約と一緒に生まれて一緒に消えるなら、それは子
  エンティティ（`OrderLine` や `Reservation` の側）です

`Shipment` はこの両方を満たします。出荷は注文とは別の時点で生まれ、別の版で更新され、
「出荷が発送済みか」は注文の不変条件ではありません。

## 1. 集約間は識別子で参照する（この規則がレシピ全体を貫きます）

新しい集約ルートが既存の集約を参照するときは、**実体ではなく識別子**を持ちます。
`Shipment` は `*Order` ではなく `OrderID` を持ちます。**検査 14 が機械強制します**
（規約は [../CONVENTIONS.md](../CONVENTIONS.md) の R-2）。

この 1 つの決定が、下の層すべてに帰結を持ちます。

| 層 | 帰結 |
| --- | --- |
| ドメイン | `Shipment.orderID` は `OrderID`。イベント `ShipmentDispatched` も識別子を運ぶ |
| ユースケース | 注文はトランザクションの**外**で読む（§ 3） |
| スキーマ | `order_id` に**外部キーを張らない**（§ 5） |
| API | `/orders/{id}/shipments` ではなく `/shipments`。注文は本文の `orderId` で指す |
| 読み取り DTO | `ShipmentView` は `OrderID` を持つが、注文の明細は持たない |

## 2. 触る箇所の全数

**この表がこの文書の中心です。** 1 つでも落とすと、コンパイルが通るのに動かない箇所が残ります。

| 層 | 触る箇所 | `Shipment` での実物 |
| --- | --- | --- |
| ドメイン | 集約ルート・値オブジェクト・状態・イベント・番兵・`Rule` | [../contexts/ordering/internal/domain/shipment.go](../contexts/ordering/internal/domain/shipment.go) / [event.go](../contexts/ordering/internal/domain/event.go) / [errors.go](../contexts/ordering/internal/domain/errors.go) |
| アプリケーション | ポート宣言（`ports.go` **のみ**）、`Repos` へのアクセサ追加、ユースケース | [ports.go](../contexts/ordering/internal/application/ports.go) に `ShipmentStore` と `Shipments()`、[prepare_shipment.go](../contexts/ordering/internal/application/prepare_shipment.go) / [mark_shipped.go](../contexts/ordering/internal/application/mark_shipped.go) / [get_shipment.go](../contexts/ordering/internal/application/get_shipment.go) |
| **`Repos` の実装（2 箇所）** | `memory` と `postgres` の `repos` struct とアクセサ | [memory/uow.go](../contexts/ordering/internal/adapter/outbound/memory/uow.go) / [postgres/uow.go](../contexts/ordering/internal/adapter/outbound/postgres/uow.go) |
| 送信アダプタ | backing store・ストア実装・行変換 | [memory/shipment_rows.go](../contexts/ordering/internal/adapter/outbound/memory/shipment_rows.go) / [memory/shipment_store.go](../contexts/ordering/internal/adapter/outbound/memory/shipment_store.go) / [postgres/shipment_store.go](../contexts/ordering/internal/adapter/outbound/postgres/shipment_store.go) |
| **モック** | `mockgen` の引数にポートを追加して再生成 | [internal/mock/generate.go](../contexts/ordering/internal/mock/generate.go) に `ShipmentStore` を追加 |
| 受信アダプタ | ogen の `Handler` 実装に**操作の数だけ**メソッドが増える | [httpapi/handler.go](../contexts/ordering/internal/adapter/inbound/httpapi/handler.go) に 3 メソッド |
| 契約 | OpenAPI の `paths` / `schemas` / `InvalidParam.code` の `enum` | [../contracts/ordering/openapi.yaml](../contracts/ordering/openapi.yaml) |
| スキーマ | `schema.sql` / `queries.sql` / `roles.sql` / `fixtures.sql` / `seed.sql` | [../contexts/ordering/db/](../contexts/ordering/db/)（§ 5） |
| **合成ルート（1 箇所）** | 公開ファサードの `New` / `NewInMemory` / `build` | [../contexts/ordering/ordering.go](../contexts/ordering/ordering.go) **のみ** |
| 問題種別 | § 4 の 4 系統 | — |

**落としやすい 2 つ**:

- **`Repos` を実装する型は 2 つあります**（`memory` と `postgres`）。ポートを 1 つ足すと
  `application.Repos` を満たす型がすべてコンパイルエラーになるので、**黙っては壊れません**
- 一方 **`mockgen` の引数追加を忘れるとモックが生成されず**、テストを書く段階まで気づけません

**`cmd/*` は無変更です。** `contexts/ordering/cmd/ordering/main.go` は `ordering.New(ordering.Deps{…})` を、
`cmd/dev/harness.go` は `ordering.NewInMemory(…)` を呼びます。どちらも**永続化アダプタ
（`memory` / `postgres`）を直接は import しません** —— 集約が増えても結線が変わらないのは
そのためです。これが公開ファサードの価値です（`main.go` が `internal/adapter/outbound` から
import するのは、`Deps` へ注入する ACL とイベント送信のトランスポートだけです）。

生成物の再生成は `make generate`、冪等性の検証は `make generate-check` です。

## 3. ユースケース —— 1 トランザクションで書き込む集約ルートは 1 つ

`PrepareShipment` は「注文が `confirmed` であること」を事前条件として確かめますが、
**注文をトランザクションの外で読み、内では出荷だけを書きます**。

```
1. 入力検証            orderID を domain.NewOrderID で包む
2. [tx の外] 注文を読む  readOrderStore.Load(orderID)
                        - 見つからない        -> domain.ErrOrderNotFound            (404)
                        - confirmed でない    -> ErrOrderNotConfirmedForShipment    (409)
3. 出荷 ID を採番        shared/id で採番し domain.NewShipmentID で包む
4. [tx の内] 出荷を作る  uow.Run: repos.Shipments().Save(shipment)   <- 書くのは Shipment だけ
```

「注文が確定済みか」は**出荷の不変条件ではなく事前条件**です。不変条件なら同一トランザクション
で読んで固定する必要がありますが、事前条件なら検査時点の値で足ります。

> ### 集約を分けるとは、この競合を受け入れることです
>
> 注文を読んだ後・出荷を保存する前に注文が取り消されると、**取消済みの注文に対する出荷が
> 生まれえます**。これは設計上受け入れます（結果整合）。
>
> 打ち消しが業務的に必要なら `OrderCancelled` を購読して出荷を取り消す設計になりますが、
> それは v1 のスコープ外です。**2 つの集約を同一トランザクションに巻き込んで競合を消す**のは
> 選択肢ではありません —— それは境界を引かなかったのと同じで、集約を分けた意味が消えます。

状態遷移（`MarkShipped`）は通常どおりトランザクションの内側で「読み込み → ドメイン操作 →
保存」を完結させ、コミット後にイベントを配信します。

## 4. 問題種別（problem details）の配線 —— 4 系統

新しい集約は新しいエラーを持ち込むので、RFC 9457 の応答まで結線が要ります。規約は
[../CONVENTIONS.md](../CONVENTIONS.md) の「HTTP エラー応答」節です。

| 系統 | 箇所 | `Shipment` での内容 |
| --- | --- | --- |
| **新しい `domain.Rule`** | **3 箇所** | ① [domain/errors.go](../contexts/ordering/internal/domain/errors.go) の `Rule` 一覧に `VShipmentID` / `VTrackingNumber`<br>② [httpapi/problem.go](../contexts/ordering/internal/adapter/inbound/httpapi/problem.go) の `domainReasons` に定型文（**受信値も閾値も載せない**）<br>③ [contracts/ordering/openapi.yaml](../contracts/ordering/openapi.yaml) の `InvalidParam.code` の `enum` に `invalid_shipment_id` / `invalid_tracking_number`。**追加先は「ドメイン検証語彙」群**で、上にある「契約検証語彙」群ではありません |
| **新しい type URI 種別** | **[shared/problem/type_uri.go](../shared/problem/type_uri.go)** | 定数 `TypeOrderNotConfirmedForShipment`、`titles` の 1 行、`detail` の定型文。**`titles` を足さないと `TitleOf` が理由句へ fallback し、409 の複数種別が同じ title になります** |
| **番兵のステータス対応** | **[httpapi/errmap.go](../contexts/ordering/internal/adapter/inbound/httpapi/errmap.go) の 3 関数** | ① `classify()` に 404 / 409 / 422 の分岐を**番兵ごとに `errors.Is` で明示列挙**（型で書かない）<br>② `problemTypeSuffix()` に**より特殊な種別を先に**判定<br>③ `detailOf()` に定型文（**足さないと「予期しないエラーが発生しました」に落ちます**） |
| **`invalid-params` の位置解決** | `locate()` と `jsonNames` | `application.locate(at, err)` をパス断片・本文断片で呼ぶ。[httpapi/problem.go](../contexts/ordering/internal/adapter/inbound/httpapi/problem.go) の `jsonNames` へ **`"ShipmentId": "id"` は必要**（パスパラメータ名が違う）。**`TrackingNumber` は不要**（機械変換で `trackingNumber` になる） |

> **`classify()` は型ではなく番兵で書きます。** `domain.FieldViolation` のような型で 422 を
> 判定すると、番兵一覧の抜けに気づけません。実際、空の追跡番号が契約の宣言する 422 ではなく
> **500** に落ちます。

> ### `shared/problem/` に触るのは INV-4 の既知の限界です
>
> 「集約ルートを 1 つ足すとき既存の集約と共通機構に触らない」という不変条件（INV-4）は、
> **`shared/uow` / `shared/outbox` / `shared/event` と、送信アダプタの共通機構
> [memory/rows.go](../contexts/ordering/internal/adapter/outbound/memory/rows.go) については成立します**
> （`Shipment` を足したコミットの差分が空であることで確認済み）。
>
> **`shared/problem/` だけは例外です。** 新しい問題種別を伴う集約を足すと、type URI の台帳に
> 1 行足すことになります。台帳を共有しているのは「同じ status に別の種別を与える」という
> 規則を 1 箇所で守るためで、この結合は意図的です。**限界として記録しておきます。**

## 5. スキーマ —— 外部キーを張らない

[../contexts/ordering/db/schema.sql](../contexts/ordering/db/schema.sql) の `ordering.shipments` は
`order_id` に**外部キー制約を張っていません**。これは意図的な選択です。

集約間は識別子で参照し、参照整合性はデータベースではなく**アプリケーションの事前条件**
（注文が存在し `confirmed` であること）で扱います。外部キーを張ると 2 つの集約が DB レベルで
結合し、**将来それぞれを別サービス・別データベースへ分けられなくなります**。schema-per-context
で境界を分けている理由と同じです。

更新は楽観的排他制御に従います（`UpdateShipment` が `where id = $1 and version = $2`）。
`roles.sql` は `GRANT … ON ALL TABLES IN SCHEMA` と既定権限で新しい表を自動的に含むので、
表を足しただけなら編集は要りません。

## 6. テストと検査

- ドメインの状態遷移・値オブジェクトの検証・イベントの記録を単体テストで固めます
- ユースケースは happy path と、各エラー経路（404 / 409 / 422）を通します
- **楽観的排他制御は注入した衝突ではなく、ストアの版チェックを通る実物の衝突**でも確かめます
  （[memory/shipment_store_test.go](../contexts/ordering/internal/adapter/outbound/memory/shipment_store_test.go)）
- HTTP は `problem+json` の `type` / `title` / `detail` / `invalid-params` まで見ます

最後に `make ci` を通します（整形・`go vet`・`golangci-lint`・規約ゲート・ビルド・テスト・
カバレッジ）。規約ゲートは新しい集約に対して検査 12〜15 を自動的に適用します —— 追加の
設定は要りません。
