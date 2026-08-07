// 予約エンティティと、その状態・参照。
//
// 束ね方の根拠は CONVENTIONS.md の B-3 則 1（型はそれが属する概念のファイルに置く）である。
// ReservationRef がここに在るのは「文字列を包む識別子」という技術的な種類のためではなく、
// 予約を名指す参照であり、予約を読むときに必ず一緒に読むからである。
//
// 値オブジェクトは境界づけられたコンテキストごとに独立して所有する。他コンテキストの
// 同名の型とは別の型であり、安易に共有しない。必要ならコンテキスト境界で明示的に翻訳する。

package domain

import (
	"strings"
	"time"
)

// ReservationStatus は予約の状態を表す。予約は二相（reserve → confirm）で扱う。
type ReservationStatus int

const (
	// ReservationPending は仮予約（未確定）。TTL を持ち、期限切れになると Reaper が解放する。
	ReservationPending ReservationStatus = iota
	// ReservationConfirmed は確定済み予約。TTL を持たず、Reaper の対象にならない。
	ReservationConfirmed
)

// String は状態の文字列表現を返す（永続化・ログ用）。
func (s ReservationStatus) String() string {
	switch s {
	case ReservationPending:
		return "pending"
	case ReservationConfirmed:
		return "confirmed"
	default:
		return "unknown"
	}
}

// Reservation は 1 つの在庫項目（StockItem）に対する予約を表すエンティティ。
// 集約 StockItem の内部に保持され、集約ルート経由でのみ生成・遷移する。
//
// 状態遷移: pending → confirmed（Confirm）、あるいは解放（Release / 期限切れ Reap）。
// confirmed は Reap の対象にならない。expiresAt は pending のときのみ意味を持つ。
//
// マルチ SKU 予約では、同一の ReservationRef が複数の StockItem に跨り、関与する各
// StockItem がそれぞれ独立に Reservation を保持する（reserved が StockItem ごとの
// 導出値であることと整合する）。
type Reservation struct {
	ref       ReservationRef
	qty       Quantity
	status    ReservationStatus
	expiresAt time.Time // pending のときのみ有効。confirmed ではゼロ値。
}

// ReconstituteReservation は永続化された状態から予約を復元する。
// 送信アダプタ（リポジトリ）が保存済みの行から集約を再構築する際に用いる。
//
// 値を返すのは、Reservation が集約ルートではない（StockItem の子エンティティである）
// からである。集約の外へ子の実体を指すポインタを出さないことで「集約の操作は集約ルート
// のみを通じて行われる」を検査ではなく型で保証する（検査 12）。復元した予約は必ず
// ReconstituteStockItem でルートに束ねられ、そこで初めて集約の内部でポインタになる。
func ReconstituteReservation(ref ReservationRef, qty Quantity, status ReservationStatus, expiresAt time.Time) Reservation {
	return Reservation{ref: ref, qty: qty, status: status, expiresAt: expiresAt}
}

// Ref は予約参照を返す。
func (r *Reservation) Ref() ReservationRef { return r.ref }

// Quantity は予約数量を返す。
func (r *Reservation) Quantity() Quantity { return r.qty }

// Status は予約状態を返す。
func (r *Reservation) Status() ReservationStatus { return r.status }

// ExpiresAt は失効時刻を返す（pending のときのみ有効。confirmed ではゼロ値）。
func (r *Reservation) ExpiresAt() time.Time { return r.expiresAt }

// isExpired は now 時点で失効しているかどうかを返す（pending 判定は呼び出し側で行う）。
func (r *Reservation) isExpired(now time.Time) bool {
	return !r.expiresAt.IsZero() && !now.Before(r.expiresAt)
}

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
