package application

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
)

// QueryStockInput は在庫照会ユースケースの入力。
type QueryStockInput struct {
	SKU string
}

// StockViewer は在庫照会（読み取り専用）ユースケース。
//
// 読み取りには書き込み用の作業単位（UnitOfWork）を使わず、コネクションプールに
// 直接ぶら下がる読み取り用の StockStore を注入する。読み取りにトランザクション境界の
// 再試行機構は不要であり、書き込み経路と読み取り経路の責務を明確に分けるためである。
type StockViewer struct {
	read StockStore
	log  *slog.Logger
}

// NewStockViewer は在庫照会ユースケースを生成する。
func NewStockViewer(read StockStore, log *slog.Logger) *StockViewer {
	return &StockViewer{read: read, log: log}
}

// QueryStock は指定 SKU の在庫状態を返す。存在しない場合は
// domain.ErrStockItemNotFound をそのまま伝播する。
func (v *StockViewer) QueryStock(ctx context.Context, in QueryStockInput) (StockResult, error) {
	sku, err := domain.NewSKU(in.SKU)
	if err != nil {
		return StockResult{}, locate("", err)
	}

	item, err := v.read.Load(ctx, sku)
	if err != nil {
		return StockResult{}, err
	}

	v.log.InfoContext(ctx, "在庫を照会しました", slog.String("sku", sku.String()))
	return StockResult{
		SKU:       item.SKU().String(),
		Available: item.Available().Int(),
		Reserved:  item.Reserved().Int(),
		Version:   item.Version(),
	}, nil
}
