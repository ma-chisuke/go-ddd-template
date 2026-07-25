package order

import (
	"strings"
)

// SKU（Stock Keeping Unit）は注文明細が指す商品を識別する値オブジェクト。
// 不変であり、生成後は値を変更できない。空文字は許容しない。
//
// 重要（コンテキストごと VO の教材ポイント）: この SKU は「注文」コンテキストが独自に
// 所有する型であり、在庫コンテキストの SKU とは別の型である。同名でも共有しない。
// 境界を跨ぐときは腐敗防止層が文字列などの翻訳済み表現へ変換する。
type SKU struct {
	value string
}

// NewSKU は SKU を検証して生成する。前後の空白を取り除いた結果が空なら
// ErrInvalidSKU を包んだ FieldViolation を返す。
func NewSKU(s string) (SKU, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return SKU{}, VSKU.Violated("SKU は空にできません")
	}
	return SKU{value: trimmed}, nil
}

// String は SKU の文字列表現を返す。
func (s SKU) String() string {
	return s.value
}
