package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/shared/uow"
)

func mustSKU(t *testing.T, s string) domain.SKU {
	t.Helper()
	sku, err := domain.NewSKU(s)
	require.NoError(t, err, "SKU 生成")
	return sku
}

func mustQty(t *testing.T, n int) domain.Quantity {
	t.Helper()
	q, err := domain.NewQuantity(n)
	require.NoError(t, err, "Quantity 生成")
	return q
}

// seedItem は SKU を新規補充して version 1 の状態を作る。
func seedItem(t *testing.T, work *memory.UnitOfWork, sku domain.SKU, qty domain.Quantity) {
	t.Helper()
	err := work.Within(context.Background(), func(ctx context.Context, r application.Repos) error {
		item, err := domain.NewStockItem("id-"+sku.String(), sku)
		if err != nil {
			return err
		}
		if err := item.Replenish(qty); err != nil {
			return err
		}
		return r.Stock().Save(ctx, item)
	})
	require.NoError(t, err, "初期データ投入")
}

// loadItem は Within の内側で SKU を読み込み、集約を取り出す。
func loadItem(t *testing.T, work *memory.UnitOfWork, sku domain.SKU) *domain.StockItem {
	t.Helper()
	var loaded *domain.StockItem
	err := work.Within(context.Background(), func(ctx context.Context, r application.Repos) error {
		item, err := r.Stock().Load(ctx, sku)
		if err != nil {
			return err
		}
		loaded = item
		return nil // 書き込みなしでコミット
	})
	require.NoError(t, err, "読み込み")
	return loaded
}

// 楽観的排他制御の衝突を DB なしで再現する。
// 同一 SKU を 2 回読み込んで両方 version 1 の集約を得たあと、片方を保存して version 2 にし、
// もう片方（version 1 のまま）を保存しようとすると衝突する。
func TestUnitOfWork_ConcurrencyConflict(t *testing.T) {
	ctx := context.Background()
	stockRows := memory.NewStockRows()
	work := memory.NewUnitOfWork(stockRows, memory.NewStores())
	sku := mustSKU(t, "WIDGET-001")

	seedItem(t, work, sku, mustQty(t, 5)) // version 1

	first := loadItem(t, work, sku)  // version 1
	second := loadItem(t, work, sku) // version 1（stale になる予定）

	// first を保存 → version 2 になる。
	require.NoError(t, first.Replenish(mustQty(t, 3)), "Replenish")
	err := work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Stock().Save(ctx, first)
	})
	require.NoError(t, err, "first の保存")
	assert.Equal(t, 2, first.Version(), "first.Version")

	// second（version 1 のまま）を保存 → 衝突。
	require.NoError(t, second.Replenish(mustQty(t, 1)), "Replenish")
	err = work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Stock().Save(ctx, second)
	})
	require.ErrorIs(t, err, uow.ErrConcurrencyConflict)

	// 確定済みの在庫は first の結果（5 + 3 = 8）のまま。
	final := loadItem(t, work, sku)
	assert.Equal(t, 8, final.Available().Int(), "確定 available")
	assert.Equal(t, 2, final.Version(), "確定 version")
}

// エラーを返すコールバックはロールバックされ、確定データが変化しないことを確認する。
func TestUnitOfWork_RollbackOnError(t *testing.T) {
	ctx := context.Background()
	stockRows := memory.NewStockRows()
	work := memory.NewUnitOfWork(stockRows, memory.NewStores())
	sku := mustSKU(t, "GADGET-1")

	sentinel := errors.New("業務都合で中断")
	err := work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		item, _ := domain.NewStockItem("id-1", sku)
		_ = item.Replenish(mustQty(t, 99))
		if err := r.Stock().Save(ctx, item); err != nil {
			return err
		}
		return sentinel // ここで中断 → ロールバック
	})
	require.ErrorIs(t, err, sentinel)

	// ロールバックされたので在庫は存在しない。
	_, err = memory.NewReadStockStore(stockRows).Load(ctx, sku)
	require.ErrorIs(t, err, domain.ErrStockItemNotFound, "ロールバック後の読み込み")
}
