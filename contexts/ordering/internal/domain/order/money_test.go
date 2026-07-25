package order_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

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

func TestMoney_ZeroValue(t *testing.T) {
	assert.True(t, (order.Money{}).IsZero(), "Money{} は IsZero であるべき")
}
