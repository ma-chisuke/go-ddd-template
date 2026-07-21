package application

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
	"github.com/example/go-ddd-template/shared/correlation"
	"github.com/example/go-ddd-template/shared/uow"
)

// CancelOrder は注文取消（非同期解放）ユースケース。
//
// 同一の作業単位で注文を取り消して Cancelled 保存し、ドメインが raise した OrderCancelled
// を collect（PullEvents）して翻訳済み契約へ変換し、同一トランザクションでアウトボックスへ
// 積む。在庫の解放は在庫コンテキストがそのイベントを購読して **非同期** に行う（下流から
// 上流への同期呼び出しはしない）。プロセス内 subscriber は v1 に無いため post-commit の
// 配信は不要。
type CancelOrder struct {
	exec uow.Executor
	work UnitOfWork
	log  *slog.Logger
}

// NewCancelOrder は注文取消ユースケースを生成する。
func NewCancelOrder(exec uow.Executor, work UnitOfWork, log *slog.Logger) *CancelOrder {
	return &CancelOrder{exec: exec, work: work, log: log}
}

// Handle は指定 ID の注文を取り消す。ID が不正なら order.ErrInvalidOrderID、存在しなければ
// order.ErrOrderNotFound、Confirmed 以外なら order.ErrOrderNotConfirmed を返す。
func (uc *CancelOrder) Handle(ctx context.Context, idStr string) error {
	orderID, err := order.NewOrderID(idStr)
	if err != nil {
		return err
	}
	traceID := correlation.FromContextOrEmpty(ctx)

	return uow.Run(ctx, uc.exec, uc.work, func(ctx context.Context, repos Repos) error {
		o, err := repos.Orders().Load(ctx, orderID)
		if err != nil {
			return err
		}
		if err := o.Cancel(); err != nil {
			return err
		}
		if err := repos.Orders().Save(ctx, o); err != nil {
			return err
		}
		// ドメインが append した OrderCancelled を collect し、翻訳してアウトボックスへ
		// （保存と同一トランザクションで積む）。
		for _, e := range o.PullEvents() {
			msg, ok, err := toOutboxMessage(e, traceID)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if err := repos.Outbox().Enqueue(ctx, msg); err != nil {
				return err
			}
		}
		uc.log.InfoContext(ctx, "注文を取り消しました", slog.String("order_id", orderID.String()))
		return nil
	})
}
