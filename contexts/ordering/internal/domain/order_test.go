package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
)

// mustLine はテスト用に注文明細を組み立てるヘルパー。
func mustLine(t *testing.T, sku string, qty int, amount int64, cur string) domain.OrderLine {
	t.Helper()
	s, err := domain.NewSKU(sku)
	require.NoError(t, err, "SKU の生成に失敗しました")
	q, err := domain.NewQuantity(qty)
	require.NoError(t, err, "Quantity の生成に失敗しました")
	m, err := domain.NewMoney(amount, cur)
	require.NoError(t, err, "Money の生成に失敗しました")
	return domain.NewOrderLine(s, q, m)
}

func mustOrderID(t *testing.T, s string) domain.OrderID {
	t.Helper()
	id, err := domain.NewOrderID(s)
	require.NoError(t, err, "OrderID の生成に失敗しました")
	return id
}

func mustCustomerID(t *testing.T, s string) domain.CustomerID {
	t.Helper()
	c, err := domain.NewCustomerID(s)
	require.NoError(t, err, "CustomerID の生成に失敗しました")
	return c
}

func eventNames(events []domain.DomainEvent) map[string]int {
	m := make(map[string]int)
	for _, e := range events {
		m[e.EventName()]++
	}
	return m
}

func TestNewOrder(t *testing.T) {
	t.Parallel()

	t.Run("正常系: 明細から Confirmed の注文を作成し合計を計算する", func(t *testing.T) {
		t.Parallel()

		id := mustOrderID(t, "ORDER-1")
		lines := []domain.OrderLine{
			mustLine(t, "SKU-A", 3, 1200, "JPY"), // 小計 3600
			mustLine(t, "SKU-B", 2, 500, "JPY"),  // 小計 1000
		}
		o, err := domain.NewOrder(id, mustCustomerID(t, "CUST-1"), lines)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusConfirmed, o.Status())
		assert.Equal(t, int64(4600), o.Total().Amount())
		assert.Equal(t, "JPY", o.Total().Currency())
		// 予約参照は注文 ID から決定的に導出される。
		assert.Equal(t, id.String(), o.ReservationRef().String())
		assert.Equal(t, 0, o.Version(), "新規作成の Version は 0")
		assert.Equal(t, 1, eventNames(o.PullEvents())["ordering.order_placed"])
	})

	t.Run("異常系: 明細が空なら ErrEmptyOrder", func(t *testing.T) {
		t.Parallel()

		_, err := domain.NewOrder(mustOrderID(t, "ORDER-1"), mustCustomerID(t, "CUST-1"), nil)
		require.ErrorIs(t, err, domain.ErrEmptyOrder)
	})

	t.Run("異常系: 行間で通貨が食い違うと ErrInvalidMoney", func(t *testing.T) {
		t.Parallel()

		lines := []domain.OrderLine{
			mustLine(t, "SKU-A", 1, 1200, "JPY"),
			mustLine(t, "SKU-B", 1, 5, "USD"),
		}
		_, err := domain.NewOrder(mustOrderID(t, "ORDER-1"), mustCustomerID(t, "CUST-1"), lines)
		require.ErrorIs(t, err, domain.ErrInvalidMoney)
	})
}

func TestOrderCancel(t *testing.T) {
	t.Parallel()

	newConfirmed := func(t *testing.T) *domain.Order {
		t.Helper()
		o, err := domain.NewOrder(mustOrderID(t, "ORDER-1"), mustCustomerID(t, "CUST-1"),
			[]domain.OrderLine{mustLine(t, "SKU-A", 1, 1000, "JPY")})
		require.NoError(t, err, "注文作成に失敗しました")
		_ = o.PullEvents() // OrderPlaced を捨てる
		return o
	}

	t.Run("正常系: Confirmed から取消できて OrderCancelled を記録する", func(t *testing.T) {
		t.Parallel()

		o := newConfirmed(t)
		require.NoError(t, o.Cancel())
		assert.Equal(t, domain.StatusCancelled, o.Status())
		events := o.PullEvents()
		assert.Equal(t, 1, eventNames(events)["ordering.order_cancelled"])
		// OrderCancelled は予約参照を運ぶ（在庫解放の駆動用）。
		for _, e := range events {
			if ev, ok := e.(domain.OrderCancelled); ok {
				assert.Equal(t, o.ReservationRef().String(), ev.ReservationRef)
			}
		}
	})

	t.Run("異常系: Confirmed 以外（取消済み）の取消は ErrOrderNotConfirmed", func(t *testing.T) {
		t.Parallel()

		o := newConfirmed(t)
		require.NoError(t, o.Cancel(), "1 回目の取消に失敗しました")
		require.ErrorIs(t, o.Cancel(), domain.ErrOrderNotConfirmed)
	})
}

func TestReconstituteAndGetters(t *testing.T) {
	t.Parallel()

	id := mustOrderID(t, "ORDER-9")
	cust := mustCustomerID(t, "CUST-9")
	lines := []domain.OrderLine{mustLine(t, "SKU-A", 2, 100, "JPY")}
	total, err := domain.NewMoney(200, "JPY")
	require.NoError(t, err, "Money 生成失敗")
	ref, err := domain.NewReservationRef("REF-9")
	require.NoError(t, err, "ReservationRef 生成失敗")

	o := domain.ReconstituteOrder(id, cust, lines, domain.StatusCancelled, total, ref, 3)

	assert.Equal(t, "ORDER-9", o.ID().String())
	assert.Equal(t, "CUST-9", o.CustomerID().String())
	assert.Equal(t, domain.StatusCancelled, o.Status())
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

func TestStatusString(t *testing.T) {
	t.Parallel()

	cases := map[domain.Status]string{
		domain.StatusConfirmed: "confirmed",
		domain.StatusCancelled: "cancelled",
		domain.Status(99):      "unknown",
	}
	for s, want := range cases {
		assert.Equal(t, want, s.String(), "Status(%d).String()", int(s))
	}
}

func TestDeriveReservationRef(t *testing.T) {
	t.Parallel()

	id, err := domain.NewOrderID("ORDER-xyz")
	require.NoError(t, err, "OrderID 生成失敗")
	// 決定的: 同一注文 ID からは常に同一の予約参照が導出される。
	r1 := domain.DeriveReservationRef(id)
	r2 := domain.DeriveReservationRef(id)
	assert.Equal(t, r1.String(), r2.String(), "導出が非決定的")
	assert.Equal(t, id.String(), r1.String())
}

func TestOrder_IdentityConstruction(t *testing.T) {
	t.Parallel()

	assert.True(t, (domain.OrderID{}).IsZero(), "OrderID{} は IsZero であるべき")
	assert.True(t, (domain.ReservationRef{}).IsZero(), "ReservationRef{} は IsZero であるべき")

	_, err := domain.NewReservationRef("  ")
	require.ErrorIs(t, err, domain.ErrInvalidReservationRef)
	_, err = domain.NewOrderID("")
	require.ErrorIs(t, err, domain.ErrInvalidOrderID)
	_, err = domain.NewCustomerID("")
	require.ErrorIs(t, err, domain.ErrInvalidCustomerID)
}
