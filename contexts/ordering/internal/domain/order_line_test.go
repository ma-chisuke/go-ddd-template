package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
)

func TestNewSKU(t *testing.T) {
	t.Parallel()

	t.Run("正常系: 空白を取り除いた値で生成できる", func(t *testing.T) {
		t.Parallel()

		sku, err := domain.NewSKU("  WIDGET-001  ")
		require.NoError(t, err)
		assert.Equal(t, "WIDGET-001", sku.String())
	})

	t.Run("異常系: 空文字は ErrInvalidSKU", func(t *testing.T) {
		t.Parallel()

		_, err := domain.NewSKU("   ")
		require.ErrorIs(t, err, domain.ErrInvalidSKU)
	})
}

func TestNewQuantity(t *testing.T) {
	t.Parallel()

	t.Run("正常系: 1 以上は生成できる", func(t *testing.T) {
		t.Parallel()

		for _, n := range []int{1, 2, 100} {
			q, err := domain.NewQuantity(n)
			require.NoErrorf(t, err, "NewQuantity(%d)", n)
			assert.Equal(t, n, q.Int())
		}
	})

	t.Run("異常系: 0 以下は ErrInvalidQuantity（注文行数量は n >= 1）", func(t *testing.T) {
		t.Parallel()

		for _, n := range []int{0, -1} {
			_, err := domain.NewQuantity(n)
			require.ErrorIsf(t, err, domain.ErrInvalidQuantity, "NewQuantity(%d)", n)
		}
	})
}
