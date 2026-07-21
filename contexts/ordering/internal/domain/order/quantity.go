package order

import "fmt"

// Quantity は注文行の数量を表す値オブジェクト。不変であり、生成後は値を変更できない。
//
// 重要（コンテキストごと VO の教材ポイント）: 注文コンテキストの Quantity は
// **1 以上（n >= 1）** を値域とする。注文行に数量 0 は無いからである。これは在庫
// コンテキストの Quantity（利用可能在庫 available を扱うため n >= 0 を許容する）とは
// 意図的に異なる制約であり、各コンテキストが自分のドメイン規則に合わせて Quantity を
// 独立に定義することを示す。同名でも別コンテキストの別型であり、共有しない。腐敗防止層が
// 境界でこれらを翻訳する。
type Quantity struct {
	value int
}

// NewQuantity は 1 以上であることを検証して Quantity を生成する。
// 0 以下の場合は ErrInvalidQuantity を返す。
func NewQuantity(n int) (Quantity, error) {
	if n < 1 {
		return Quantity{}, fmt.Errorf("注文行の数量は 1 以上でなければなりません（指定値: %d）: %w", n, ErrInvalidQuantity)
	}
	return Quantity{value: n}, nil
}

// Int は数量を int で返す。
func (q Quantity) Int() int {
	return q.value
}
