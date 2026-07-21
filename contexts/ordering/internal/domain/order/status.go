package order

// Status は注文の状態を表す。v1 の状態モデルは Confirmed -> Cancelled のみで、
// fulfillment（履行）は範囲外のため Fulfilled 状態は無い。
type Status int

const (
	// StatusConfirmed は確定済みの注文。作成（place）時に在庫予約が成立するとこの状態になる。
	StatusConfirmed Status = iota
	// StatusCancelled は取り消された注文。Confirmed からのみ遷移できる。
	StatusCancelled
)

// String は状態の文字列表現を返す（永続化・ログ・API 表示用）。
func (s Status) String() string {
	switch s {
	case StatusConfirmed:
		return "confirmed"
	case StatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}
