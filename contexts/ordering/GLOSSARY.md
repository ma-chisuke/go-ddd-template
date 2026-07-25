# 用語集 — 受注（Ordering）コンテキスト

このコンテキストのユビキタス言語です。ここに載る語は**この境界の内側でだけ**通用します。
同じ語が在庫（Inventory）コンテキストにも存在することがありますが、別の型・別の意味です
（対比は [../../docs/glossary.md](../../docs/glossary.md)）。

対象は `internal/domain/order` の全公開型と番兵エラーです。コンテキストをまるごとコピーして
自分のプロジェクトの出発点にするとき、この用語集も一緒に付いてきます
（[../../docs/copy-a-context.md](../../docs/copy-a-context.md)）。

集約ルートと不変条件の要約は `go doc ./internal/domain/order`
（[internal/domain/order/doc.go](internal/domain/order/doc.go)）にあります。

| 語（Go 型） | 業務上の意味 | 種別 | 定義ファイル |
| --- | --- | --- | --- |
| `Order` | 顧客からの 1 件の注文。`OrderID` で識別し、明細を子として内包する。合計金額を自ら導出し、取消は Confirmed からのみ許す | 集約ルート | [internal/domain/order/order.go](internal/domain/order/order.go) |
| `OrderLine` | 注文明細の 1 行。SKU・数量・単価の対で、小計（単価 × 数量）を導出する | 子要素（値） | [internal/domain/order/order_line.go](internal/domain/order/order_line.go) |
| `OrderID` | 注文の識別子。採番はアプリケーション層が行い、ドメインは与えられた文字列を検証して包む | 値オブジェクト | [internal/domain/order/order_id.go](internal/domain/order/order_id.go) |
| `CustomerID` | 注文した顧客の識別子 | 値オブジェクト | [internal/domain/order/customer_id.go](internal/domain/order/customer_id.go) |
| `SKU` | 注文明細が**指す**商品の識別子。注文は在庫の実体を知らない | 値オブジェクト | [internal/domain/order/sku.go](internal/domain/order/sku.go) |
| `Quantity` | 注文する数量。**1 以上**（注文行に数量 0 は無い）。加減算を持たない | 値オブジェクト | [internal/domain/order/quantity.go](internal/domain/order/quantity.go) |
| `Money` | 金額（最小通貨単位の額 + ISO-4217 の通貨コード）。`Add` / `Mul` / `IsZero` を持ち、通貨をまたぐ加算は失敗する | 値オブジェクト | [internal/domain/order/money.go](internal/domain/order/money.go) |
| `ReservationRef` | 在庫予約の参照（相関 ID）。`OrderID` から**決定的に導出**する（`DeriveReservationRef`）ため、再試行が同じ参照を生む | 値オブジェクト | [internal/domain/order/reservation_ref.go](internal/domain/order/reservation_ref.go) |
| `Status` | 注文の状態。`StatusConfirmed`（確定）→ `StatusCancelled`（取消）の一方向のみ。v1 に履行（fulfillment）は無い | 状態 | [internal/domain/order/status.go](internal/domain/order/status.go) |
| `DomainEvent` | このコンテキストのドメインイベントの共通契約（`EventName` と `OccurredAt`）。共有モジュールの型に依存しないためドメイン独自に定義する | イベント契約 | [internal/domain/order/event.go](internal/domain/order/event.go) |
| `OrderPlaced` | 注文が確定した。v1 ではプロセス内イベントで、クロスコンテキストの購読者は持たない | ドメインイベント | [internal/domain/order/event.go](internal/domain/order/event.go) |
| `OrderCancelled` | 注文が取り消された。在庫コンテキストが購読し、非同期に予約を解放する | ドメインイベント | [internal/domain/order/event.go](internal/domain/order/event.go) |
| `Rule` | 検証規則 1 件。「どのフィールドが / どの `code` で / どの番兵に」対応するかを 1 箇所に束ねる | 検証の表現 | [internal/domain/order/errors.go](internal/domain/order/errors.go) |
| `FieldViolation` | 規則違反。違反したフィールドと固定語彙の `code` を持ち、番兵を `Unwrap` するので `errors.Is` の判定は変わらない | 検証の表現 | [internal/domain/order/errors.go](internal/domain/order/errors.go) |
| `ErrEmptyOrder` | 明細が 1 行も無い注文を作成しようとした | 番兵エラー | [internal/domain/order/errors.go](internal/domain/order/errors.go) |
| `ErrOrderNotConfirmed` | Confirmed でない注文を取り消そうとした | 番兵エラー | [internal/domain/order/errors.go](internal/domain/order/errors.go) |
| `ErrOrderNotFound` | 指定した ID の注文が存在しない | 番兵エラー | [internal/domain/order/errors.go](internal/domain/order/errors.go) |
| `ErrInvalidOrderID` | 注文 ID が不正（空文字など） | 番兵エラー | [internal/domain/order/errors.go](internal/domain/order/errors.go) |
| `ErrInvalidCustomerID` | 顧客 ID が不正（空文字など） | 番兵エラー | [internal/domain/order/errors.go](internal/domain/order/errors.go) |
| `ErrInvalidSKU` | SKU が不正（空文字など） | 番兵エラー | [internal/domain/order/errors.go](internal/domain/order/errors.go) |
| `ErrInvalidQuantity` | 数量が不正（注文行の数量が 1 未満） | 番兵エラー | [internal/domain/order/errors.go](internal/domain/order/errors.go) |
| `ErrInvalidMoney` | 金額が不正（負数・通貨が空・通貨不一致） | 番兵エラー | [internal/domain/order/errors.go](internal/domain/order/errors.go) |
| `ErrInvalidReservationRef` | 予約参照が不正（空文字など） | 番兵エラー | [internal/domain/order/errors.go](internal/domain/order/errors.go) |

> **境界を跨ぐときは翻訳する**: 上の型はどれもこのコンテキストの内部型です。他コンテキストや
> 外部サービスへ渡すときは、そのまま渡さず翻訳済みの公開型（[port/](port/) の DTO や
> `contracts/events/` のメッセージ契約）へ変換します。`Status` / `Money` などの内部表現が
> 相手の制約になることを防ぐためです。
