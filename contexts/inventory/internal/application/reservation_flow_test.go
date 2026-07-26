package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/shared/event"
	"github.com/example/go-ddd-template/shared/uow"
)

// reserveFixture は予約系ユースケース一式をインメモリアダプタで組み立てる。
type reserveFixture struct {
	replenisher *application.Replenisher
	reserver    *application.Reserver
	confirmer   *application.Confirmer
	releaser    *application.Releaser
	viewer      *application.StockViewer
	captured    *[]domain.DomainEvent
}

func newReserveFixture(t *testing.T, work application.UnitOfWork, store *memory.Store) reserveFixture {
	t.Helper()
	read := memory.NewReadStockStore(store)
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))
	log := testLogger()

	captured := &[]domain.DomainEvent{}
	dispatcher := event.NewTyped[domain.DomainEvent](log, func(_ context.Context, e domain.DomainEvent) {
		*captured = append(*captured, e)
	})

	return reserveFixture{
		replenisher: application.NewReplenisher(exec, work, dispatcher, log),
		reserver:    application.NewReserver(exec, work, dispatcher, log, time.Hour),
		confirmer:   application.NewConfirmer(exec, work, dispatcher, log),
		releaser:    application.NewReleaser(exec, work, dispatcher, log),
		viewer:      application.NewStockViewer(read, log),
		captured:    captured,
	}
}

func capturedNames(events []domain.DomainEvent) map[string]int {
	m := make(map[string]int)
	for _, e := range events {
		m[e.EventName()]++
	}
	return m
}

func TestReserveConfirmRelease_MultiSKU(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewStores())
	f := newReserveFixture(t, work, store)

	// 2 つの SKU を補充する。
	for _, sku := range []string{"SKU-A", "SKU-B"} {
		_, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: sku, Quantity: 10})
		require.NoError(t, err, "補充")
	}
	*f.captured = nil

	// マルチ SKU 予約（全か無か）。
	err := f.reserver.Reserve(ctx, application.ReserveInput{
		Ref: "ORDER-1",
		Lines: []application.ReserveLine{
			{SKU: "SKU-A", Quantity: 3},
			{SKU: "SKU-B", Quantity: 7},
		},
	})
	require.NoError(t, err, "Reserve")
	// A は available 7 / reserved 3、B は available 3 / reserved 7。
	viewA, _ := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	viewB, _ := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-B"})
	assert.Equal(t, 7, viewA.Available, "A 予約後 available")
	assert.Equal(t, 3, viewA.Reserved, "A 予約後 reserved")
	assert.Equal(t, 3, viewB.Available, "B 予約後 available")
	assert.Equal(t, 7, viewB.Reserved, "B 予約後 reserved")
	assert.Equal(t, 2, capturedNames(*f.captured)["inventory.stock_reserved"], "StockReserved 件数")
	*f.captured = nil

	// 確定（pending → confirmed）。available は変わらない。
	require.NoError(t, f.confirmer.Confirm(ctx, "ORDER-1"), "Confirm")
	viewA, _ = f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	assert.Equal(t, 7, viewA.Available, "A 確定後 available")
	assert.Equal(t, 3, viewA.Reserved, "A 確定後 reserved")
	assert.Equal(t, 2, capturedNames(*f.captured)["inventory.stock_reservation_confirmed"], "StockReservationConfirmed 件数")
	*f.captured = nil

	// 解放（confirmed を解放して available へ戻す）。
	require.NoError(t, f.releaser.Release(ctx, "ORDER-1"), "Release")
	viewA, _ = f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	viewB, _ = f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-B"})
	assert.Equal(t, 10, viewA.Available, "A 解放後 available")
	assert.Equal(t, 0, viewA.Reserved, "A 解放後 reserved")
	assert.Equal(t, 10, viewB.Available, "B 解放後 available")
	assert.Equal(t, 0, viewB.Reserved, "B 解放後 reserved")
	assert.Equal(t, 2, capturedNames(*f.captured)["inventory.stock_released"], "StockReleased 件数")
}

func TestReserve_InsufficientStockIsAllOrNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewStores())
	f := newReserveFixture(t, work, store)

	_, _ = f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "SKU-A", Quantity: 10})
	_, _ = f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "SKU-B", Quantity: 2})
	*f.captured = nil

	// B が不足 → 全体失敗、A も予約されない。
	err := f.reserver.Reserve(ctx, application.ReserveInput{
		Ref: "ORDER-1",
		Lines: []application.ReserveLine{
			{SKU: "SKU-A", Quantity: 3},
			{SKU: "SKU-B", Quantity: 7},
		},
	})
	require.ErrorIs(t, err, domain.ErrInsufficientStock)
	viewA, _ := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	assert.Equal(t, 10, viewA.Available, "部分予約が作られていない（available）")
	assert.Equal(t, 0, viewA.Reserved, "部分予約が作られていない（reserved）")
	assert.Empty(t, *f.captured, "失敗時にイベントは配信されない")
}

func TestConfirm_NotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewStores())
	f := newReserveFixture(t, work, store)

	require.ErrorIs(t, f.confirmer.Confirm(ctx, "UNKNOWN"), domain.ErrReservationNotFound)
}

func TestRelease_UnknownRefIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewStores())
	f := newReserveFixture(t, work, store)

	// 未知の ref を解放しても成功（冪等 no-op）。
	require.NoError(t, f.releaser.Release(ctx, "UNKNOWN"), "未知 ref の Release")
}

// flakyUoW は最初の failsLeft 回だけ ErrConcurrencyConflict を注入する UoW デコレータ。
// 楽観的排他制御の衝突と Run の再試行を決定的に再現する。
type flakyUoW struct {
	inner     application.UnitOfWork
	failsLeft int
}

func (f *flakyUoW) Within(ctx context.Context, fn func(ctx context.Context, r application.Repos) error) error {
	return f.inner.Within(ctx, func(ctx context.Context, r application.Repos) error {
		if f.failsLeft > 0 {
			f.failsLeft--
			return uow.ErrConcurrencyConflict
		}
		return fn(ctx, r)
	})
}

func TestReserve_RetriesOnConflictThenSucceeds(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := memory.NewStore()
	inner := memory.NewUnitOfWork(store, memory.NewStores())
	flaky := &flakyUoW{inner: inner, failsLeft: 1}
	f := newReserveFixture(t, flaky, store)

	_, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "SKU-A", Quantity: 10})
	require.NoError(t, err, "補充")

	// 1 回目は衝突を注入 → Run が再試行 → 2 回目で成功する。
	err = f.reserver.Reserve(ctx, application.ReserveInput{
		Ref:   "ORDER-1",
		Lines: []application.ReserveLine{{SKU: "SKU-A", Quantity: 4}},
	})
	require.NoError(t, err, "再試行後は成功する")
	assert.Zero(t, flaky.failsLeft, "衝突注入が消費されている")
	view, _ := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	assert.Equal(t, 6, view.Available, "再試行後 available")
	assert.Equal(t, 4, view.Reserved, "再試行後 reserved")
}

func TestReserve_GivesUpAfterMaxAttempts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := memory.NewStore()
	inner := memory.NewUnitOfWork(store, memory.NewStores())
	log := testLogger()

	// まず非フレーキーな UoW で在庫を用意する。
	seed := application.NewReplenisher(uow.NewExecutor(uow.WithBaseBackoff(0)), inner, event.NewTyped[domain.DomainEvent](log), log)
	_, err := seed.Replenish(ctx, application.ReplenishInput{SKU: "SKU-A", Quantity: 10})
	require.NoError(t, err, "補充")

	// 衝突を注入し続ける UoW で、試行回数 1 回（再試行なし）にすると衝突が表面化する。
	flaky := &flakyUoW{inner: inner, failsLeft: 5}
	exec := uow.NewExecutor(uow.WithMaxAttempts(1), uow.WithBaseBackoff(0))
	reserver := application.NewReserver(exec, flaky, event.NewTyped[domain.DomainEvent](log), log, time.Hour)

	err = reserver.Reserve(ctx, application.ReserveInput{
		Ref:   "ORDER-1",
		Lines: []application.ReserveLine{{SKU: "SKU-A", Quantity: 4}},
	})
	require.ErrorIs(t, err, uow.ErrConcurrencyConflict)
}
