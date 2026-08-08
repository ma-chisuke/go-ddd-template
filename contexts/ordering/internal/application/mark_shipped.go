package application

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/shared/uow"
)

// MarkShipped は出荷発送ユースケース。
//
// 出荷の状態遷移（preparing -> shipped）を 1 本持ち、楽観的排他制御が実際に働く経路である。
// 同一の作業単位で出荷を読み・遷移させ・保存し、コミット後に ShipmentDispatched を
// プロセス内配信する。
//
// ShipmentDispatched はアウトボックスへ積まない。在庫コンテキストは出荷を購読しないため、
// クロスコンテキストメッセージを増やさない（v1 のスコープを広げない）。
type MarkShipped struct {
	exec     uow.Executor
	work     UnitOfWork
	dispatch EventDispatcher
	log      *slog.Logger
}

// NewMarkShipped は出荷発送ユースケースを生成する。
func NewMarkShipped(exec uow.Executor, work UnitOfWork, dispatch EventDispatcher, log *slog.Logger) *MarkShipped {
	return &MarkShipped{exec: exec, work: work, dispatch: dispatch, log: log}
}

// Handle は指定した出荷を発送済みにする。
//
// エラー:
//   - 出荷 ID が不正 → domain.ErrInvalidShipmentID（422）
//   - 追跡番号が空 → domain.ErrInvalidTrackingNumber（422）
//   - 出荷が存在しない → domain.ErrShipmentNotFound（404）
//   - 出荷が preparing でない → domain.ErrShipmentNotPreparing（409）
//   - 版の衝突 → uow.ErrConcurrencyConflict（409）
func (uc *MarkShipped) Handle(ctx context.Context, idStr, trackingNumber string) error {
	shipmentID, err := domain.NewShipmentID(idStr)
	if err != nil {
		return locate("", err)
	}
	tn, err := domain.NewTrackingNumber(trackingNumber)
	if err != nil {
		return locate("", err)
	}

	// 再試行のたびに読み直すため、配信するイベントは成功した試行のものを使う。
	var shipped *domain.Shipment
	err = uow.Run(ctx, uc.exec, uc.work, func(ctx context.Context, repos Repos) error {
		s, err := repos.Shipments().Load(ctx, shipmentID)
		if err != nil {
			return err
		}
		if err := s.MarkShipped(tn); err != nil {
			return err
		}
		if err := repos.Shipments().Save(ctx, s); err != nil {
			return err
		}
		shipped = s
		return nil
	})
	if err != nil {
		return err
	}

	// コミット後にプロセス内イベント（ShipmentDispatched）を配信する。
	uc.dispatch.Dispatch(ctx, shipped.PullEvents()...)
	uc.log.InfoContext(ctx, "出荷を発送しました",
		slog.String("shipment_id", shipmentID.String()),
	)
	return nil
}
