package application

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
	"github.com/example/go-ddd-template/shared/event"
)

// EventDispatcher はドメインイベントをプロセス内で配信するポート。
// ユースケースは永続化の成功後にのみ、このポートを通じてイベントを配信する。
type EventDispatcher interface {
	Dispatch(ctx context.Context, events ...order.DomainEvent)
}

// EventHandler はすべてのドメインイベントを受け取る「捕捉ハンドラ」。
// 種別を問わず全イベントに反応する（テストでの記録やログ的な用途）。
// 種別ごとに購読したい場合は InProcessDispatcher.On を使う。
type EventHandler func(ctx context.Context, event order.DomainEvent)

// InProcessDispatcher はプロセス内同期ディスパッチャの既定実装。
//
// 2 つの配信経路を束ねる:
//   - handlers（捕捉ハンドラ）… 種別を問わず全イベントへ渡す。
//   - inner（shared/event 機構）… 種別名ごとに登録されたハンドラへ渡す。
//
// ドメインの order.DomainEvent は EventName() を持つため、shared/event.Event を
// 構造的に満たす。ここで両者を適合させることで、ドメイン層を shared/event に依存させずに
// （純粋なドメインを保ちつつ）汎用の配信機構へ載せられる。
//
// このディスパッチャが扱うのは「プロセス内のみ」のイベント（v1 の OrderPlaced）である。
// クロスコンテキストイベント（OrderCancelled）は、これとは別に、ユースケースが同一 UoW 内で
// 翻訳済み契約へ変換してアウトボックスへ積む（[messages.go] 参照）。
type InProcessDispatcher struct {
	log      *slog.Logger
	handlers []EventHandler
	inner    *event.InProcess
}

// NewInProcessDispatcher はディスパッチャを生成する。
func NewInProcessDispatcher(log *slog.Logger, handlers ...EventHandler) *InProcessDispatcher {
	return &InProcessDispatcher{log: log, handlers: handlers, inner: event.NewInProcess()}
}

// On は shared/event 機構に、種別名ごとの購読ハンドラを登録する。
func (d *InProcessDispatcher) On(name string, h event.Handler) {
	d.inner.Register(name, h)
}

// Dispatch は各イベントを配信の事実とともに構造化ログに記録し、捕捉ハンドラと
// shared/event 機構（種別名で登録されたハンドラ）の双方へ届ける。
//
// これは永続化成功後に呼ばれる「後処理」なので、ハンドラのエラーは呼び出し元へ返さず
// ログに残すに留める（コミット済みのトランザクションは巻き戻せないため）。
func (d *InProcessDispatcher) Dispatch(ctx context.Context, events ...order.DomainEvent) {
	for _, e := range events {
		d.log.InfoContext(ctx, "ドメインイベントを配信しました",
			slog.String("event", e.EventName()),
			slog.Time("occurred_at", e.OccurredAt()),
		)
		for _, h := range d.handlers {
			h(ctx, e)
		}
	}

	// order.DomainEvent は shared/event.Event を構造的に満たすため、そのまま適合できる。
	adapted := make([]event.Event, len(events))
	for i, e := range events {
		adapted[i] = e
	}
	if err := d.inner.Dispatch(ctx, adapted...); err != nil {
		d.log.WarnContext(ctx, "イベントハンドラがエラーを返しました", "error", err)
	}
}
