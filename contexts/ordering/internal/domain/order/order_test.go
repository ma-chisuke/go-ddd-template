package order_test

import (
	"errors"
	"testing"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

// mustLine はテスト用に注文明細を組み立てるヘルパー。
func mustLine(t *testing.T, sku string, qty int, amount int64, cur string) order.OrderLine {
	t.Helper()
	s, err := order.NewSKU(sku)
	if err != nil {
		t.Fatalf("SKU の生成に失敗しました: %v", err)
	}
	q, err := order.NewQuantity(qty)
	if err != nil {
		t.Fatalf("Quantity の生成に失敗しました: %v", err)
	}
	m, err := order.NewMoney(amount, cur)
	if err != nil {
		t.Fatalf("Money の生成に失敗しました: %v", err)
	}
	return order.NewOrderLine(s, q, m)
}

func mustOrderID(t *testing.T, s string) order.OrderID {
	t.Helper()
	id, err := order.NewOrderID(s)
	if err != nil {
		t.Fatalf("OrderID の生成に失敗しました: %v", err)
	}
	return id
}

func mustCustomerID(t *testing.T, s string) order.CustomerID {
	t.Helper()
	c, err := order.NewCustomerID(s)
	if err != nil {
		t.Fatalf("CustomerID の生成に失敗しました: %v", err)
	}
	return c
}

func eventNames(events []order.DomainEvent) map[string]int {
	m := make(map[string]int)
	for _, e := range events {
		m[e.EventName()]++
	}
	return m
}

func TestNewOrder(t *testing.T) {
	t.Run("正常系: 明細から Confirmed の注文を作成し合計を計算する", func(t *testing.T) {
		id := mustOrderID(t, "ORDER-1")
		lines := []order.OrderLine{
			mustLine(t, "SKU-A", 3, 1200, "JPY"), // 小計 3600
			mustLine(t, "SKU-B", 2, 500, "JPY"),  // 小計 1000
		}
		o, err := order.NewOrder(id, mustCustomerID(t, "CUST-1"), lines)
		if err != nil {
			t.Fatalf("想定外のエラー: %v", err)
		}
		if o.Status() != order.StatusConfirmed {
			t.Fatalf("Status = %v, want Confirmed", o.Status())
		}
		if o.Total().Amount() != 4600 || o.Total().Currency() != "JPY" {
			t.Fatalf("Total = %+v, want 4600 JPY", o.Total())
		}
		// 予約参照は注文 ID から決定的に導出される。
		if o.ReservationRef().String() != id.String() {
			t.Fatalf("ReservationRef = %q, want %q", o.ReservationRef().String(), id.String())
		}
		if o.Version() != 0 {
			t.Fatalf("新規作成の Version = %d, want 0", o.Version())
		}
		if got := eventNames(o.PullEvents())["ordering.order_placed"]; got != 1 {
			t.Fatalf("OrderPlaced 件数 = %d, want 1", got)
		}
	})

	t.Run("異常系: 明細が空なら ErrEmptyOrder", func(t *testing.T) {
		_, err := order.NewOrder(mustOrderID(t, "ORDER-1"), mustCustomerID(t, "CUST-1"), nil)
		if !errors.Is(err, order.ErrEmptyOrder) {
			t.Fatalf("エラー = %v, want ErrEmptyOrder", err)
		}
	})

	t.Run("異常系: 行間で通貨が食い違うと ErrInvalidMoney", func(t *testing.T) {
		lines := []order.OrderLine{
			mustLine(t, "SKU-A", 1, 1200, "JPY"),
			mustLine(t, "SKU-B", 1, 5, "USD"),
		}
		_, err := order.NewOrder(mustOrderID(t, "ORDER-1"), mustCustomerID(t, "CUST-1"), lines)
		if !errors.Is(err, order.ErrInvalidMoney) {
			t.Fatalf("エラー = %v, want ErrInvalidMoney", err)
		}
	})
}

func TestOrderCancel(t *testing.T) {
	newConfirmed := func(t *testing.T) *order.Order {
		t.Helper()
		o, err := order.NewOrder(mustOrderID(t, "ORDER-1"), mustCustomerID(t, "CUST-1"),
			[]order.OrderLine{mustLine(t, "SKU-A", 1, 1000, "JPY")})
		if err != nil {
			t.Fatalf("注文作成に失敗しました: %v", err)
		}
		_ = o.PullEvents() // OrderPlaced を捨てる
		return o
	}

	t.Run("正常系: Confirmed から取消できて OrderCancelled を記録する", func(t *testing.T) {
		o := newConfirmed(t)
		if err := o.Cancel(); err != nil {
			t.Fatalf("想定外のエラー: %v", err)
		}
		if o.Status() != order.StatusCancelled {
			t.Fatalf("Status = %v, want Cancelled", o.Status())
		}
		events := o.PullEvents()
		if got := eventNames(events)["ordering.order_cancelled"]; got != 1 {
			t.Fatalf("OrderCancelled 件数 = %d, want 1", got)
		}
		// OrderCancelled は予約参照を運ぶ（在庫解放の駆動用）。
		for _, e := range events {
			if ev, ok := e.(order.OrderCancelled); ok {
				if ev.ReservationRef != o.ReservationRef().String() {
					t.Fatalf("OrderCancelled.ReservationRef = %q, want %q", ev.ReservationRef, o.ReservationRef().String())
				}
			}
		}
	})

	t.Run("異常系: Confirmed 以外（取消済み）の取消は ErrOrderNotConfirmed", func(t *testing.T) {
		o := newConfirmed(t)
		if err := o.Cancel(); err != nil {
			t.Fatalf("1 回目の取消に失敗しました: %v", err)
		}
		if err := o.Cancel(); !errors.Is(err, order.ErrOrderNotConfirmed) {
			t.Fatalf("2 回目の取消エラー = %v, want ErrOrderNotConfirmed", err)
		}
	})
}
