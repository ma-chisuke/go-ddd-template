// Package inventory は在庫（Inventory）の境界づけられたコンテキストのドメイン層である。
//
// # 集約ルート
//
// 集約ルートは StockItem である。SKU で識別し（SKU ごとに 1 つ存在する）、予約
// Reservation を子エンティティとして内包する。子の生成と遷移は集約ルート経由でのみ行う
// （Replenish / Reserve / Confirm / Release / ReapExpired）。在庫の数え方は
// 「available（自由に予約できる数）+ reserved（有効な予約の合計）= 総在庫」である。
// 永続化された状態からの復元は ReconstituteStockItem と ReconstituteReservation が担う。
// 楽観的排他制御のバージョン番号は集約が「保持」するだけで、比較（compare-and-set）は
// リポジトリの責務である。
//
// # 不変条件
//
// StockItem は次の不変条件を常に自身で守る。
//
//   - available（利用可能在庫）は非負である。要求数量が available を上回る予約は
//     ErrInsufficientStock で拒否する。
//   - reserved は状態として持たず、有効な予約（pending + confirmed）の数量合計として
//     導出する。
//   - 予約は二相である（Reserve -> Confirm）。pending は TTL を持ち、期限切れは
//     ReapExpired が解放する。
//   - confirmed の予約は ReapExpired で決して解放されない（TTL を持たない）。
//   - Reserve / Confirm / Release はいずれも冪等である。同一の ReservationRef に対する
//     再実行が状態を壊さない（自動リトライと at-least-once 配送のもとで安全にするため）。
//
// # ドメインサービス
//
// ReservationService はマルチ SKU 予約の「全か無か」割り当て（Allocate）を担う。これは
// 複数の StockItem 集約を 1 つの作業単位で跨ぐ、唯一の意図的なケースである。
// ドメインサービスは自分ではリポジトリを引かず、ユースケースが引き当て済みの
// []*StockItem を受け取って操作する（純粋なドメインを保つための規約）。
// 原子性の担保はアプリケーション層の作業単位（UnitOfWork）の役目である。
//
// # 純粋性
//
// この層は context.Context・リポジトリ（そのポート interface を含む）・永続化・IO・
// フレームワーク・アダプタを一切 import しない。依存するのは標準ライブラリだけである。
// ポートはアプリケーション層が定義し、アダプタが実装する（依存性逆転）。
// この純粋性は規約コメントではなく、golangci-lint の depguard rule domain-purity が
// 機械的に強制している。
//
// # 語彙
//
// 用語の定義は contexts/inventory/GLOSSARY.md にある。SKU・Quantity・ReservationRef は
// 受注（Ordering）コンテキストにも同名で存在するが、別パッケージの別の型であり意味も
// 異なる（対比表は docs/glossary.md）。とくにこのコンテキストは「注文」という概念を
// 持たない。ReservationRef は呼び出し側が供給する不透明な相関 ID であり、在庫側はその
// 由来を解釈しない。
package inventory
