package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/uow"
)

// newRouterFixture は予約系ユースケースと、購読ポリシを結線した outbox.Router を用意する。
func newRouterFixture(t *testing.T) (reserveFixture, *outbox.Router) {
	t.Helper()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewStores())
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
	_, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "SKU-A", Quantity: 10})
	require.NoError(t, err, "補充")
	require.NoError(t, f.reserver.Reserve(ctx, application.ReserveInput{Ref: "ORDER-1", Lines: []application.ReserveLine{{SKU: "SKU-A", Quantity: 4}}}), "予約")

	// 確定要求メッセージを配送 → Confirm される。
	confirmMsg := outbox.Message{
		ID:         "m-confirm",
		Type:       application.MessageTypeConfirmReservation,
		Payload:    []byte(`{"reservation_ref":"ORDER-1"}`),
		TraceID:    "trace-1",
		OccurredAt: time.Now().UTC(),
	}
	require.NoError(t, router.Deliver(ctx, confirmMsg), "確定配送")
	view, _ := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	assert.Equal(t, 6, view.Available, "確定後 available")
	assert.Equal(t, 4, view.Reserved, "確定後 reserved")

	// 取消イベントを配送 → Release される（available へ戻る）。
	cancelMsg := outbox.Message{
		ID:         "m-cancel",
		Type:       application.MessageTypeOrderCancelled,
		Payload:    []byte(`{"reservation_ref":"ORDER-1"}`),
		TraceID:    "trace-1",
		OccurredAt: time.Now().UTC(),
	}
	require.NoError(t, router.Deliver(ctx, cancelMsg), "取消配送")
	view, _ = f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-A"})
	assert.Equal(t, 10, view.Available, "取消後 available")
	assert.Equal(t, 0, view.Reserved, "取消後 reserved")
}

func TestRouter_UnknownTypeReturnsErrNoRoute(t *testing.T) {
	ctx := context.Background()
	_, router := newRouterFixture(t)
	err := router.Deliver(ctx, outbox.Message{ID: "x", Type: "unknown.type"})
	require.ErrorIs(t, err, outbox.ErrNoRoute)
}

func TestOnConfirmReservation_BenignNoopWhenNotFound(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewStores())
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
	require.NoError(t, err, "良性 no-op はエラーにしない")
}
