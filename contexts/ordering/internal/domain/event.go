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

// OrderPlaced は注文が作成（place）されたときに発生するドメインイベント。
//
// v1 ではクロスコンテキストの購読者を持たず、記録や将来拡張のためのプロセス内イベント
// として扱う（在庫の予約確定は ConfirmReservation コマンドが担い、ドメインイベント経路は
// 通らない）。
type OrderPlaced struct {
	// OrderID は作成された注文の識別子（文字列表現）。
	OrderID string
	// CustomerID は注文者（顧客）の識別子。
	CustomerID string
	// ReservationRef はこの注文が在庫予約に用いる予約参照（相関 ID）。
	ReservationRef string
	// TotalAmount は注文合計金額（最小通貨単位）。
	TotalAmount int64
	// Currency は合計金額の通貨コード。
	Currency string
	// At はイベント発生時刻。
	At time.Time
}

// EventName はイベント種別名を返す。
func (OrderPlaced) EventName() string { return "ordering.order_placed" }

// OccurredAt はイベント発生時刻を返す。
func (e OrderPlaced) OccurredAt() time.Time { return e.At }

// OrderCancelled は注文が取り消されたときに発生するドメインイベント。
//
// これはクロスコンテキストイベントであり、アプリケーション層が翻訳済み契約へ変換して
// アウトボックスへ積む。在庫コンテキストがこれを購読し、予約参照を翻訳して非同期に
// 在庫を解放する。
type OrderCancelled struct {
	// OrderID は取り消された注文の識別子（文字列表現）。
	OrderID string
	// ReservationRef は解放対象の予約参照（相関 ID）。
	ReservationRef string
	// At はイベント発生時刻。
	At time.Time
}

// EventName はイベント種別名を返す。
func (OrderCancelled) EventName() string { return "ordering.order_cancelled" }

// OccurredAt はイベント発生時刻を返す。
func (e OrderCancelled) OccurredAt() time.Time { return e.At }

// ShipmentDispatched は出荷が発送されたときに発生するドメインイベント。
//
// プロセス内配信のみで、アウトボックスへは積まない。在庫コンテキストは出荷を購読しない
// ため、クロスコンテキストメッセージを増やさない（v1 のスコープを広げない）。
//
// 集約間は識別子で参照するという規則はイベントにも及ぶ。ここに載るのは注文の実体では
// なく OrderID の文字列表現である。
type ShipmentDispatched struct {
	// ShipmentID は発送された出荷の識別子（文字列表現）。
	ShipmentID string
	// OrderID は出荷対象の注文の識別子（文字列表現）。
	OrderID string
	// TrackingNumber は配送業者の追跡番号。
	TrackingNumber string
	// At はイベント発生時刻。
	At time.Time
}

// EventName はイベント種別名を返す。
func (ShipmentDispatched) EventName() string { return "ordering.shipment_dispatched" }

// OccurredAt はイベント発生時刻を返す。
func (e ShipmentDispatched) OccurredAt() time.Time { return e.At }
