package order_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

func TestReconstituteAndGetters(t *testing.T) {
	id := mustOrderID(t, "ORDER-9")
	cust := mustCustomerID(t, "CUST-9")
	lines := []order.OrderLine{mustLine(t, "SKU-A", 2, 100, "JPY")}
	total, err := order.NewMoney(200, "JPY")
	require.NoError(t, err, "Money 生成失敗")
	ref, err := order.NewReservationRef("REF-9")
	require.NoError(t, err, "ReservationRef 生成失敗")

	o := order.ReconstituteOrder(id, cust, lines, order.StatusCancelled, total, ref, 3)

	assert.Equal(t, "ORDER-9", o.ID().String())
	assert.Equal(t, "CUST-9", o.CustomerID().String())
	assert.Equal(t, order.StatusCancelled, o.Status())
	assert.Equal(t, 3, o.Version())
	assert.Equal(t, "REF-9", o.ReservationRef().String())
	got := o.Lines()
	require.Len(t, got, 1)
	l := got[0]
	assert.Equal(t, "SKU-A", l.SKU().String())
	assert.Equal(t, 2, l.Quantity().Int())
	assert.Equal(t, int64(100), l.UnitPrice().Amount())
	assert.Equal(t, int64(200), l.Subtotal().Amount())

	// 復元ではドメインイベントを発生させない。
	assert.Empty(t, o.PullEvents(), "復元でイベントが発生した")

	// MarkPersisted はバージョンを同期する。
	o.MarkPersisted(4)
	assert.Equal(t, 4, o.Version(), "MarkPersisted 後の Version")
}

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

func TestStatusString(t *testing.T) {
	cases := map[order.Status]string{
		order.StatusConfirmed: "confirmed",
		order.StatusCancelled: "cancelled",
		order.Status(99):      "unknown",
	}
	for s, want := range cases {
		assert.Equal(t, want, s.String(), "Status(%d).String()", int(s))
	}
}

func TestValueObjectZeroAndErrors(t *testing.T) {
	assert.True(t, (order.OrderID{}).IsZero(), "OrderID{} は IsZero であるべき")
	assert.True(t, (order.ReservationRef{}).IsZero(), "ReservationRef{} は IsZero であるべき")
	assert.True(t, (order.Money{}).IsZero(), "Money{} は IsZero であるべき")

	_, err := order.NewReservationRef("  ")
	require.ErrorIs(t, err, order.ErrInvalidReservationRef)
	_, err = order.NewOrderID("")
	require.ErrorIs(t, err, order.ErrInvalidOrderID)
	_, err = order.NewCustomerID("")
	require.ErrorIs(t, err, order.ErrInvalidCustomerID)
}
