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

	// ErrInvalidQuantity は、数量が不正（負数、または要求数が 0）であることを表す。
	ErrInvalidQuantity = errors.New("数量が不正です")

	// ErrInvalidReservationRef は、予約参照（相関 ID）が不正（空文字など）であることを表す。
	ErrInvalidReservationRef = errors.New("予約参照が不正です")

	// ErrInsufficientStock は、要求数量が利用可能在庫を上回り予約できないことを表す。
	// 予約が許容されるのは、要求 Quantity が available 以下のときのみ（在庫不変条件）。
	ErrInsufficientStock = errors.New("在庫が不足しています")

	// ErrReservationNotFound は、Confirm 対象の有効な予約が存在しないことを表す。
	// 既に Reap 済み、または速い取消で解放済みの参照に対する Confirm で返る。
	ErrReservationNotFound = errors.New("予約が見つかりません")
)
