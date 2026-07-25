package inventory_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
)

func TestNewQuantity(t *testing.T) {
	t.Parallel()

	t.Run("正常系: 0 以上は生成できる", func(t *testing.T) {
		t.Parallel()

		for _, n := range []int{0, 1, 100} {
			q, err := inventory.NewQuantity(n)
			require.NoError(t, err, "NewQuantity(%d)", n)
			assert.Equal(t, n, q.Int())
		}
	})

	t.Run("異常系: 負数は ErrInvalidQuantity", func(t *testing.T) {
		t.Parallel()

		_, err := inventory.NewQuantity(-1)
		require.ErrorIs(t, err, inventory.ErrInvalidQuantity)
	})

	t.Run("正常系: IsZero と Add が数量を正しく扱う", func(t *testing.T) {
		t.Parallel()

		zero := mustQuantity(t, 0)
		assert.True(t, zero.IsZero(), "0 は IsZero であるべき")
		sum := mustQuantity(t, 3).Add(mustQuantity(t, 4))
		assert.Equal(t, 7, sum.Int(), "3 + 4")
	})
}
