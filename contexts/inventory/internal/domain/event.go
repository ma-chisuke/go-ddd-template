package domain

import "time"

// DomainEvent はこのコンテキストのドメインイベントを表すマーカーインターフェース。
//
// あえて共有モジュールのイベント型に依存させず、コンテキスト独自の型として定義する。
// これにより、ドメイン層は外部の建材（shared/event など）に一切依存しない純粋な層を保てる。
// EventName はイベント種別を表す安定した名前を返し、購読側の振り分けやログ出力に使う。
type DomainEvent interface {
	EventName() string
	// OccurredAt はイベントが発生した時刻を返す。
	OccurredAt() time.Time
}

// StockReplenished は在庫が補充されたときに発生するドメインイベント。
type StockReplenished struct {
	// SKU は補充対象の在庫識別子（文字列表現）。
	SKU string
	// QuantityAdded は今回補充された数量。
	QuantityAdded int
	// Available は補充後の利用可能在庫数。
	Available int
	// At はイベント発生時刻。
	At time.Time
}

// EventName はイベント種別名を返す。
func (StockReplenished) EventName() string { return "inventory.stock_replenished" }

// OccurredAt はイベント発生時刻を返す。
func (e StockReplenished) OccurredAt() time.Time { return e.At }

// StockReserved は在庫が仮予約（pending）されたときに発生するドメインイベント。
type StockReserved struct {
	// SKU は予約対象の在庫識別子。
	SKU string
	// ReservationRef は予約参照（相関 ID）の文字列表現。
	ReservationRef string
	// Quantity は今回予約した数量。
	Quantity int
	// Available は予約後の利用可能在庫数。
	Available int
	// At はイベント発生時刻。
	At time.Time
}

// EventName はイベント種別名を返す。
func (StockReserved) EventName() string { return "inventory.stock_reserved" }

// OccurredAt はイベント発生時刻を返す。
func (e StockReserved) OccurredAt() time.Time { return e.At }

// StockReservationConfirmed は予約が確定（pending → confirmed）されたときに発生する
// ドメインイベント。
type StockReservationConfirmed struct {
	// SKU は予約対象の在庫識別子。
	SKU string
	// ReservationRef は予約参照の文字列表現。
	ReservationRef string
	// At はイベント発生時刻。
	At time.Time
}

// EventName はイベント種別名を返す。
func (StockReservationConfirmed) EventName() string {
	return "inventory.stock_reservation_confirmed"
}

// OccurredAt はイベント発生時刻を返す。
func (e StockReservationConfirmed) OccurredAt() time.Time { return e.At }

// StockReleased は予約が解放（Release / 期限切れ Reap）されたときに発生する
// ドメインイベント。数量は available へ戻る。
type StockReleased struct {
	// SKU は対象の在庫識別子。
	SKU string
	// ReservationRef は解放された予約参照の文字列表現。
	ReservationRef string
	// Quantity は available へ戻した数量。
	Quantity int
	// Available は解放後の利用可能在庫数。
	Available int
	// At はイベント発生時刻。
	At time.Time
}

// EventName はイベント種別名を返す。
func (StockReleased) EventName() string { return "inventory.stock_released" }

// OccurredAt はイベント発生時刻を返す。
func (e StockReleased) OccurredAt() time.Time { return e.At }

// StockDepleted は利用可能在庫が 0 に到達したときに発生するドメインイベント。
// このスライスでは「発行＋ログのみ」で、クロスコンテキストの購読者は持たない
// （イベント発行の例を示しつつ、seam を過剰実装しないため）。
type StockDepleted struct {
	// SKU は在庫が尽きた在庫識別子。
	SKU string
	// At はイベント発生時刻。
	At time.Time
}

// EventName はイベント種別名を返す。
func (StockDepleted) EventName() string { return "inventory.stock_depleted" }

// OccurredAt はイベント発生時刻を返す。
func (e StockDepleted) OccurredAt() time.Time { return e.At }
