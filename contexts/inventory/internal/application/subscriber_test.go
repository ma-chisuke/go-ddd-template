package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/uow"
)

// newRouterFixture は予約系ユースケースと、購読ポリシを結線した outbox.Router を用意する。
func newRouterFixture(t *testing.T) (reserveFixture, *outbox.Router) {
	t.Helper()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewOutboxStore())
	f := newReserveFixture(t, work, store)

	router := outbox.NewRouter()
	router.Register(application.MessageTypeConfirmReservation, application.OnConfirmReservation(f.confirmer, testLogger()))
	router.Register(application.MessageTypeOrderCancelled, application.OnOrderCancelled(f.releaser))
	return f, router
}

func TestRouter_ConfirmAndCancelFlow(t *testing.T) {
	ctx := context.Background()
	f, router := newRouterFixture(t)

	// 在庫を用意して予約する。
	if _, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "SKU-A", Quantity: 10}); err != nil {
		t.Fatalf("補充失敗: %v", err)
	}
	if err := f.reserver.Reserve(ctx, application.ReserveInput{Ref: "ORDER-1", Lines: []application.ReserveLine{{SKU: "SKU-A", Quantity: 4}}}); err != nil {
		t.Fatalf("予約失敗: %v", err)
	}

	// 確定要求メッセージを配送 → Confirm される。
	confirmMsg := outbox.Message{
		ID:         "m-confirm",
		Type:       application.MessageTypeConfirmReservation,
		Payload:    []byte(`{"reservation_ref":"ORDER-1"}`),
		TraceID:    "trace-1",
		OccurredAt: time.Now().UTC(),
	}
	if err := router.Deliver(ctx, confirmMsg); err != nil {
		t.Fatalf("確定配送失敗: %v", err)
	}
	view, _ := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	if view.Available != 6 || view.Reserved != 4 {
		t.Fatalf("確定後の状態が不正: %+v", view)
	}

	// 取消イベントを配送 → Release される（available へ戻る）。
	cancelMsg := outbox.Message{
		ID:         "m-cancel",
		Type:       application.MessageTypeOrderCancelled,
		Payload:    []byte(`{"reservation_ref":"ORDER-1"}`),
		TraceID:    "trace-1",
		OccurredAt: time.Now().UTC(),
	}
	if err := router.Deliver(ctx, cancelMsg); err != nil {
		t.Fatalf("取消配送失敗: %v", err)
	}
	view, _ = f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	if view.Available != 10 || view.Reserved != 0 {
		t.Fatalf("取消後の状態が不正: %+v", view)
	}
}

func TestRouter_UnknownTypeReturnsErrNoRoute(t *testing.T) {
	ctx := context.Background()
	_, router := newRouterFixture(t)
	err := router.Deliver(ctx, outbox.Message{ID: "x", Type: "unknown.type"})
	if !errors.Is(err, outbox.ErrNoRoute) {
		t.Fatalf("エラー = %v, want ErrNoRoute", err)
	}
}

func TestOnConfirmReservation_BenignNoopWhenNotFound(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewOutboxStore())
	log := testLogger()
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))
	confirmer := application.NewConfirmer(exec, work, application.NewInProcessDispatcher(log), log)

	// 有効な予約が無い ref への確定要求は、良性の no-op（エラーにしない）。
	consumer := application.OnConfirmReservation(confirmer, log)
	err := consumer(ctx, outbox.Message{
		ID:      "m-1",
		Type:    application.MessageTypeConfirmReservation,
		Payload: []byte(`{"reservation_ref":"NEVER-RESERVED"}`),
		TraceID: "trace-9",
	})
	if err != nil {
		t.Fatalf("良性 no-op のはずがエラー: %v", err)
	}
}
