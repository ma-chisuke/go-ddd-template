package application

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

// GetOrder は注文照会（読み取り専用）ユースケース。
//
// 読み取りには書き込み用の作業単位（UnitOfWork）を使わず、コネクションプールに
// 直接ぶら下がる読み取り用の OrderStore を注入する。読み取りにトランザクション境界の
// 再試行機構は不要であり、書き込み経路と読み取り経路の責務を明確に分けるためである。
type GetOrder struct {
	read OrderStore
	log  *slog.Logger
}

// NewGetOrder は注文照会ユースケースを生成する。
func NewGetOrder(read OrderStore, log *slog.Logger) *GetOrder {
	return &GetOrder{read: read, log: log}
}

// Handle は指定 ID の注文の現在状態を返す。ID が不正なら order.ErrInvalidOrderID、
// 存在しなければ order.ErrOrderNotFound をそのまま伝播する。
func (uc *GetOrder) Handle(ctx context.Context, idStr string) (OrderView, error) {
	orderID, err := order.NewOrderID(idStr)
	if err != nil {
		return OrderView{}, locate("", err)
	}

	o, err := uc.read.Load(ctx, orderID)
	if err != nil {
		return OrderView{}, err
	}

	uc.log.InfoContext(ctx, "注文を照会しました", slog.String("order_id", orderID.String()))
	return toOrderView(o), nil
}
