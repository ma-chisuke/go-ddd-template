package inventory

import (
	"strings"
)

// ReservationRef は予約を識別する値オブジェクト（相関 ID）。不変であり、生成後は
// 値を変更できない。空文字は許容しない。
//
// 重要な境界規則として、在庫コンテキストは「注文（Order）」という概念を持たない。
// ReservationRef は呼び出し側が供給する「不透明な相関 ID」であり、在庫側はその由来
// （注文番号など）を解釈しない。他コンテキストの識別子（OrderID など）は、境界を跨ぐ
// 腐敗防止層（anti-corruption layer）がこの ReservationRef へ翻訳する。
//
// 値オブジェクトは境界づけられたコンテキストごとに独立して所有する。他コンテキストの
// 同名の型とは別の型であり、安易に共有しない。
type ReservationRef struct {
	value string
}

// NewReservationRef は予約参照を検証して生成する。前後の空白を取り除いた結果が
// 空なら ErrInvalidReservationRef を包んだ FieldViolation を返す。
func NewReservationRef(s string) (ReservationRef, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ReservationRef{}, VReservationRef.Violated("予約参照は空にできません")
	}
	return ReservationRef{value: trimmed}, nil
}

// String は予約参照の文字列表現を返す。
func (r ReservationRef) String() string {
	return r.value
}

// IsZero はゼロ値（未設定）かどうかを返す。
func (r ReservationRef) IsZero() bool {
	return r.value == ""
}
