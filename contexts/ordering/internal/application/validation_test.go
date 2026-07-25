package application_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

// これらは入力検証（境界での値検証）が、在庫予約より前に失敗して番兵を返すことを検証する。
// 検証で弾かれる経路では在庫予約は呼ばれない。reserver に EXPECT を置かないので、もし
// Reserve が呼ばれれば gomock がテストを失敗させる（＝「予約は呼ばれない」を検証している）。

func TestGetOrder_InvalidID(t *testing.T) {
	t.Parallel()

	f := newMemFixture(t)
	_, err := f.get.Handle(context.Background(), "   ")
	require.ErrorIs(t, err, order.ErrInvalidOrderID)
}

func TestCancelOrder_InvalidID(t *testing.T) {
	t.Parallel()

	f := newMemFixture(t)
	err := f.cancel.Handle(context.Background(), "   ")
	require.ErrorIs(t, err, order.ErrInvalidOrderID)
}

func TestPlaceOrder_InvalidLineValues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	newInput := func(l application.PlaceOrderLine, customer string) application.PlaceOrderInput {
		return application.PlaceOrderInput{CustomerID: customer, Lines: []application.PlaceOrderLine{l}}
	}

	cases := []struct {
		name     string
		customer string
		line     application.PlaceOrderLine
		want     error
	}{
		{"数量 0", "CUST-1", application.PlaceOrderLine{SKU: "SKU-A", Quantity: 0, UnitPriceAmount: 100, Currency: "JPY"}, order.ErrInvalidQuantity},
		{"SKU 空", "CUST-1", application.PlaceOrderLine{SKU: "  ", Quantity: 1, UnitPriceAmount: 100, Currency: "JPY"}, order.ErrInvalidSKU},
		{"通貨空", "CUST-1", application.PlaceOrderLine{SKU: "SKU-A", Quantity: 1, UnitPriceAmount: 100, Currency: ""}, order.ErrInvalidMoney},
		{"顧客空", "  ", application.PlaceOrderLine{SKU: "SKU-A", Quantity: 1, UnitPriceAmount: 100, Currency: "JPY"}, order.ErrInvalidCustomerID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newMemFixture(t)
			_, err := f.place.Handle(ctx, newInput(tc.line, tc.customer))
			require.ErrorIs(t, err, tc.want)
		})
	}
}
