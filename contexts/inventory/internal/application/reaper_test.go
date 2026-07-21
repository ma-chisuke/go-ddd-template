package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/testutil"
	"github.com/example/go-ddd-template/shared/uow"
)

func TestReaper_ReleasesOnlyExpiredPending(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewOutboxStore())
	log := testLogger()
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))

	captured := &[]inventory.DomainEvent{}
	dispatcher := application.NewInProcessDispatcher(log, func(_ context.Context, e inventory.DomainEvent) {
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

	if _, err := replenisher.Replenish(ctx, application.ReplenishInput{SKU: "SKU-A", Quantity: 100}); err != nil {
		t.Fatalf("補充失敗: %v", err)
	}
	// 期限切れになる pending 予約と、確定して Reap されない予約を作る。
	if err := reserver.Reserve(ctx, application.ReserveInput{Ref: "PENDING", Lines: []application.ReserveLine{{SKU: "SKU-A", Quantity: 10}}}); err != nil {
		t.Fatalf("pending 予約失敗: %v", err)
	}
	if err := reserver.Reserve(ctx, application.ReserveInput{Ref: "CONFIRMED", Lines: []application.ReserveLine{{SKU: "SKU-A", Quantity: 20}}}); err != nil {
		t.Fatalf("confirmed 予約失敗: %v", err)
	}
	if err := confirmer.Confirm(ctx, "CONFIRMED"); err != nil {
		t.Fatalf("Confirm 失敗: %v", err)
	}
	*captured = nil

	// 掃除を実行する。期限切れ pending（PENDING）だけが解放される。
	if err := reaper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep 失敗: %v", err)
	}

	view, _ := viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	// PENDING(10) が戻る。available = 100 - 20(confirmed) = 80、reserved = 20。
	if view.Available != 80 || view.Reserved != 20 {
		t.Fatalf("Reap 後の状態が不正: %+v", view)
	}
	if got := capturedNames(*captured)["inventory.stock_released"]; got != 1 {
		t.Fatalf("StockReleased 件数 = %d, want 1", got)
	}

	// 2 回目の掃除では解放対象が無い（イベントも出ない）。
	*captured = nil
	if err := reaper.Sweep(ctx); err != nil {
		t.Fatalf("2 回目 Sweep 失敗: %v", err)
	}
	if len(*captured) != 0 {
		t.Fatalf("2 回目の掃除でイベントが出た: %d 件", len(*captured))
	}
}

func TestReaper_SweepNoopWhenClockBeforeExpiry(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewOutboxStore())
	log := testLogger()
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))
	dispatcher := application.NewInProcessDispatcher(log)

	replenisher := application.NewReplenisher(exec, work, dispatcher, log)
	reserver := application.NewReserver(exec, work, dispatcher, log, time.Hour)
	viewer := application.NewStockViewer(memory.NewReadStockStore(store), log)

	// 擬似時計を「実時刻より前」に置く → いかなる予約も未期限。
	clock := testutil.NewClock(time.Now().Add(-time.Hour))
	reaper := application.NewReaper(exec, work, dispatcher, clock, log, 100)

	_, _ = replenisher.Replenish(ctx, application.ReplenishInput{SKU: "SKU-A", Quantity: 100})
	_ = reserver.Reserve(ctx, application.ReserveInput{Ref: "PENDING", Lines: []application.ReserveLine{{SKU: "SKU-A", Quantity: 10}}})

	if err := reaper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep 失敗: %v", err)
	}
	view, _ := viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	if view.Reserved != 10 {
		t.Fatalf("未期限の予約が解放された: %+v", view)
	}
}
