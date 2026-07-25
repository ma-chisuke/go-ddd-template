package order

import (
	"strings"
)

// ReservationRef は在庫予約を識別する値オブジェクト（相関 ID）。不変であり、生成後は
// 値を変更できない。空文字は許容しない。
//
// 注文コンテキストは注文ごとにこの参照を導出（DeriveReservationRef）して在庫予約に用いる。
// 在庫コンテキストへはこの文字列表現を「不透明な相関 ID」として渡す。在庫側は注文という
// 概念を持たず、その由来（注文番号など）を解釈しない。
type ReservationRef struct {
	value string
}

// NewReservationRef は予約参照を検証して生成する。前後の空白を取り除いた結果が
// 空なら ErrInvalidReservationRef を包んだ FieldViolation を返す
// （永続化された値からの復元に使う）。
func NewReservationRef(s string) (ReservationRef, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ReservationRef{}, VReservationRef.Violated("予約参照は空にできません")
	}
	return ReservationRef{value: trimmed}, nil
}

// DeriveReservationRef は注文 ID から予約参照を **決定的に** 導出する。
//
// 同一注文に対する再試行が常に同一の予約参照を生むため、在庫側の冪等な予約
// （同一参照への再予約は no-op）と噛み合い、二重予約を避けられる。ここでは注文 ID を
// そのまま不透明な相関 ID として用いる（決定的かつ追跡しやすい）。
func DeriveReservationRef(id OrderID) ReservationRef {
	return ReservationRef{value: id.value}
}

// String は予約参照の文字列表現を返す。
func (r ReservationRef) String() string {
	return r.value
}

// IsZero はゼロ値（未設定）かどうかを返す。
func (r ReservationRef) IsZero() bool {
	return r.value == ""
}
