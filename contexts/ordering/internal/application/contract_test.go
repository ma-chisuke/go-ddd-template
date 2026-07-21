package application_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
)

// TestMessageTypeContract は、注文側が送出する message_type が在庫側の購読ポリシが登録する
// 種別文字列と一致することを固定する（クロスコンテキストの公開契約）。これらの文字列が
// ずれると、在庫側の outbox.Router が Consumer を解決できず配送が失敗する。
func TestMessageTypeContract(t *testing.T) {
	if application.MessageTypeConfirmReservation != "ordering.reservation.confirm_requested" {
		t.Fatalf("ConfirmReservation の種別 = %q, 契約と不一致", application.MessageTypeConfirmReservation)
	}
	if application.MessageTypeOrderCancelled != "ordering.order.cancelled" {
		t.Fatalf("OrderCancelled の種別 = %q, 契約と不一致", application.MessageTypeOrderCancelled)
	}
}

// TestOrderCancelledPayloadContract は、注文側が生む OrderCancelled の payload が、在庫側の
// 購読ポリシがデコードする契約（reservation_ref 必須・未知フィールドは無視）に一致することを
// 検証する。payload には参考情報として order_id を添えるが、在庫側の decode（reservation_ref
// のみ）は order_id を無視できなければならない。
func TestOrderCancelledPayloadContract(t *testing.T) {
	ctx := context.Background()
	f := newMemoryFixture(t, &fakeReserver{})
	id := placeOne(t, f)

	if err := f.cancel.Handle(ctx, id); err != nil {
		t.Fatalf("取消に失敗: %v", err)
	}
	cancels := filterByType(f.obx.Messages(), application.MessageTypeOrderCancelled)
	if len(cancels) != 1 {
		t.Fatalf("OrderCancelled 件数 = %d, want 1", len(cancels))
	}

	// 在庫側と同一の decode 構造体（reservation_ref のみ）で読める。
	if ref := decodeReservationRef(t, cancels[0].Payload); ref != id {
		t.Fatalf("reservation_ref = %q, want %q", ref, id)
	}

	// payload には参考情報として order_id も含まれている（在庫側は無視する）。
	var full struct {
		ReservationRef string `json:"reservation_ref"`
		OrderID        string `json:"order_id"`
	}
	if err := json.Unmarshal(cancels[0].Payload, &full); err != nil {
		t.Fatalf("payload のデコードに失敗: %v", err)
	}
	if full.OrderID != id {
		t.Fatalf("order_id = %q, want %q", full.OrderID, id)
	}
}
