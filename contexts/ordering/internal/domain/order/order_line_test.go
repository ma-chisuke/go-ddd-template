package order_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

func TestNewQuantity(t *testing.T) {
	t.Parallel()

	t.Run("正常系: 1 以上は生成できる", func(t *testing.T) {
		t.Parallel()

		for _, n := range []int{1, 2, 100} {
			q, err := order.NewQuantity(n)
			require.NoErrorf(t, err, "NewQuantity(%d)", n)
			assert.Equal(t, n, q.Int())
		}
	})

	t.Run("異常系: 0 以下は ErrInvalidQuantity（注文行数量は n >= 1）", func(t *testing.T) {
		t.Parallel()

		for _, n := range []int{0, -1} {
			_, err := order.NewQuantity(n)
			require.ErrorIsf(t, err, order.ErrInvalidQuantity, "NewQuantity(%d)", n)
		}
	})
}
