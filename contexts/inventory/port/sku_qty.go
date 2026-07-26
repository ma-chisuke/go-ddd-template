// Package port は、在庫コンテキストが公開する「翻訳済み DTO」だけを収める公開パッケージ。
//
// ここに置くのはドメインの値オブジェクトではなく、コンテキスト境界を跨いでよいプレーンな
// データ転送オブジェクト（DTO）である。合成ルート（cmd/dev など、モジュール外のアダプタ）が
// 公開シーム（inventory.Module.Reserve など）の引数型を名指しできるように公開する一方、
// ユースケースやポートのインターフェース自体は internal/application に留める。
//
// 内部のドメイン型（domain.SKU / domain.Quantity など）はここには現れない。それらは
// 境界でこの翻訳済み DTO へ変換される（コンテキスト間で Go の共有ドメイン型を渡さないため）。
// 注文コンテキストの ordering/port.ReserveLine と対称の位置づけである。
package port

// SKUQty は在庫予約要求の 1 行を表す翻訳済み DTO（SKU と数量）。
// 在庫コンテキストの内部ドメイン VO ではなく、境界を跨げるプレーンな型である。
type SKUQty struct {
	// SKU は在庫識別子の文字列表現。
	SKU string
	// Qty は予約する数量。
	Qty int
}
