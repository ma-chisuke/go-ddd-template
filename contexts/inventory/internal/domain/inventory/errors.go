package inventory

import "errors"

// ドメイン層のセンチネルエラー。呼び出し側は errors.Is で判定する。
// いずれも「予期される失敗」であり、panic ではなく値として返す。
// 上位層（アプリケーション／インターフェース）でラップする際は %w を用いて
// これらを保持し、errors.Is による判定を壊さないこと。
var (
	// ErrStockItemNotFound は、指定した SKU の在庫項目が存在しないことを表す。
	ErrStockItemNotFound = errors.New("在庫項目が見つかりません")

	// ErrInvalidSKU は、SKU が不正（空文字など）であることを表す。
	ErrInvalidSKU = errors.New("SKU が不正です")

	// ErrInvalidQuantity は、数量が不正（負数、または補充数が 0）であることを表す。
	ErrInvalidQuantity = errors.New("数量が不正です")
)
