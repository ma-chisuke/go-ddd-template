# 用語集 — 在庫（Inventory）コンテキスト

このコンテキストのユビキタス言語です。ここに載る語は**この境界の内側でだけ**通用します。
同じ語が受注（Ordering）コンテキストにも存在することがありますが、別の型・別の意味です
（対比は [../../docs/glossary.md](../../docs/glossary.md)）。

対象は `internal/domain` の全公開型と番兵エラーです。コンテキストをまるごとコピーして
自分のプロジェクトの出発点にするとき、この用語集も一緒に付いてきます
（[../../docs/copy-a-context.md](../../docs/copy-a-context.md)）。

このコンテキストは「注文」という概念を**持ちません**。予約は外から与えられた不透明な相関 ID
（`ReservationRef`）で識別され、その由来を在庫側は解釈しません。集約ルートと不変条件の要約は
`go doc ./internal/domain`
（[internal/domain/doc.go](internal/domain/doc.go)）にあります。

| 語（Go 型） | 業務上の意味 | 種別 | 定義ファイル |
| --- | --- | --- | --- |
| `StockItem` | 1 つの SKU の在庫。`SKU` で識別し、予約を子として内包する。`available`（利用可能）を非負に保ち、`reserved` を導出する | 集約ルート | [internal/domain/stock_item.go](internal/domain/stock_item.go) |
| `Reservation` | 1 件の予約。参照・数量・状態・期限を持つ。生成と遷移は集約ルート経由でのみ行う | 子エンティティ | [internal/domain/reservation.go](internal/domain/reservation.go) |
| `SKU` | 在庫項目**そのもの**の識別子（集約の同一性） | 値オブジェクト | [internal/domain/stock_item.go](internal/domain/stock_item.go) |
| `Quantity` | 在庫数量。**0 以上**（利用可能在庫を表すため 0 が有効）。`Add` / `Sub`（不足で失敗）/ `GreaterThan` / `IsZero` を持つ | 値オブジェクト | [internal/domain/quantity.go](internal/domain/quantity.go) |
| `ReservationRef` | 予約の識別子（相関 ID）。**外から受け取る**不透明な値で、導出規則を持たない | 値オブジェクト | [internal/domain/reservation.go](internal/domain/reservation.go) |
| `ReservationLine` | マルチ SKU 予約の 1 行（SKU と要求数量の対） | 値オブジェクト | [internal/domain/reservation_service.go](internal/domain/reservation_service.go) |
| `ReservationStatus` | 予約の状態。`ReservationPending`（仮予約・TTL あり）→ `ReservationConfirmed`（確定・TTL なし） | 状態 | [internal/domain/reservation.go](internal/domain/reservation.go) |
| `ReservationService` | マルチ SKU 予約の「全か無か」割り当て（`Allocate`）。**リポジトリを引かず**、ユースケースから引き当て済みの `[]*StockItem` を受け取る | ドメインサービス | [internal/domain/reservation_service.go](internal/domain/reservation_service.go) |
| `DomainEvent` | このコンテキストのドメインイベントの共通契約（`EventName` と `OccurredAt`）。共有モジュールの型に依存しないためドメイン独自に定義する | イベント契約 | [internal/domain/event.go](internal/domain/event.go) |
| `StockReplenished` | 在庫が補充された | ドメインイベント | [internal/domain/event.go](internal/domain/event.go) |
| `StockReserved` | 在庫が仮予約された（二相予約の第 1 相） | ドメインイベント | [internal/domain/event.go](internal/domain/event.go) |
| `StockReservationConfirmed` | 仮予約が確定した（二相予約の第 2 相） | ドメインイベント | [internal/domain/event.go](internal/domain/event.go) |
| `StockReleased` | 予約が解放された（取消・期限切れ）。数量は `available` へ戻る | ドメインイベント | [internal/domain/event.go](internal/domain/event.go) |
| `StockDepleted` | 利用可能在庫が 0 に到達した。発行とログのみで購読者は持たない | ドメインイベント | [internal/domain/event.go](internal/domain/event.go) |
| `Rule` | 検証規則 1 件。「どのフィールドが / どの `code` で / どの番兵に」対応するかを 1 箇所に束ねる | 検証の表現 | [internal/domain/errors.go](internal/domain/errors.go) |
| `FieldViolation` | 規則違反。違反したフィールドと固定語彙の `code` を持ち、番兵を `Unwrap` するので `errors.Is` の判定は変わらない | 検証の表現 | [internal/domain/errors.go](internal/domain/errors.go) |
| `ErrStockItemNotFound` | 指定した SKU の在庫項目が存在しない | 番兵エラー | [internal/domain/errors.go](internal/domain/errors.go) |
| `ErrInvalidSKU` | SKU が不正（空文字など） | 番兵エラー | [internal/domain/errors.go](internal/domain/errors.go) |
| `ErrInvalidQuantity` | 数量が不正（負数、または要求数が 0） | 番兵エラー | [internal/domain/errors.go](internal/domain/errors.go) |
| `ErrInvalidReservationRef` | 予約参照が不正（空文字など） | 番兵エラー | [internal/domain/errors.go](internal/domain/errors.go) |
| `ErrInsufficientStock` | 要求数量が利用可能在庫を上回り予約できない | 番兵エラー | [internal/domain/errors.go](internal/domain/errors.go) |
| `ErrReservationNotFound` | 確定（`Confirm`）の対象となる有効な予約が存在しない | 番兵エラー | [internal/domain/errors.go](internal/domain/errors.go) |

> **境界を跨ぐときは翻訳する**: 上の型はどれもこのコンテキストの内部型です。他コンテキストや
> 外部サービスへ渡すときは、そのまま渡さず翻訳済みの公開型（[port/](port/) の DTO や
> `contracts/` の OpenAPI / メッセージ契約）へ変換します。在庫の識別規則や数量の演算が
> 相手の制約になることを防ぐためです。
