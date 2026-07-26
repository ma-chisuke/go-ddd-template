// Package domain は受注（Ordering）の境界づけられたコンテキストのドメイン層である。
//
// # 集約ルート
//
// 集約ルートは Order である。OrderID で識別し、明細 OrderLine を子として内包する。
// 子は集約ルート経由でのみ生成・参照し、外から直接組み替えることはできない
// （Lines はコピーを返す）。永続化された状態からの復元は ReconstituteOrder が担う。
// 楽観的排他制御のバージョン番号は集約が「保持」するだけで、比較（compare-and-set）は
// リポジトリの責務である。
//
// # 不変条件
//
// Order は次の不変条件を常に自身で守る。
//
//   - 明細は 1 行以上である（空なら ErrEmptyOrder）。
//   - 合計金額 Total は各明細の小計の総和として集約が導出する（外から設定できない）。
//     行をまたいで通貨が食い違えば ErrInvalidMoney。
//   - 取消は Confirmed 状態からのみ許される（それ以外は ErrOrderNotConfirmed）。
//     v1 の状態モデルは Confirmed -> Cancelled の一方向だけである。
//   - 在庫予約の参照 ReservationRef は OrderID から決定的に導出する
//     （DeriveReservationRef）。同一注文の再試行が常に同一の参照を生むため、
//     在庫側の冪等な予約と噛み合い、二重予約を避けられる。
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
// 用語の定義は contexts/ordering/GLOSSARY.md にある。SKU・Quantity・ReservationRef は
// 在庫（Inventory）コンテキストにも同名で存在するが、import パスの異なる別パッケージ
// （contexts/inventory/internal/domain）の別の型であり意味も異なる
// （対比表は docs/glossary.md）。境界を跨ぐときはこれらの内部型をそのまま渡さず、
// 翻訳済みの公開型（contexts/ordering/port の DTO）を使う。
package domain
