package application

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/shared/uow"
)

// MarkShipped は出荷を発送済みにするユースケース。
//
// 同一の作業単位で出荷を読み込み、状態を遷移させて保存する。トランザクションの内側で
// 触れる集約ルートは Shipment ただ 1 つである。
//
// **トランザクションの内側で Load するのは楽観的排他制御のためである。** uow.Run は
// ErrConcurrencyConflict を検知して指数バックオフで再試行し、再試行のたびに Within が
// 新しいトランザクションを開くので、Load からやり直される。だから Shipment には
// Version() / MarkPersisted() が要る（AggregateRoot 契約の 2 つはこのために在る）。
type MarkShipped struct {
	exec     uow.Executor
	work     UnitOfWork
	dispatch EventDispatcher
	log      *slog.Logger
}

// NewMarkShipped は発送済み化ユースケースを生成する。
func NewMarkShipped(exec uow.Executor, work UnitOfWork, dispatch EventDispatcher, log *slog.Logger) *MarkShipped {
	return &MarkShipped{exec: exec, work: work, dispatch: dispatch, log: log}
}

// Handle は指定 ID の出荷を発送済みにする。成功すると出荷の現在状態を返す。
//
// エラー:
//   - 出荷 ID が不正 → domain.ErrInvalidShipmentID（422）。
//   - 追跡番号が空 → domain.ErrInvalidTrackingNumber（422）。
//   - 出荷が存在しない → domain.ErrShipmentNotFound（404）。
//   - 出荷が preparing でない → domain.ErrShipmentNotPreparing（409）。
func (uc *MarkShipped) Handle(ctx context.Context, idStr, trackingStr string) (ShipmentView, error) {
	shipmentID, err := domain.NewShipmentID(idStr)
	if err != nil {
		return ShipmentView{}, locate("", err)
	}
	tracking, err := domain.NewTrackingNumber(trackingStr)
	if err != nil {
		return ShipmentView{}, locate("", err)
	}

	var shipped *domain.Shipment
	err = uow.Run(ctx, uc.exec, uc.work, func(ctx context.Context, repos Repos) error {
		sh, err := repos.Shipments().Load(ctx, shipmentID)
		if err != nil {
			return err
		}
		if err := sh.MarkShipped(tracking); err != nil {
			return err
		}
		if err := repos.Shipments().Save(ctx, sh); err != nil {
			return err
		}
		shipped = sh
		return nil
	})
	if err != nil {
		return ShipmentView{}, err
	}

	// コミット後にプロセス内イベント（ShipmentDispatched）を配信する。
	uc.dispatch.Dispatch(ctx, shipped.PullEvents()...)
	uc.log.InfoContext(ctx, "出荷を発送済みにしました",
		slog.String("shipment_id", shipmentID.String()),
		slog.String("tracking_number", tracking.String()),
	)
	return toShipmentView(shipped), nil
}
