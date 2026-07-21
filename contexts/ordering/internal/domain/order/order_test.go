package order_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

// mustLine はテスト用に注文明細を組み立てるヘルパー。
func mustLine(t *testing.T, sku string, qty int, amount int64, cur string) order.OrderLine {
	t.Helper()
	s, err := order.NewSKU(sku)
	require.NoError(t, err, "SKU の生成に失敗しました")
	q, err := order.NewQuantity(qty)
	require.NoError(t, err, "Quantity の生成に失敗しました")
	m, err := order.NewMoney(amount, cur)
	require.NoError(t, err, "Money の生成に失敗しました")
	return order.NewOrderLine(s, q, m)
}

func mustOrderID(t *testing.T, s string) order.OrderID {
	t.Helper()
	id, err := order.NewOrderID(s)
	require.NoError(t, err, "OrderID の生成に失敗しました")
	return id
}

func mustCustomerID(t *testing.T, s string) order.CustomerID {
	t.Helper()
	c, err := order.NewCustomerID(s)
	require.NoError(t, err, "CustomerID の生成に失敗しました")
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
		require.NoError(t, err)
		assert.Equal(t, order.StatusConfirmed, o.Status())
		assert.Equal(t, int64(4600), o.Total().Amount())
		assert.Equal(t, "JPY", o.Total().Currency())
		// 予約参照は注文 ID から決定的に導出される。
		assert.Equal(t, id.String(), o.ReservationRef().String())
		assert.Equal(t, 0, o.Version(), "新規作成の Version は 0")
		assert.Equal(t, 1, eventNames(o.PullEvents())["ordering.order_placed"])
	})

	t.Run("異常系: 明細が空なら ErrEmptyOrder", func(t *testing.T) {
		_, err := order.NewOrder(mustOrderID(t, "ORDER-1"), mustCustomerID(t, "CUST-1"), nil)
		require.ErrorIs(t, err, order.ErrEmptyOrder)
	})

	t.Run("異常系: 行間で通貨が食い違うと ErrInvalidMoney", func(t *testing.T) {
		lines := []order.OrderLine{
			mustLine(t, "SKU-A", 1, 1200, "JPY"),
			mustLine(t, "SKU-B", 1, 5, "USD"),
		}
		_, err := order.NewOrder(mustOrderID(t, "ORDER-1"), mustCustomerID(t, "CUST-1"), lines)
		require.ErrorIs(t, err, order.ErrInvalidMoney)
	})
}

func TestOrderCancel(t *testing.T) {
	newConfirmed := func(t *testing.T) *order.Order {
		t.Helper()
		o, err := order.NewOrder(mustOrderID(t, "ORDER-1"), mustCustomerID(t, "CUST-1"),
			[]order.OrderLine{mustLine(t, "SKU-A", 1, 1000, "JPY")})
		require.NoError(t, err, "注文作成に失敗しました")
		_ = o.PullEvents() // OrderPlaced を捨てる
		return o
	}

	t.Run("正常系: Confirmed から取消できて OrderCancelled を記録する", func(t *testing.T) {
		o := newConfirmed(t)
		require.NoError(t, o.Cancel())
		assert.Equal(t, order.StatusCancelled, o.Status())
		events := o.PullEvents()
		assert.Equal(t, 1, eventNames(events)["ordering.order_cancelled"])
		// OrderCancelled は予約参照を運ぶ（在庫解放の駆動用）。
		for _, e := range events {
			if ev, ok := e.(order.OrderCancelled); ok {
				assert.Equal(t, o.ReservationRef().String(), ev.ReservationRef)
			}
		}
	})

	t.Run("異常系: Confirmed 以外（取消済み）の取消は ErrOrderNotConfirmed", func(t *testing.T) {
		o := newConfirmed(t)
		require.NoError(t, o.Cancel(), "1 回目の取消に失敗しました")
		require.ErrorIs(t, o.Cancel(), order.ErrOrderNotConfirmed)
	})
}
