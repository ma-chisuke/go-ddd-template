package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

func TestGetOrder_InvalidID(t *testing.T) {
	f := newMemoryFixture(t, &fakeReserver{})
	if _, err := f.get.Handle(context.Background(), "   "); !errors.Is(err, order.ErrInvalidOrderID) {
		t.Fatalf("エラー = %v, want ErrInvalidOrderID", err)
	}
}

func TestCancelOrder_InvalidID(t *testing.T) {
	f := newMemoryFixture(t, &fakeReserver{})
	if err := f.cancel.Handle(context.Background(), "   "); !errors.Is(err, order.ErrInvalidOrderID) {
		t.Fatalf("エラー = %v, want ErrInvalidOrderID", err)
	}
}

func TestPlaceOrder_InvalidLineValues(t *testing.T) {
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
			reserver := &fakeReserver{}
			f := newMemoryFixture(t, reserver)
			_, err := f.place.Handle(ctx, newInput(tc.line, tc.customer))
			if !errors.Is(err, tc.want) {
				t.Fatalf("エラー = %v, want %v", err, tc.want)
			}
			// ドメイン検証で失敗するため、在庫予約は呼ばれない。
			if reserver.reserveCalls != 0 {
				t.Fatalf("検証失敗時に予約が呼ばれた: %d 回", reserver.reserveCalls)
			}
		})
	}
}

func TestPlaceOrder_SaveFailureReleaseAlsoFails(t *testing.T) {
	ctx := context.Background()
	// 補償解放も失敗する場合、エラーはログに留めて元の保存失敗を返す。
	reserver := &fakeReserver{releaseErr: errors.New("解放も失敗")}
	store := memory.NewStore()
	obx := memory.NewOutboxStore()
	f := newFixture(t, reserver, &failingUoW{err: errors.New("DB 書き込み失敗")}, store, obx)

	if _, err := f.place.Handle(ctx, sampleInput()); err == nil {
		t.Fatalf("保存失敗が伝播していない")
	}
	if reserver.releaseCalls != 1 {
		t.Fatalf("補償解放の呼び出し回数 = %d, want 1", reserver.releaseCalls)
	}
}
