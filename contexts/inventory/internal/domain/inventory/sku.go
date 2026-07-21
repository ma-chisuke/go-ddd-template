package inventory

import (
	"fmt"
	"strings"
)

// SKU（Stock Keeping Unit）は在庫を識別する値オブジェクト。
// 不変であり、生成後は値を変更できない。空文字は許容しない。
//
// 値オブジェクトは境界づけられたコンテキストごとに独立して所有する。
// 他コンテキストと安易に共有せず、必要ならコンテキスト境界で明示的に翻訳する。
type SKU struct {
	value string
}

// NewSKU は SKU を検証して生成する。前後の空白を取り除いた結果が空なら
// ErrInvalidSKU を返す。
func NewSKU(s string) (SKU, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return SKU{}, fmt.Errorf("SKU は空にできません: %w", ErrInvalidSKU)
	}
	return SKU{value: trimmed}, nil
}

// String は SKU の文字列表現を返す。
func (s SKU) String() string {
	return s.value
}
