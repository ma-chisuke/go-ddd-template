package inventory_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
)

func TestNewSKU(t *testing.T) {
	t.Parallel()

	t.Run("正常系: 空白を取り除いた値で生成できる", func(t *testing.T) {
		t.Parallel()

		sku, err := inventory.NewSKU("  WIDGET-001  ")
		require.NoError(t, err)
		assert.Equal(t, "WIDGET-001", sku.String())
	})

	t.Run("異常系: 空文字は ErrInvalidSKU", func(t *testing.T) {
		t.Parallel()

		for _, in := range []string{"", "   ", "\t"} {
			_, err := inventory.NewSKU(in)
			require.ErrorIs(t, err, inventory.ErrInvalidSKU, "NewSKU(%q)", in)
		}
	})
}
