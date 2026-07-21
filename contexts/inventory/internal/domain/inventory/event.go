package inventory

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
// このスライスで扱う唯一のイベントである。
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
