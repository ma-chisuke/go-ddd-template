package application

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/shared/id"
	"github.com/example/go-ddd-template/shared/uow"
)

// PrepareShipment は出荷準備ユースケース。
//
// **1 つのトランザクションで書き込む集約ルートは 1 つに保つ。** 注文はトランザクションの
// **外**で読み、内では出荷だけを書く。「注文が確定済みか」は出荷の不変条件ではなく事前条件
// だからである。不変条件なら同一トランザクションで読んで固定する必要があるが、事前条件なら
// 検査時点の値で足りる。2 つの集約を同一トランザクションへ巻き込まないことが
// 「集約 = 整合性の境界」の実演になる。
//
// 競合の受容: 注文を読んだ後・出荷を保存する前に注文が取り消されると、取消済みの注文に
// 対する出荷が生まれうる。これは**設計上受け入れる**（結果整合）。業務的な打ち消しが
// 必要なら OrderCancelled を購読して出荷を取り消す設計になるが、それは v1 のスコープ外である。
// 集約を分けるとは、この競合を受け入れることである。
//
// 冪等性: このユースケースは冪等ではない（呼ぶたびに新しい出荷 ID を採番する）。
// Order の DeriveReservationRef のような決定的導出は行わない（v1 の割り切り）。
type PrepareShipment struct {
	exec uow.Executor
	work UnitOfWork
	read OrderStore
	log  *slog.Logger
}

// NewPrepareShipment は出荷準備ユースケースを生成する。
// read は注文をトランザクションの外で読むための読み取り専用ストアである。
func NewPrepareShipment(exec uow.Executor, work UnitOfWork, read OrderStore, log *slog.Logger) *PrepareShipment {
	return &PrepareShipment{exec: exec, work: work, read: read, log: log}
}

// Handle は指定した注文に対する出荷を準備し、作成された出荷の ID を返す。
//
// エラー:
//   - 注文 ID が不正 → domain.ErrInvalidOrderID（422）
//   - 注文が存在しない → domain.ErrOrderNotFound（404）
//   - 注文が confirmed でない → ErrOrderNotConfirmedForShipment（409）
func (uc *PrepareShipment) Handle(ctx context.Context, orderIDStr string) (domain.ShipmentID, error) {
	orderID, err := domain.NewOrderID(orderIDStr)
	if err != nil {
		return domain.ShipmentID{}, locate("", err)
	}

	// [トランザクションの外] 事前条件の検査。ここで読んだ注文は書き込まない。
	o, err := uc.read.Load(ctx, orderID)
	if err != nil {
		return domain.ShipmentID{}, err
	}
	if o.Status() != domain.OrderStatusConfirmed {
		return domain.ShipmentID{}, fmt.Errorf("注文 %q: %w", orderID.String(), ErrOrderNotConfirmedForShipment)
	}

	// 採番はアプリケーション層が行う。ここで失敗したらリクエスターの入力ではなく
	// サーバ側の問題なので、locate で「入力検証エラー」に見せかけてはならない。
	shipmentID, err := domain.NewShipmentID(id.New())
	if err != nil {
		return domain.ShipmentID{}, err
	}
	s := domain.NewShipment(shipmentID, orderID)

	// [トランザクションの内] 書くのは出荷だけである。
	if err := uow.Run(ctx, uc.exec, uc.work, func(ctx context.Context, repos Repos) error {
		return repos.Shipments().Save(ctx, s)
	}); err != nil {
		return domain.ShipmentID{}, err
	}

	uc.log.InfoContext(ctx, "出荷を準備しました",
		slog.String("shipment_id", shipmentID.String()),
		slog.String("order_id", orderID.String()),
	)
	return shipmentID, nil
}
