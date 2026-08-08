package application

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
)

// GetShipment は出荷照会（読み取り専用）ユースケース。
//
// 読み取りには書き込み用の作業単位（UnitOfWork）を使わず、コネクションプールに
// 直接ぶら下がる読み取り用の ShipmentStore を注入する。読み取りにトランザクション境界の
// 再試行機構は不要であり、書き込み経路と読み取り経路の責務を明確に分けるためである
// （GetOrder と同形）。
type GetShipment struct {
	read ShipmentStore
	log  *slog.Logger
}

// NewGetShipment は出荷照会ユースケースを生成する。
func NewGetShipment(read ShipmentStore, log *slog.Logger) *GetShipment {
	return &GetShipment{read: read, log: log}
}

// Handle は指定 ID の出荷の現在状態を返す。ID が不正なら domain.ErrInvalidShipmentID、
// 存在しなければ domain.ErrShipmentNotFound をそのまま伝播する。
func (uc *GetShipment) Handle(ctx context.Context, idStr string) (ShipmentView, error) {
	shipmentID, err := domain.NewShipmentID(idStr)
	if err != nil {
		return ShipmentView{}, locate("", err)
	}

	s, err := uc.read.Load(ctx, shipmentID)
	if err != nil {
		return ShipmentView{}, err
	}

	uc.log.InfoContext(ctx, "出荷を照会しました", slog.String("shipment_id", shipmentID.String()))
	return toShipmentView(s), nil
}
