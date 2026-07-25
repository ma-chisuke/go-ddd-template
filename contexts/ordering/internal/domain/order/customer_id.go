package order

import (
	"strings"
)

// CustomerID は注文者（顧客）を識別する値オブジェクト。不変であり、生成後は値を
// 変更できない。空文字は許容しない。
type CustomerID struct {
	value string
}

// NewCustomerID は顧客 ID を検証して生成する。前後の空白を取り除いた結果が空なら
// ErrInvalidCustomerID を包んだ FieldViolation を返す。
func NewCustomerID(s string) (CustomerID, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return CustomerID{}, VCustomerID.Violated("顧客 ID は空にできません")
	}
	return CustomerID{value: trimmed}, nil
}

// String は顧客 ID の文字列表現を返す。
func (c CustomerID) String() string {
	return c.value
}
