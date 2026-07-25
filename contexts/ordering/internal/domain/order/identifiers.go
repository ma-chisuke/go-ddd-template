// 識別子の族。文字列を包んで検証するだけの値オブジェクトを 1 ファイルに集めている。
//
// 束ね方の根拠は CONVENTIONS.md の B-3 則 1（識別子は族でまとめる）である。4 つとも
// 「文字列を trim して空を弾き、String() で取り出す」という同じ形をしており、
// 常に一緒に読む。1 型 1 ファイルに割ると 26〜45 行のファイルが 4 つ並ぶだけで、
// 読み手が得るものが無い。
//
// 値オブジェクトは境界づけられたコンテキストごとに独立して所有する。他コンテキストへは
// これらの内部型をそのまま渡さず、境界で文字列などの翻訳済み表現へ変換する。

package order

import (
	"strings"
)

// OrderID は注文を識別する値オブジェクト。不変であり、生成後は値を変更できない。
// 空文字は許容しない。採番そのもの（ID 生成）はアプリケーション層が行い、ドメインは
// 与えられた文字列を検証して包むだけである。
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
