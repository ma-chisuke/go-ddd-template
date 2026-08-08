package memory_test

import (
	"context"
	"errors"
	"sync"
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

// マルチ SKU の確定が 1 回のロックで行われることを検証する。
//
// 書き手は 3 つの SKU を 1 つのトランザクションで更新し、読み手は同時に 3 つをまとめて読む。
// 確定が行ごとのロックへ崩れると、読み手は「一部の SKU だけ新しい版」という確定の途中の
// 状態を観測できてしまう。全か無かの予約はこの不可分性を前提にしているため、単一行しか
// 書かない集約のテストでは退行を検出できない。
//
// 「観測されない」ことを主張する検査なので、判定器の校正が要る。applyGroup を行ごとの
// ロックへ一時的に崩すとこのテストが落ちることを確認してある（崩した版では torn が
// 数百件になる）。
func TestUnitOfWork_MultiSKUCommitIsAtomic(t *testing.T) {
	ctx := context.Background()
	stockRows := memory.NewStockRows()
	work := memory.NewUnitOfWork(stockRows, memory.NewStores())
	read := memory.NewReadStockStore(stockRows)

	skus := []domain.SKU{mustSKU(t, "ATOM-1"), mustSKU(t, "ATOM-2"), mustSKU(t, "ATOM-3")}
	for _, sku := range skus {
		seedItem(t, work, sku, mustQty(t, 1)) // version 1
	}
	one := mustQty(t, 1)

	const rounds = 300

	var (
		wg       sync.WaitGroup
		writeErr error
		readErr  error
		torn     int // 版が食い違う状態を観測した回数
		done     = make(chan struct{})
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for range rounds {
			writeErr = work.Within(ctx, func(ctx context.Context, r application.Repos) error {
				items, err := r.Stock().LoadMany(ctx, skus)
				if err != nil {
					return err
				}
				for _, item := range items {
					if err := item.Replenish(one); err != nil {
						return err
					}
				}
				return r.Stock().Save(ctx, items...)
			})
			if writeErr != nil {
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			items, err := read.LoadMany(ctx, skus)
			if err != nil {
				readErr = err
				return
			}
			for _, item := range items {
				if item.Version() != items[0].Version() {
					torn++
				}
			}
		}
	}()

	wg.Wait()
	require.NoError(t, writeErr, "書き手")
	require.NoError(t, readErr, "読み手")
	assert.Zero(t, torn, "確定の途中（SKU ごとに版が食い違う状態）を観測してはならない")

	final, err := read.LoadMany(ctx, skus)
	require.NoError(t, err, "最終読み込み")
	require.Len(t, final, len(skus), "最終件数")
	for _, item := range final {
		assert.Equal(t, 1+rounds, item.Version(), "全 SKU が同じ版まで進む")
	}
}
