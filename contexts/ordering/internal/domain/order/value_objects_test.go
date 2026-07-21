package order_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

func TestNewQuantity(t *testing.T) {
	t.Run("正常系: 1 以上は生成できる", func(t *testing.T) {
		for _, n := range []int{1, 2, 100} {
			q, err := order.NewQuantity(n)
			require.NoErrorf(t, err, "NewQuantity(%d)", n)
			assert.Equal(t, n, q.Int())
		}
	})

	t.Run("異常系: 0 以下は ErrInvalidQuantity（注文行数量は n >= 1）", func(t *testing.T) {
		for _, n := range []int{0, -1} {
			_, err := order.NewQuantity(n)
			require.ErrorIsf(t, err, order.ErrInvalidQuantity, "NewQuantity(%d)", n)
		}
	})
}

func TestNewMoney(t *testing.T) {
	t.Run("正常系: 非負金額と非空通貨で生成できる", func(t *testing.T) {
		m, err := order.NewMoney(0, "JPY")
		require.NoError(t, err)
		assert.Equal(t, int64(0), m.Amount())
		assert.Equal(t, "JPY", m.Currency())
	})

	t.Run("異常系: 負数は ErrInvalidMoney", func(t *testing.T) {
		_, err := order.NewMoney(-1, "JPY")
		require.ErrorIs(t, err, order.ErrInvalidMoney)
	})

	t.Run("異常系: 空通貨は ErrInvalidMoney", func(t *testing.T) {
		_, err := order.NewMoney(100, "  ")
		require.ErrorIs(t, err, order.ErrInvalidMoney)
	})

	t.Run("Add: ゼロ値は加法の単位元", func(t *testing.T) {
		a, _ := order.NewMoney(300, "JPY")
		sum, err := (order.Money{}).Add(a)
		require.NoError(t, err)
		assert.Equal(t, int64(300), sum.Amount())
		assert.Equal(t, "JPY", sum.Currency())
	})

	t.Run("Add: 通貨不一致は ErrInvalidMoney", func(t *testing.T) {
		a, _ := order.NewMoney(300, "JPY")
		b, _ := order.NewMoney(5, "USD")
		_, err := a.Add(b)
		require.ErrorIs(t, err, order.ErrInvalidMoney)
	})

	t.Run("Mul: 単価 × 数量", func(t *testing.T) {
		a, _ := order.NewMoney(1200, "JPY")
		got := a.Mul(3)
		assert.Equal(t, int64(3600), got.Amount())
		assert.Equal(t, "JPY", got.Currency())
	})
}

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
