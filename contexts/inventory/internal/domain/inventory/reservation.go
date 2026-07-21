package inventory

import "time"

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
func ReconstituteReservation(ref ReservationRef, qty Quantity, status ReservationStatus, expiresAt time.Time) *Reservation {
	return &Reservation{ref: ref, qty: qty, status: status, expiresAt: expiresAt}
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
