package application_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
)

// TestMessageTypeContract は、注文側が送出する message_type が在庫側の購読ポリシが登録する
// 種別文字列と一致することを固定する（クロスコンテキストの公開契約）。これらの文字列が
// ずれると、在庫側の outbox.Router が Consumer を解決できず配送が失敗する。
func TestMessageTypeContract(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "ordering.reservation.confirm_requested", application.MessageTypeConfirmReservation,
		"ConfirmReservation の種別が契約と不一致")
	assert.Equal(t, "ordering.order.cancelled", application.MessageTypeOrderCancelled,
		"OrderCancelled の種別が契約と不一致")
}

// TestOrderCancelledPayloadContract は、注文側が生む OrderCancelled の payload が、在庫側の
// 購読ポリシがデコードする契約（reservation_ref 必須・未知フィールドは無視）に一致することを
// 検証する。payload には参考情報として order_id を添えるが、在庫側の decode（reservation_ref
// のみ）は order_id を無視できなければならない。
func TestOrderCancelledPayloadContract(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	id := placeOne(t, f)

	require.NoError(t, f.cancel.Handle(ctx, id))
	cancels := filterByType(f.stores.Queued(), application.MessageTypeOrderCancelled)
	require.Len(t, cancels, 1)

	// 在庫側と同一の decode 構造体（reservation_ref のみ）で読める。
	assert.Equal(t, id, decodeReservationRef(t, cancels[0].Payload))

	// payload には参考情報として order_id も含まれている（在庫側は無視する）。
	var full struct {
		ReservationRef string `json:"reservation_ref"`
		OrderID        string `json:"order_id"`
	}
	require.NoError(t, json.Unmarshal(cancels[0].Payload, &full))
	assert.Equal(t, id, full.OrderID)
}
