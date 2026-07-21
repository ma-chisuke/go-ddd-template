package inventory

import "fmt"

// Quantity は非負の個数を表す値オブジェクト（n >= 0）。不変であり、
// 生成後は値を変更できない。ゼロ値の Quantity{} は数量 0 として有効である。
type Quantity struct {
	value int
}

// NewQuantity は非負であることを検証して Quantity を生成する。
// 負数の場合は ErrInvalidQuantity を返す。
func NewQuantity(n int) (Quantity, error) {
	if n < 0 {
		return Quantity{}, fmt.Errorf("数量は 0 以上でなければなりません（指定値: %d）: %w", n, ErrInvalidQuantity)
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
