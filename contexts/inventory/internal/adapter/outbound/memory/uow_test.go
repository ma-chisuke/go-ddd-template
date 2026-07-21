package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/uow"
)

func mustSKU(t *testing.T, s string) inventory.SKU {
	t.Helper()
	sku, err := inventory.NewSKU(s)
	if err != nil {
		t.Fatalf("SKU 生成失敗: %v", err)
	}
	return sku
}

func mustQty(t *testing.T, n int) inventory.Quantity {
	t.Helper()
	q, err := inventory.NewQuantity(n)
	if err != nil {
		t.Fatalf("Quantity 生成失敗: %v", err)
	}
	return q
}

// seedItem は SKU を新規補充して version 1 の状態を作る。
func seedItem(t *testing.T, work *memory.UnitOfWork, sku inventory.SKU, qty inventory.Quantity) {
	t.Helper()
	err := work.Within(context.Background(), func(ctx context.Context, r application.Repos) error {
		item, err := inventory.NewStockItem("id-"+sku.String(), sku)
		if err != nil {
			return err
		}
		if err := item.Replenish(qty); err != nil {
			return err
		}
		return r.Stock().Save(ctx, item)
	})
	if err != nil {
		t.Fatalf("初期データ投入に失敗: %v", err)
	}
}

// loadItem は Within の内側で SKU を読み込み、集約を取り出す。
func loadItem(t *testing.T, work *memory.UnitOfWork, sku inventory.SKU) *inventory.StockItem {
	t.Helper()
	var loaded *inventory.StockItem
	err := work.Within(context.Background(), func(ctx context.Context, r application.Repos) error {
		item, err := r.Stock().Load(ctx, sku)
		if err != nil {
			return err
		}
		loaded = item
		return nil // 書き込みなしでコミット
	})
	if err != nil {
		t.Fatalf("読み込みに失敗: %v", err)
	}
	return loaded
}

// 楽観的排他制御の衝突を DB なしで再現する。
// 同一 SKU を 2 回読み込んで両方 version 1 の集約を得たあと、片方を保存して version 2 にし、
// もう片方（version 1 のまま）を保存しようとすると衝突する。
func TestUnitOfWork_ConcurrencyConflict(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewOutboxStore())
	sku := mustSKU(t, "WIDGET-001")

	seedItem(t, work, sku, mustQty(t, 5)) // version 1

	first := loadItem(t, work, sku)  // version 1
	second := loadItem(t, work, sku) // version 1（stale になる予定）

	// first を保存 → version 2 になる。
	if err := first.Replenish(mustQty(t, 3)); err != nil {
		t.Fatalf("Replenish 失敗: %v", err)
	}
	err := work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Stock().Save(ctx, first)
	})
	if err != nil {
		t.Fatalf("first の保存に失敗: %v", err)
	}
	if first.Version() != 2 {
		t.Fatalf("first.Version = %d, want 2", first.Version())
	}

	// second（version 1 のまま）を保存 → 衝突。
	if err := second.Replenish(mustQty(t, 1)); err != nil {
		t.Fatalf("Replenish 失敗: %v", err)
	}
	err = work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Stock().Save(ctx, second)
	})
	if !errors.Is(err, uow.ErrConcurrencyConflict) {
		t.Fatalf("エラー = %v, want ErrConcurrencyConflict", err)
	}

	// 確定済みの在庫は first の結果（5 + 3 = 8）のまま。
	final := loadItem(t, work, sku)
	if final.Available().Int() != 8 || final.Version() != 2 {
		t.Fatalf("確定状態が不正: available=%d version=%d", final.Available().Int(), final.Version())
	}
}

// エラーを返すコールバックはロールバックされ、確定データが変化しないことを確認する。
func TestUnitOfWork_RollbackOnError(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewOutboxStore())
	sku := mustSKU(t, "GADGET-1")

	sentinel := errors.New("業務都合で中断")
	err := work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		item, _ := inventory.NewStockItem("id-1", sku)
		_ = item.Replenish(mustQty(t, 99))
		if err := r.Stock().Save(ctx, item); err != nil {
			return err
		}
		return sentinel // ここで中断 → ロールバック
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("エラー = %v, want sentinel", err)
	}

	// ロールバックされたので在庫は存在しない。
	_, err = memory.NewReadStockStore(store).Load(ctx, sku)
	if !errors.Is(err, inventory.ErrStockItemNotFound) {
		t.Fatalf("ロールバック後の読み込み = %v, want ErrStockItemNotFound", err)
	}
}
