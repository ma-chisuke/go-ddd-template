package order_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

func TestNewSKU(t *testing.T) {
	t.Run("正常系: 空白を取り除いた値で生成できる", func(t *testing.T) {
		sku, err := order.NewSKU("  WIDGET-001  ")
		require.NoError(t, err)
		assert.Equal(t, "WIDGET-001", sku.String())
	})

	t.Run("異常系: 空文字は ErrInvalidSKU", func(t *testing.T) {
		_, err := order.NewSKU("   ")
		require.ErrorIs(t, err, order.ErrInvalidSKU)
	})
}

func TestDeriveReservationRef(t *testing.T) {
	id, err := order.NewOrderID("ORDER-xyz")
	require.NoError(t, err, "OrderID 生成失敗")
	// 決定的: 同一注文 ID からは常に同一の予約参照が導出される。
	r1 := order.DeriveReservationRef(id)
	r2 := order.DeriveReservationRef(id)
	assert.Equal(t, r1.String(), r2.String(), "導出が非決定的")
	assert.Equal(t, id.String(), r1.String())
}

func TestIdentifiers_ZeroAndConstructionErrors(t *testing.T) {
	assert.True(t, (order.OrderID{}).IsZero(), "OrderID{} は IsZero であるべき")
	assert.True(t, (order.ReservationRef{}).IsZero(), "ReservationRef{} は IsZero であるべき")

	_, err := order.NewReservationRef("  ")
	require.ErrorIs(t, err, order.ErrInvalidReservationRef)
	_, err = order.NewOrderID("")
	require.ErrorIs(t, err, order.ErrInvalidOrderID)
	_, err = order.NewCustomerID("")
	require.ErrorIs(t, err, order.ErrInvalidCustomerID)
}
