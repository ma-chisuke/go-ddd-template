package order_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

func TestEventOccurredAt(t *testing.T) {
	o, err := order.NewOrder(mustOrderID(t, "ORDER-1"), mustCustomerID(t, "CUST-1"),
		[]order.OrderLine{mustLine(t, "SKU-A", 1, 1000, "JPY")})
	require.NoError(t, err, "注文作成失敗")
	placed := o.PullEvents()
	require.Len(t, placed, 1)
	assert.False(t, placed[0].OccurredAt().IsZero(), "OrderPlaced の OccurredAt が未設定")

	require.NoError(t, o.Cancel(), "取消失敗")
	cancelled := o.PullEvents()
	require.Len(t, cancelled, 1)
	assert.False(t, cancelled[0].OccurredAt().IsZero(), "OrderCancelled の OccurredAt が未設定")
}
