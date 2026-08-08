# DDD パターン → このリポジトリでの実装位置

この索引は「**今あるものがどこにあるか**」を引く表です。「**新しいものをどう足すか**」の手順は
レシピ側にあります（索引とレシピで役割を分け、内容を重複させていません）——
ユースケースを足すなら [add-a-use-case.md](./add-a-use-case.md)、**集約ルートを足すなら**
[add-an-aggregate.md](./add-an-aggregate.md) です。層の分離そのものの規約は [../CONVENTIONS.md](../CONVENTIONS.md)、境界を割った理由は
[why-these-boundaries.md](./why-these-boundaries.md) を参照してください。

## 1. 戦術パターン（モデルの構成要素）

| パターン | 一言の定義 | このリポジトリでの実装 |
| --- | --- | --- |
| 集約（Aggregate） | 同時に一貫していなければならないオブジェクトの塊。外からは集約ルート経由でのみ操作する | `Order`・`Shipment`（[../contexts/ordering/internal/domain/order.go](../contexts/ordering/internal/domain/order.go) ／ [shipment.go](../contexts/ordering/internal/domain/shipment.go)）／ `StockItem`（[../contexts/inventory/internal/domain/stock_item.go](../contexts/inventory/internal/domain/stock_item.go)） |
| **集約間の参照は識別子で** | 集約ルートは他の集約ルートの実体を持たず、識別子だけを持つ。境界を跨ぐ整合性を引き受けない | `Shipment.orderID` は `OrderID`（`*Order` ではない）。[../contexts/ordering/internal/domain/shipment.go](../contexts/ordering/internal/domain/shipment.go)。**検査 14 が機械強制**し、手順は [add-an-aggregate.md](./add-an-aggregate.md) |
| エンティティ（Entity） | 同一性を持ち、状態が時間とともに変わるもの | `Reservation`（[../contexts/inventory/internal/domain/reservation.go](../contexts/inventory/internal/domain/reservation.go)）— 集約 `StockItem` の子 |
| 値オブジェクト（Value Object） | 同一性を持たず、値そのもので等価性が決まる不変の型 | `SKU` / `Quantity` / `Money` / `ReservationRef` / `CustomerID` / `OrderID`。**境界ごとに独立所有**する（[../contexts/ordering/internal/domain/](../contexts/ordering/internal/domain/) と [../contexts/inventory/internal/domain/](../contexts/inventory/internal/domain/) に同名で別の型がある） |
| ドメインイベント（Domain Event） | ドメインで起きた「事実」。過去形で名づける | 注文 2 種（[../contexts/ordering/internal/domain/event.go](../contexts/ordering/internal/domain/event.go)）と在庫 5 種（[../contexts/inventory/internal/domain/event.go](../contexts/inventory/internal/domain/event.go)）の計 7 種。集約が記録し、`PullEvents` で取り出す |
| ドメインサービス（Domain Service） | 1 つの集約に閉じない振る舞い。状態を持たない | `ReservationService`（[../contexts/inventory/internal/domain/reservation_service.go](../contexts/inventory/internal/domain/reservation_service.go)）— マルチ SKU 予約の全か無か。**リポジトリを引かず**、ユースケースから引き当て済みの集約を受け取る |
| リポジトリ（Repository） | 集約の永続化を抽象化する。**ポートはアプリケーション層**、実装はアダプタ層 | ポート: `StockStore` / `OrderStore` / `ShipmentStore`（[../contexts/inventory/internal/application/ports.go](../contexts/inventory/internal/application/ports.go) ／ [../contexts/ordering/internal/application/ports.go](../contexts/ordering/internal/application/ports.go)）。実装: `memory`（[../contexts/inventory/internal/adapter/outbound/memory/stock_rows.go](../contexts/inventory/internal/adapter/outbound/memory/stock_rows.go)）と `postgres`（[../contexts/inventory/internal/adapter/outbound/postgres/stock_store.go](../contexts/inventory/internal/adapter/outbound/postgres/stock_store.go)） |
| 再構成（Reconstitution） | 永続化された状態から集約を組み立て直す。検証済みなのでイベントは発生させない | `ReconstituteOrder`（[../contexts/ordering/internal/domain/order.go](../contexts/ordering/internal/domain/order.go)）／ `ReconstituteStockItem`・`ReconstituteReservation`（[../contexts/inventory/internal/domain/stock_item.go](../contexts/inventory/internal/domain/stock_item.go)） |

## 2. 戦略パターン（大きな構造）

| パターン | 一言の定義 | このリポジトリでの実装 |
| --- | --- | --- |
| 境界づけられたコンテキスト（Bounded Context） | モデルと語彙が一貫して通用する範囲。その外では同じ語が別の意味を持つ | [../contexts/inventory/](../contexts/inventory/) と [../contexts/ordering/](../contexts/ordering/) — **1 コンテキスト = 1 Go モジュール**。4 層を `internal/` に隠し、公開ファサード（[../contexts/ordering/ordering.go](../contexts/ordering/ordering.go)）と `port/` だけを見せる |
| ユビキタス言語（Ubiquitous Language） | 境界の内側で開発者と業務担当が共有する語彙 | [../contexts/inventory/GLOSSARY.md](../contexts/inventory/GLOSSARY.md) ／ [../contexts/ordering/GLOSSARY.md](../contexts/ordering/GLOSSARY.md)、境界を跨ぐ同名語の対比は [glossary.md](./glossary.md) |
| コンテキストマップ（Context Map） | コンテキスト間の関係（上流／下流、供給者／顧客）と、その連携の形 | [context-map.md](./context-map.md) — 在庫が上流／供給者、注文が下流／顧客で ACL を持つ側 |
| 腐敗防止層（Anti-Corruption Layer） | 相手のモデルを自分のモデルへ翻訳し、相手の概念が漏れ込むのを防ぐ層 | [../contexts/ordering/internal/adapter/outbound/aclhttp/](../contexts/ordering/internal/adapter/outbound/aclhttp/) — ポートは [../contexts/ordering/internal/application/ports.go](../contexts/ordering/internal/application/ports.go) の `StockReserver`、番兵は [../contexts/ordering/internal/application/errors.go](../contexts/ordering/internal/application/errors.go)。在庫の 409 / 5xx を注文側の番兵へ翻訳する |
| 公表された言語（Published Language） | 境界を跨ぐやりとりのために公開された、実装から独立した契約 | [../contracts/](../contracts/) — OpenAPI（公開・内部）とクロスコンテキストのメッセージスキーマ。コード生成の入力を兼ねる |
| 共有カーネル（Shared Kernel）の**不採用** | 複数コンテキストで同じモデルを共有する形。採らない選択も設計判断である | ドメイン値オブジェクトは共有しない。[../shared/](../shared/) に置くのはドメイン非依存の機構だけで、`shared-purity` rule が `shared/` からコンテキストへの import を禁じている |

## 3. 支援機構（DDD を実装で成立させる配管）

| 機構 | 一言の定義 | このリポジトリでの実装 |
| --- | --- | --- |
| 作業単位（Unit of Work） | 1 つのトランザクションの範囲を明示し、その中でだけ書き込みを許す | [../shared/uow/uow.go](../shared/uow/uow.go)（純粋な契約 + 再試行 `Run`）と [../shared/uow/pgxuow/uow.go](../shared/uow/pgxuow/uow.go)（pgx 実装）。**トランザクションを `context.Context` に載せない**（束ねたリポジトリをクロージャ引数で渡す） |
| トランザクショナルアウトボックス | 集約の保存と同一トランザクションでメッセージを積み、二重書き込みを避ける | [../shared/outbox/outbox.go](../shared/outbox/outbox.go)。`outbox` は一時的な配送キュー（送出成功後に削除）、`events` は恒久イベントログで、`Enqueue` が同一トランザクションで両方書く |
| 楽観的排他制御 | バージョン番号の比較で更新の衝突を検出し、再試行する | 集約はバージョンを**保持するだけ**（`Order.Version` / `StockItem.Version`）で、比較はリポジトリ（[../contexts/ordering/internal/adapter/outbound/postgres/order_store.go](../contexts/ordering/internal/adapter/outbound/postgres/order_store.go)）が行い、`uow.Run` が再試行する |
| プロセス内イベント配信 | 永続化に成功したあとにドメインイベントを購読者へ配る | [../shared/event/event.go](../shared/event/event.go)（型なしコア）と [../shared/event/typed.go](../shared/event/typed.go)（型付きファサード `Typed[E]`）。ドメイン層は `shared/event` を import しない |
| HTTP サーバランナー | 複数の HTTP サーバの起動・停止・グレースフルシャットダウンをまとめる | [../shared/serve/serve.go](../shared/serve/serve.go) — サーバ本数に依存しない（注文は公開 1 本、在庫は公開 + 内部の 2 本） |
| RFC 9457（Problem Details） | エラー応答の標準形式。`type` URI で問題種別を機械識別できるようにする | [../shared/problem/](../shared/problem/) — `type` URI 台帳・`code` 語彙・パス表記。各コンテキストの変換は `internal/adapter/inbound/*/problem.go` |

## 4. 意図的に実装していないパターン

「知らない」のか「意図的に持たない」のかを読者が判別できるよう、**採らなかったパターンも
明記します**。理由はいずれも共通で、**題材のドメインを小さく保つため**です（このテンプレートの
製品は DDD のパターンそのものであって、ドメインの深さではありません）。パターンの否定では
ありません。

| パターン | 状態 | 理由 |
| --- | --- | --- |
| 仕様（Specification） | 未実装 | 検証規則は `Rule` の一覧と集約のメソッドで足りている。組み合わせ可能な述語オブジェクトが要るほど条件が複雑ではない |
| ポリシー（Policy） | 未実装 | 差し替えたい業務方針（価格戦略・引当戦略）が題材に無い。TTL や閾値は設定値であって方針オブジェクトではない |
| 明示的なファクトリ（Factory） | 未実装 | 生成の複雑さがコンストラクタ `New<Type>(...) (Type, error)` に収まっている。専用のファクトリ型を挟むと間接が増えるだけになる |
| イベントソーシング（Event Sourcing） | 未実装 | 状態は現在値として永続化し、ドメインイベントは通知と記録に使う。`events` 表は追記専用の履歴だが、状態の**復元元**ではない |
| CQRS（読み書きモデルの分離） | 未実装 | [../contexts/ordering/internal/application/view.go](../contexts/ordering/internal/application/view.go) は読み取り専用のビューだが、書き込みモデルと**同じ**永続化を読む。独立した読み取りモデルや射影は持たない |
