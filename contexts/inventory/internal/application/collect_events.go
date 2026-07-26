package application

import (
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
)

// collectEvents は複数の在庫項目に蓄積されたドメインイベントをまとめて取り出す。
// 各ユースケースは作業単位の成功後、これで集めたイベントを配信する。
func collectEvents(items []*domain.StockItem) []domain.DomainEvent {
	var events []domain.DomainEvent
	for _, it := range items {
		events = append(events, it.PullEvents()...)
	}
	return events
}
