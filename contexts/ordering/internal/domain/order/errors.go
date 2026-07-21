package order

import "errors"

// ドメイン層のセンチネルエラー。呼び出し側は errors.Is で判定する。
// いずれも「予期される失敗」であり、panic ではなく値として返す。
// 上位層（アプリケーション／インターフェース）でラップする際は %w を用いて
// これらを保持し、errors.Is による判定を壊さないこと。
var (
	// ErrEmptyOrder は、明細が 1 行も無い注文を作成しようとしたことを表す。
	ErrEmptyOrder = errors.New("注文には 1 行以上の明細が必要です")

	// ErrOrderNotConfirmed は、Confirmed でない注文を取り消そうとしたことを表す。
	// 取消が許容されるのは Confirmed 状態の注文のみ（状態不変条件）。
	ErrOrderNotConfirmed = errors.New("注文が確定状態ではありません")

	// ErrOrderNotFound は、指定した ID の注文が存在しないことを表す。
	ErrOrderNotFound = errors.New("注文が見つかりません")

	// ErrInvalidOrderID は、注文 ID が不正（空文字など）であることを表す。
	ErrInvalidOrderID = errors.New("注文 ID が不正です")

	// ErrInvalidCustomerID は、顧客 ID が不正（空文字など）であることを表す。
	ErrInvalidCustomerID = errors.New("顧客 ID が不正です")

	// ErrInvalidSKU は、SKU が不正（空文字など）であることを表す。
	ErrInvalidSKU = errors.New("SKU が不正です")

	// ErrInvalidQuantity は、数量が不正（注文行数量が 1 未満）であることを表す。
	// 注文行の数量は 1 以上でなければならない（0 の注文行は無い）。
	ErrInvalidQuantity = errors.New("数量が不正です")

	// ErrInvalidMoney は、金額が不正（負数、通貨が空、または通貨不一致）であることを表す。
	ErrInvalidMoney = errors.New("金額が不正です")

	// ErrInvalidReservationRef は、予約参照が不正（空文字など）であることを表す。
	ErrInvalidReservationRef = errors.New("予約参照が不正です")
)
