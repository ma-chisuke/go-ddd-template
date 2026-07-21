package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/uow"
)

// reserveFixture は予約系ユースケース一式をインメモリアダプタで組み立てる。
type reserveFixture struct {
	replenisher *application.Replenisher
	reserver    *application.Reserver
	confirmer   *application.Confirmer
	releaser    *application.Releaser
	viewer      *application.StockViewer
	captured    *[]inventory.DomainEvent
}

func newReserveFixture(t *testing.T, work application.UnitOfWork, store *memory.Store) reserveFixture {
	t.Helper()
	read := memory.NewReadStockStore(store)
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))
	log := testLogger()

	captured := &[]inventory.DomainEvent{}
	dispatcher := application.NewInProcessDispatcher(log, func(_ context.Context, e inventory.DomainEvent) {
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

func capturedNames(events []inventory.DomainEvent) map[string]int {
	m := make(map[string]int)
	for _, e := range events {
		m[e.EventName()]++
	}
	return m
}

func TestReserveConfirmRelease_MultiSKU(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewOutboxStore())
	f := newReserveFixture(t, work, store)

	// 2 つの SKU を補充する。
	for _, sku := range []string{"SKU-A", "SKU-B"} {
		if _, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: sku, Quantity: 10}); err != nil {
			t.Fatalf("補充失敗: %v", err)
		}
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
	if err != nil {
		t.Fatalf("Reserve 失敗: %v", err)
	}
	// A は available 7 / reserved 3、B は available 3 / reserved 7。
	viewA, _ := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	viewB, _ := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-B"})
	if viewA.Available != 7 || viewA.Reserved != 3 {
		t.Fatalf("A の予約後が不正: %+v", viewA)
	}
	if viewB.Available != 3 || viewB.Reserved != 7 {
		t.Fatalf("B の予約後が不正: %+v", viewB)
	}
	if got := capturedNames(*f.captured)["inventory.stock_reserved"]; got != 2 {
		t.Fatalf("StockReserved 件数 = %d, want 2", got)
	}
	*f.captured = nil

	// 確定（pending → confirmed）。available は変わらない。
	if err := f.confirmer.Confirm(ctx, "ORDER-1"); err != nil {
		t.Fatalf("Confirm 失敗: %v", err)
	}
	viewA, _ = f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	if viewA.Available != 7 || viewA.Reserved != 3 {
		t.Fatalf("A の確定後が不正: %+v", viewA)
	}
	if got := capturedNames(*f.captured)["inventory.stock_reservation_confirmed"]; got != 2 {
		t.Fatalf("StockReservationConfirmed 件数 = %d, want 2", got)
	}
	*f.captured = nil

	// 解放（confirmed を解放して available へ戻す）。
	if err := f.releaser.Release(ctx, "ORDER-1"); err != nil {
		t.Fatalf("Release 失敗: %v", err)
	}
	viewA, _ = f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	viewB, _ = f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-B"})
	if viewA.Available != 10 || viewA.Reserved != 0 {
		t.Fatalf("A の解放後が不正: %+v", viewA)
	}
	if viewB.Available != 10 || viewB.Reserved != 0 {
		t.Fatalf("B の解放後が不正: %+v", viewB)
	}
	if got := capturedNames(*f.captured)["inventory.stock_released"]; got != 2 {
		t.Fatalf("StockReleased 件数 = %d, want 2", got)
	}
}

func TestReserve_InsufficientStockIsAllOrNothing(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewOutboxStore())
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
	if !errors.Is(err, inventory.ErrInsufficientStock) {
		t.Fatalf("エラー = %v, want ErrInsufficientStock", err)
	}
	viewA, _ := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	if viewA.Available != 10 || viewA.Reserved != 0 {
		t.Fatalf("部分予約が作られた: %+v", viewA)
	}
	if len(*f.captured) != 0 {
		t.Fatalf("失敗時にイベントが配信された: %d 件", len(*f.captured))
	}
}

func TestConfirm_NotFound(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewOutboxStore())
	f := newReserveFixture(t, work, store)

	if err := f.confirmer.Confirm(ctx, "UNKNOWN"); !errors.Is(err, inventory.ErrReservationNotFound) {
		t.Fatalf("エラー = %v, want ErrReservationNotFound", err)
	}
}

func TestRelease_UnknownRefIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewOutboxStore())
	f := newReserveFixture(t, work, store)

	// 未知の ref を解放しても成功（冪等 no-op）。
	if err := f.releaser.Release(ctx, "UNKNOWN"); err != nil {
		t.Fatalf("未知 ref の Release がエラー: %v", err)
	}
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
	ctx := context.Background()
	store := memory.NewStore()
	inner := memory.NewUnitOfWork(store, memory.NewOutboxStore())
	flaky := &flakyUoW{inner: inner, failsLeft: 1}
	f := newReserveFixture(t, flaky, store)

	if _, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "SKU-A", Quantity: 10}); err != nil {
		t.Fatalf("補充失敗: %v", err)
	}

	// 1 回目は衝突を注入 → Run が再試行 → 2 回目で成功する。
	err := f.reserver.Reserve(ctx, application.ReserveInput{
		Ref:   "ORDER-1",
		Lines: []application.ReserveLine{{SKU: "SKU-A", Quantity: 4}},
	})
	if err != nil {
		t.Fatalf("再試行後も失敗: %v", err)
	}
	if flaky.failsLeft != 0 {
		t.Fatalf("衝突注入が消費されていない: failsLeft=%d", flaky.failsLeft)
	}
	view, _ := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	if view.Available != 6 || view.Reserved != 4 {
		t.Fatalf("再試行後の状態が不正: %+v", view)
	}
}

func TestReserve_GivesUpAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	inner := memory.NewUnitOfWork(store, memory.NewOutboxStore())
	log := testLogger()

	// まず非フレーキーな UoW で在庫を用意する。
	seed := application.NewReplenisher(uow.NewExecutor(uow.WithBaseBackoff(0)), inner, application.NewInProcessDispatcher(log), log)
	if _, err := seed.Replenish(ctx, application.ReplenishInput{SKU: "SKU-A", Quantity: 10}); err != nil {
		t.Fatalf("補充失敗: %v", err)
	}

	// 衝突を注入し続ける UoW で、試行回数 1 回（再試行なし）にすると衝突が表面化する。
	flaky := &flakyUoW{inner: inner, failsLeft: 5}
	exec := uow.NewExecutor(uow.WithMaxAttempts(1), uow.WithBaseBackoff(0))
	reserver := application.NewReserver(exec, flaky, application.NewInProcessDispatcher(log), log, time.Hour)

	err := reserver.Reserve(ctx, application.ReserveInput{
		Ref:   "ORDER-1",
		Lines: []application.ReserveLine{{SKU: "SKU-A", Quantity: 4}},
	})
	if !errors.Is(err, uow.ErrConcurrencyConflict) {
		t.Fatalf("エラー = %v, want ErrConcurrencyConflict", err)
	}
}
