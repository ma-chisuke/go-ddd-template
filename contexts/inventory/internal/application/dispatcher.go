package application

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
)

// EventDispatcher はドメインイベントをプロセス内で配信するポート。
// ユースケースは永続化の成功後にのみ、このポートを通じてイベントを配信する。
type EventDispatcher interface {
	Dispatch(ctx context.Context, events ...inventory.DomainEvent)
}

// EventHandler はドメインイベントを受け取る購読ハンドラ。
type EventHandler func(ctx context.Context, event inventory.DomainEvent)

// InProcessDispatcher はプロセス内同期ディスパッチャの既定実装。
// 登録されたハンドラへ順番にイベントを渡す。
//
// このスライスでは購読側はログ出力のみで、外部への非同期配信（トランザクショナル
// アウトボックスなど）は行わない。より強い配信保証が必要になった段階で、
// アウトボックス方式の実装へ差し替えられるよう、ポート（EventDispatcher）を挟んでいる。
type InProcessDispatcher struct {
	log      *slog.Logger
	handlers []EventHandler
}

// NewInProcessDispatcher はディスパッチャを生成する。
func NewInProcessDispatcher(log *slog.Logger, handlers ...EventHandler) *InProcessDispatcher {
	return &InProcessDispatcher{log: log, handlers: handlers}
}

// Dispatch は各イベントを全ハンドラへ配信し、配信の事実を構造化ログに記録する。
func (d *InProcessDispatcher) Dispatch(ctx context.Context, events ...inventory.DomainEvent) {
	for _, event := range events {
		d.log.InfoContext(ctx, "ドメインイベントを配信しました",
			slog.String("event", event.EventName()),
			slog.Time("occurred_at", event.OccurredAt()),
		)
		for _, h := range d.handlers {
			h(ctx, event)
		}
	}
}
