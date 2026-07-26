package domain

import "fmt"

// Quantity は非負の個数を表す値オブジェクト（n >= 0）。不変であり、
// 生成後は値を変更できない。ゼロ値の Quantity{} は数量 0 として有効である。
type Quantity struct {
	value int
}

// NewQuantity は非負であることを検証して Quantity を生成する。
// 負数の場合は ErrInvalidQuantity を包んだ FieldViolation を返す
// （errors.Is(err, ErrInvalidQuantity) は従来どおり真になる — 規則 R-15）。
//
// 重要: 0 は「有効な数量」として通過する（利用可能在庫を表すため）。したがって
// quantity: 0 は値オブジェクトを通り抜け、集約の不変条件（Replenish / Reserve /
// ReservationService.Allocate）で初めて弾かれる。その集約側の規則も FieldViolation で
// 名乗らないと、注文コンテキストと比べて「422 でフィールドが分かるときと分からないときが
// ある」という体験の割れが在庫側で再現する（FR-4.7）。
func NewQuantity(n int) (Quantity, error) {
	if n < 0 {
		return Quantity{}, VQuantity.Violated("数量は 0 以上でなければなりません（指定値: %d）", n)
	}
	return Quantity{value: n}, nil
}

// Int は数量を int で返す。
func (q Quantity) Int() int {
	return q.value
}

// IsZero は数量が 0 かどうかを返す。
func (q Quantity) IsZero() bool {
	return q.value == 0
}

// Add は 2 つの数量の和を返す。いずれも非負なので和も非負であり、常に有効。
func (q Quantity) Add(other Quantity) Quantity {
	return Quantity{value: q.value + other.value}
}

// Sub は q から other を引いた数量を返す。結果が負になる場合は非負制約に反するため
// ErrInvalidQuantity を返す。予約時の在庫引き落としなどで用いる（呼び出し側は
// 事前に other <= q を確認しておくのが望ましい）。
func (q Quantity) Sub(other Quantity) (Quantity, error) {
	if other.value > q.value {
		return Quantity{}, fmt.Errorf("数量 %d から %d は引けません（負になります）: %w", q.value, other.value, ErrInvalidQuantity)
	}
	return Quantity{value: q.value - other.value}, nil
}

// GreaterThan は q が other より大きいかどうかを返す。予約可否の判定などに使う。
func (q Quantity) GreaterThan(other Quantity) bool {
	return q.value > other.value
}
