package application

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/uow"
)

// Confirmer は予約確定（pending → confirmed）ユースケース。
type Confirmer struct {
	exec     uow.Executor
	work     UnitOfWork
	dispatch EventDispatcher
	log      *slog.Logger
}

// NewConfirmer は予約確定ユースケースを生成する。
func NewConfirmer(exec uow.Executor, work UnitOfWork, dispatch EventDispatcher, log *slog.Logger) *Confirmer {
	return &Confirmer{exec: exec, work: work, dispatch: dispatch, log: log}
}

// Confirm は参照 ref の予約を確定する。マルチ SKU 予約では同一 ref が複数の StockItem に
// 跨るため、LoadByReservation でその ref を持つ「全て」の StockItem をロードし、1 つの
// 作業単位で原子的に遷移させる（単一項目への部分適用は、残り SKU の pending が取り残されて
// Reaper に誤解放され、二重割当ホールを再発させるため禁止）。
//
// 有効な予約を持つ StockItem が皆無なら ErrReservationNotFound を返す。既に confirmed の
// 予約は冪等な no-op として扱われる。
func (c *Confirmer) Confirm(ctx context.Context, ref string) error {
	reservationRef, err := inventory.NewReservationRef(ref)
	if err != nil {
		return err
	}

	var events []inventory.DomainEvent
	err = uow.Run(ctx, c.exec, c.work, func(ctx context.Context, repos Repos) error {
		stocks, err := repos.Stock().LoadByReservation(ctx, reservationRef)
		if err != nil {
			return err
		}
		if len(stocks) == 0 {
			return inventory.ErrReservationNotFound
		}
		for _, s := range stocks {
			if err := s.Confirm(reservationRef); err != nil {
				return err
			}
		}
		if err := repos.Stock().Save(ctx, stocks...); err != nil {
			return err
		}
		events = collectEvents(stocks)
		return nil
	})
	if err != nil {
		return err
	}

	c.dispatch.Dispatch(ctx, events...)
	c.log.InfoContext(ctx, "予約を確定しました", slog.String("ref", reservationRef.String()))
	return nil
}
