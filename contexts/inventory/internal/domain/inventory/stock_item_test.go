package inventory_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
)

// mustSKU はテスト用に SKU を生成するヘルパー。生成に失敗したらテストを止める。
func mustSKU(t *testing.T, s string) inventory.SKU {
	t.Helper()
	sku, err := inventory.NewSKU(s)
	require.NoError(t, err, "SKU の生成")
	return sku
}

// mustQuantity はテスト用に Quantity を生成するヘルパー。
func mustQuantity(t *testing.T, n int) inventory.Quantity {
	t.Helper()
	q, err := inventory.NewQuantity(n)
	require.NoError(t, err, "Quantity の生成")
	return q
}

func TestNewSKU(t *testing.T) {
	t.Run("正常系: 空白を取り除いた値で生成できる", func(t *testing.T) {
		sku, err := inventory.NewSKU("  WIDGET-001  ")
		require.NoError(t, err)
		assert.Equal(t, "WIDGET-001", sku.String())
	})

	t.Run("異常系: 空文字は ErrInvalidSKU", func(t *testing.T) {
		for _, in := range []string{"", "   ", "\t"} {
			_, err := inventory.NewSKU(in)
			require.ErrorIs(t, err, inventory.ErrInvalidSKU, "NewSKU(%q)", in)
		}
	})
}

func TestNewQuantity(t *testing.T) {
	t.Run("正常系: 0 以上は生成できる", func(t *testing.T) {
		for _, n := range []int{0, 1, 100} {
			q, err := inventory.NewQuantity(n)
			require.NoError(t, err, "NewQuantity(%d)", n)
			assert.Equal(t, n, q.Int())
		}
	})

	t.Run("異常系: 負数は ErrInvalidQuantity", func(t *testing.T) {
		_, err := inventory.NewQuantity(-1)
		require.ErrorIs(t, err, inventory.ErrInvalidQuantity)
	})

	t.Run("IsZero と Add", func(t *testing.T) {
		zero := mustQuantity(t, 0)
		assert.True(t, zero.IsZero(), "0 は IsZero であるべき")
		sum := mustQuantity(t, 3).Add(mustQuantity(t, 4))
		assert.Equal(t, 7, sum.Int(), "3 + 4")
	})
}

func TestNewStockItem(t *testing.T) {
	t.Run("正常系: 利用可能 0・version 0 で始まる", func(t *testing.T) {
		item, err := inventory.NewStockItem("id-1", mustSKU(t, "WIDGET-001"))
		require.NoError(t, err)
		assert.Equal(t, 0, item.Available().Int(), "Available")
		assert.Equal(t, 0, item.Version(), "Version")
		assert.Equal(t, 0, item.Reserved().Int(), "Reserved")
		assert.Equal(t, "id-1", item.ID(), "ID")
	})

	t.Run("異常系: 空 id は不正", func(t *testing.T) {
		_, err := inventory.NewStockItem("", mustSKU(t, "WIDGET-001"))
		require.Error(t, err, "空 id はエラーになるべき")
	})
}

func TestStockItem_Replenish(t *testing.T) {
	t.Run("正常系: 利用可能在庫が増え、イベントが記録される", func(t *testing.T) {
		item, _ := inventory.NewStockItem("id-1", mustSKU(t, "WIDGET-001"))

		require.NoError(t, item.Replenish(mustQuantity(t, 10)))
		require.NoError(t, item.Replenish(mustQuantity(t, 5)))
		assert.Equal(t, 15, item.Available().Int(), "Available")

		events := item.PullEvents()
		require.Len(t, events, 2, "イベント数")
		first, ok := events[0].(inventory.StockReplenished)
		require.True(t, ok, "イベント型は StockReplenished")
		assert.Equal(t, "inventory.stock_replenished", first.EventName())
		assert.Equal(t, 10, first.QuantityAdded)
		assert.Equal(t, 10, first.Available)
		assert.False(t, first.OccurredAt().IsZero(), "OccurredAt が設定されている")

		// PullEvents 後は空になる。
		assert.Empty(t, item.PullEvents(), "PullEvents 後は空")
	})

	t.Run("異常系: 補充数量 0 は ErrInvalidQuantity", func(t *testing.T) {
		item, _ := inventory.NewStockItem("id-1", mustSKU(t, "WIDGET-001"))
		require.ErrorIs(t, item.Replenish(mustQuantity(t, 0)), inventory.ErrInvalidQuantity)
		// 失敗時はイベントも記録されない。
		assert.Empty(t, item.PullEvents(), "失敗時にイベントは記録されない")
	})
}

func TestReconstituteAndMarkPersisted(t *testing.T) {
	item := inventory.ReconstituteStockItem("id-9", mustSKU(t, "GADGET-9"), mustQuantity(t, 42), 3, nil)
	assert.Equal(t, 3, item.Version(), "復元 version")
	assert.Equal(t, 42, item.Available().Int(), "復元 available")
	item.MarkPersisted(4)
	assert.Equal(t, 4, item.Version(), "MarkPersisted 後の Version")
	// 復元では未発火イベントは無い。
	assert.Empty(t, item.PullEvents(), "復元直後のイベントは無い")
}
