package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

// placeOne は happy path で注文を 1 件作成し、その ID を返す（取消系テストの前準備）。
func placeOne(t *testing.T, f fixture) string {
	t.Helper()
	id, err := f.place.Handle(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("前準備の注文作成に失敗: %v", err)
	}
	return id.String()
}

func TestCancelOrder_EnqueuesOrderCancelledSameTx(t *testing.T) {
	ctx := context.Background()
	f := newMemoryFixture(t, &fakeReserver{})
	id := placeOne(t, f)

	if err := f.cancel.Handle(ctx, id); err != nil {
		t.Fatalf("取消に失敗: %v", err)
	}

	// 注文が Cancelled 保存されている。
	view, err := f.get.Handle(ctx, id)
	if err != nil {
		t.Fatalf("照会失敗: %v", err)
	}
	if view.Status != "cancelled" || view.Version != 2 {
		t.Fatalf("取消後の注文が不正: %+v", view)
	}

	// 保存と同一 tx で OrderCancelled が outbox に積まれている（両方存在＝原子的コミット）。
	cancels := filterByType(f.obx.Messages(), application.MessageTypeOrderCancelled)
	if len(cancels) != 1 {
		t.Fatalf("OrderCancelled 件数 = %d, want 1", len(cancels))
	}
	if ref := decodeReservationRef(t, cancels[0].Payload); ref != id {
		t.Fatalf("OrderCancelled の reservation_ref = %q, want %q", ref, id)
	}
}

func TestCancelOrder_NotConfirmed(t *testing.T) {
	ctx := context.Background()
	f := newMemoryFixture(t, &fakeReserver{})
	id := placeOne(t, f)

	if err := f.cancel.Handle(ctx, id); err != nil {
		t.Fatalf("1 回目の取消に失敗: %v", err)
	}
	// 取消済みの注文を再度取り消すと ErrOrderNotConfirmed。
	if err := f.cancel.Handle(ctx, id); !errors.Is(err, order.ErrOrderNotConfirmed) {
		t.Fatalf("2 回目の取消エラー = %v, want ErrOrderNotConfirmed", err)
	}
}

func TestGetOrder_NotFound(t *testing.T) {
	ctx := context.Background()
	f := newMemoryFixture(t, &fakeReserver{})

	if _, err := f.get.Handle(ctx, "UNKNOWN-ORDER"); !errors.Is(err, order.ErrOrderNotFound) {
		t.Fatalf("エラー = %v, want ErrOrderNotFound", err)
	}
}

func TestPlaceOrder_EmptyLinesRejected(t *testing.T) {
	ctx := context.Background()
	reserver := &fakeReserver{}
	f := newMemoryFixture(t, reserver)

	_, err := f.place.Handle(ctx, application.PlaceOrderInput{CustomerID: "CUST-1"})
	if !errors.Is(err, order.ErrEmptyOrder) {
		t.Fatalf("エラー = %v, want ErrEmptyOrder", err)
	}
	// ドメイン検証で失敗するため、在庫予約は呼ばれない。
	if reserver.reserveCalls != 0 {
		t.Fatalf("明細検証失敗時に予約が呼ばれた: %d 回", reserver.reserveCalls)
	}
}
