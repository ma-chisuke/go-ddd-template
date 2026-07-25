package order

import (
	"strings"
)

// OrderID は注文を識別する値オブジェクト。不変であり、生成後は値を変更できない。
// 空文字は許容しない。採番そのもの（ID 生成）はアプリケーション層が行い、ドメインは
// 与えられた文字列を検証して包むだけである。
//
// 値オブジェクトは境界づけられたコンテキストごとに独立して所有する。他コンテキストへは
// この内部型をそのまま渡さず、境界で文字列などの翻訳済み表現へ変換する。
type OrderID struct {
	value string
}

// NewOrderID は注文 ID を検証して生成する。前後の空白を取り除いた結果が空なら
// ErrInvalidOrderID を包んだ FieldViolation を返す。
func NewOrderID(s string) (OrderID, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return OrderID{}, VOrderID.Violated("注文 ID は空にできません")
	}
	return OrderID{value: trimmed}, nil
}

// String は注文 ID の文字列表現を返す。
func (o OrderID) String() string {
	return o.value
}

// IsZero はゼロ値（未設定）かどうかを返す。
func (o OrderID) IsZero() bool {
	return o.value == ""
}
