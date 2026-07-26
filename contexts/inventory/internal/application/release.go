package application

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/shared/uow"
)

// Releaser は予約解放（pending / confirmed を解放して available へ戻す）ユースケース。
type Releaser struct {
	exec     uow.Executor
	work     UnitOfWork
	dispatch EventDispatcher
	log      *slog.Logger
}

// NewReleaser は予約解放ユースケースを生成する。
func NewReleaser(exec uow.Executor, work UnitOfWork, dispatch EventDispatcher, log *slog.Logger) *Releaser {
	return &Releaser{exec: exec, work: work, dispatch: dispatch, log: log}
}

// Release は参照 ref の予約を解放する。Confirm と同様、LoadByReservation でその ref を
// 持つ全ての StockItem をロードし、1 つの作業単位で原子的に解放する。
//
// 有効な予約を持つ StockItem が皆無でも冪等な no-op として成功を返す（未知 / 解放済みの
// 参照に対する解放は安全）。
func (r *Releaser) Release(ctx context.Context, ref string) error {
	reservationRef, err := domain.NewReservationRef(ref)
	if err != nil {
		return locate("", err)
	}

	var events []domain.DomainEvent
	err = uow.Run(ctx, r.exec, r.work, func(ctx context.Context, repos Repos) error {
		stocks, err := repos.Stock().LoadByReservation(ctx, reservationRef)
		if err != nil {
			return err
		}
		// stocks が空でも no-op。for ループは回らず、Save も空で問題ない。
		for _, s := range stocks {
			if err := s.Release(reservationRef); err != nil {
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

	r.dispatch.Dispatch(ctx, events...)
	r.log.InfoContext(ctx, "予約を解放しました", slog.String("ref", reservationRef.String()))
	return nil
}
