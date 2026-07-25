package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/event"
	"github.com/example/go-ddd-template/shared/testutil"
	"github.com/example/go-ddd-template/shared/uow"
)

func TestReaper_ReleasesOnlyExpiredPending(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewStores())
	log := testLogger()
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))

	captured := &[]inventory.DomainEvent{}
	dispatcher := event.NewTyped[inventory.DomainEvent](log, func(_ context.Context, e inventory.DomainEvent) {
		*captured = append(*captured, e)
	})

	replenisher := application.NewReplenisher(exec, work, dispatcher, log)
	// TTL は 1 時間。あとで擬似時計を 2 時間進めて期限切れにする。
	reserver := application.NewReserver(exec, work, dispatcher, log, time.Hour)
	confirmer := application.NewConfirmer(exec, work, dispatcher, log)
	viewer := application.NewStockViewer(memory.NewReadStockStore(store), log)

	// 擬似時計。予約はこのテスト実行時の実時刻 + TTL で失効時刻が入るため、
	// 擬似時計を「実時刻より十分未来」に置いて確実に期限切れにする。
	clock := testutil.NewClock(time.Now().Add(2 * time.Hour))
	reaper := application.NewReaper(exec, work, dispatcher, clock, log, 100)

	_, err := replenisher.Replenish(ctx, application.ReplenishInput{SKU: "SKU-A", Quantity: 100})
	require.NoError(t, err, "補充")
	// 期限切れになる pending 予約と、確定して Reap されない予約を作る。
	require.NoError(t, reserver.Reserve(ctx, application.ReserveInput{Ref: "PENDING", Lines: []application.ReserveLine{{SKU: "SKU-A", Quantity: 10}}}), "pending 予約")
	require.NoError(t, reserver.Reserve(ctx, application.ReserveInput{Ref: "CONFIRMED", Lines: []application.ReserveLine{{SKU: "SKU-A", Quantity: 20}}}), "confirmed 予約")
	require.NoError(t, confirmer.Confirm(ctx, "CONFIRMED"), "Confirm")
	*captured = nil

	// 掃除を実行する。期限切れ pending（PENDING）だけが解放される。
	require.NoError(t, reaper.Sweep(ctx), "Sweep")

	view, _ := viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	// PENDING(10) が戻る。available = 100 - 20(confirmed) = 80、reserved = 20。
	assert.Equal(t, 80, view.Available, "Reap 後 available")
	assert.Equal(t, 20, view.Reserved, "Reap 後 reserved")
	assert.Equal(t, 1, capturedNames(*captured)["inventory.stock_released"], "StockReleased 件数")

	// 2 回目の掃除では解放対象が無い（イベントも出ない）。
	*captured = nil
	require.NoError(t, reaper.Sweep(ctx), "2 回目 Sweep")
	assert.Empty(t, *captured, "2 回目の掃除ではイベントが出ない")
}

func TestReaper_SweepNoopWhenClockBeforeExpiry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewStores())
	log := testLogger()
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))
	dispatcher := event.NewTyped[inventory.DomainEvent](log)

	replenisher := application.NewReplenisher(exec, work, dispatcher, log)
	reserver := application.NewReserver(exec, work, dispatcher, log, time.Hour)
	viewer := application.NewStockViewer(memory.NewReadStockStore(store), log)

	// 擬似時計を「実時刻より前」に置く → いかなる予約も未期限。
	clock := testutil.NewClock(time.Now().Add(-time.Hour))
	reaper := application.NewReaper(exec, work, dispatcher, clock, log, 100)

	_, _ = replenisher.Replenish(ctx, application.ReplenishInput{SKU: "SKU-A", Quantity: 100})
	_ = reserver.Reserve(ctx, application.ReserveInput{Ref: "PENDING", Lines: []application.ReserveLine{{SKU: "SKU-A", Quantity: 10}}})

	require.NoError(t, reaper.Sweep(ctx), "Sweep")
	view, _ := viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	assert.Equal(t, 10, view.Reserved, "未期限の予約は解放されない")
}
