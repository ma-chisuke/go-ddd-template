// Package port は、注文コンテキストが公開する「翻訳済み DTO」だけを収める公開パッケージ。
//
// ここに置くのはドメインの値オブジェクトではなく、コンテキスト境界を跨いでよいプレーンな
// データ転送オブジェクト（DTO）である。合成ルート（cmd/dev など、モジュール外のアダプタ）が
// 腐敗防止層（ACL）ポートの引数型を名指しできるように公開する一方、ポートのインターフェース
// 自体（StockReserver）は internal/application に留める。
//
// 内部のドメイン型（order.SKU / order.Quantity など）はここには現れない。それらは境界で
// この翻訳済み DTO へ変換される（コンテキスト間で Go の共有ドメイン型を渡さないため）。
package port

// ReserveLine は在庫予約要求の 1 行を表す翻訳済み DTO（SKU と数量）。
// 注文コンテキストの内部ドメイン VO ではなく、境界を跨げるプレーンな型である。
type ReserveLine struct {
	// SKU は在庫識別子の文字列表現。
	SKU string
	// Qty は予約する数量。
	Qty int
}
