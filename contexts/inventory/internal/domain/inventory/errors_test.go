package inventory_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
)

// このファイルは「ドメインが自分の語彙でフィールドを名乗る」という契約を固定する。
//
// 在庫側でとくに重要なのは、NewQuantity が 0 を通す（値域が >= 0）ため、quantity: 0 が
// 値オブジェクトを通り抜けて集約／ドメインサービスで初めて弾かれる点である。その集約側の
// 規則も FieldViolation で名乗らないと、注文側と比べて「422 でフィールドが分かるときと
// 分からないときがある」体験の割れが在庫側で再現する（FR-4.7）。
//
// もうひとつは ReservationService.Allocate の明細位置（Index）。明細の走査は集約側で
// 行われるため、位置を知っているのはドメインだけである。

// requireViolation は err からドメインの FieldViolation を取り出す。
func requireViolation(t *testing.T, err error) *inventory.FieldViolation {
	t.Helper()
	var v *inventory.FieldViolation
	require.ErrorAs(t, err, &v, "FieldViolation として取り出せること")
	return v
}

func TestFieldViolation_ValueObjects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  func() error
		// want は違反が名乗るべき検証規則。Rule ごと比較するので、Field / Code / 番兵の
		// 3 つが同時に固定される。
		want inventory.Rule
	}{
		{
			name: "境界: NewQuantity(-1) は quantity を名乗る",
			err:  func() error { _, err := inventory.NewQuantity(-1); return err },
			want: inventory.VQuantity,
		},
		{
			name: "境界: NewSKU(空) は sku を名乗る",
			err:  func() error { _, err := inventory.NewSKU("  "); return err },
			want: inventory.VSKU,
		},
		{
			name: "境界: NewReservationRef(空) は reservationRef を名乗る",
			err:  func() error { _, err := inventory.NewReservationRef(""); return err },
			want: inventory.VReservationRef,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.err()
			require.ErrorIs(t, err, tc.want.Err, "番兵まで Unwrap が繋がること（規則 R-15）")
			v := requireViolation(t, err)
			assert.Equal(t, tc.want, v.Rule)
			assert.Nil(t, v.Index, "値オブジェクトの違反は位置を持たない")
		})
	}
}

// NewQuantity(0) が「有効」であること自体を固定する。これが在庫側で集約規則が必要になる理由。
func TestFieldViolation_QuantityZeroPassesTheValueObject(t *testing.T) {
	t.Parallel()

	q, err := inventory.NewQuantity(0)
	require.NoError(t, err, "在庫の Quantity は 0 を許容する（>= 0）")
	assert.True(t, q.IsZero())
}

func TestFieldViolation_AggregateRules(t *testing.T) {
	t.Parallel()

	newItem := func(t *testing.T, available int) *inventory.StockItem {
		t.Helper()
		item, err := inventory.NewStockItem("stock-1", mustSKU(t, "WIDGET-001"))
		require.NoError(t, err)
		if available > 0 {
			require.NoError(t, item.Replenish(mustQuantity(t, available)))
		}
		return item
	}

	t.Run("境界: Replenish(0) は quantity を名乗る", func(t *testing.T) {
		t.Parallel()

		err := newItem(t, 0).Replenish(mustQuantity(t, 0))
		require.ErrorIs(t, err, inventory.ErrInvalidQuantity)
		assert.Equal(t, inventory.VQuantity, requireViolation(t, err).Rule)
	})

	t.Run("境界: Reserve(空 ref) は reservationRef を名乗る", func(t *testing.T) {
		t.Parallel()

		err := newItem(t, 5).Reserve(inventory.ReservationRef{}, mustQuantity(t, 1), time.Minute)
		require.ErrorIs(t, err, inventory.ErrInvalidReservationRef)
		assert.Equal(t, inventory.VReservationRef, requireViolation(t, err).Rule)
	})

	t.Run("境界: Reserve(0) は quantity を名乗る", func(t *testing.T) {
		t.Parallel()

		err := newItem(t, 5).Reserve(mustRef(t, "ORDER-1"), mustQuantity(t, 0), time.Minute)
		require.ErrorIs(t, err, inventory.ErrInvalidQuantity)
		assert.Equal(t, inventory.VQuantity, requireViolation(t, err).Rule)
	})

	t.Run("異常系: 在庫不足の 409 は FieldViolation にしない", func(t *testing.T) {
		t.Parallel()

		err := newItem(t, 1).Reserve(mustRef(t, "ORDER-1"), mustQuantity(t, 5), time.Minute)
		require.ErrorIs(t, err, inventory.ErrInsufficientStock)
		var v *inventory.FieldViolation
		assert.False(t, errors.As(err, &v), "状態の矛盾であり入力フィールドの問題ではない（規則 R-14）")
	})
}

// ReservationService.Allocate の明細位置。アプリケーション層のループでは付与できない
// （集約側で走査しているため）ので、ドメインが Index を載せて返す。
func TestFieldViolation_AllocateCarriesLineIndex(t *testing.T) {
	t.Parallel()

	items := []*inventory.StockItem{}
	for _, sku := range []string{"SKU-A", "SKU-B", "SKU-C"} {
		item, err := inventory.NewStockItem("stock-"+sku, mustSKU(t, sku))
		require.NoError(t, err)
		require.NoError(t, item.Replenish(mustQuantity(t, 10)))
		items = append(items, item)
	}

	var svc inventory.ReservationService

	t.Run("境界: 2 行目（添字 1）が 0 なら Index=1 を載せる", func(t *testing.T) {
		t.Parallel()

		lines := []inventory.ReservationLine{
			{SKU: mustSKU(t, "SKU-A"), Quantity: mustQuantity(t, 1)},
			{SKU: mustSKU(t, "SKU-B"), Quantity: mustQuantity(t, 0)},
			{SKU: mustSKU(t, "SKU-C"), Quantity: mustQuantity(t, 1)},
		}
		err := svc.Allocate(items, mustRef(t, "ORDER-1"), lines, time.Minute)
		require.ErrorIs(t, err, inventory.ErrInvalidQuantity)

		v := requireViolation(t, err)
		assert.Equal(t, inventory.VQuantity, v.Rule)
		require.NotNil(t, v.Index, "明細位置が載っていること")
		// 壊れた実装が「常に 0」を返しても通ってしまわないよう、2 行目を壊して 1 を要求する。
		assert.Equal(t, 1, *v.Index)
	})

	t.Run("境界: 3 行目（添字 2）が 0 なら Index=2 を載せる", func(t *testing.T) {
		t.Parallel()

		lines := []inventory.ReservationLine{
			{SKU: mustSKU(t, "SKU-A"), Quantity: mustQuantity(t, 1)},
			{SKU: mustSKU(t, "SKU-B"), Quantity: mustQuantity(t, 1)},
			{SKU: mustSKU(t, "SKU-C"), Quantity: mustQuantity(t, 0)},
		}
		err := svc.Allocate(items, mustRef(t, "ORDER-1"), lines, time.Minute)
		v := requireViolation(t, err)
		require.NotNil(t, v.Index)
		assert.Equal(t, 2, *v.Index)
	})

	t.Run("境界: 参照が空なら位置は載らない（明細の問題ではない）", func(t *testing.T) {
		t.Parallel()

		lines := []inventory.ReservationLine{
			{SKU: mustSKU(t, "SKU-A"), Quantity: mustQuantity(t, 1)},
		}
		err := svc.Allocate(items, inventory.ReservationRef{}, lines, time.Minute)
		require.ErrorIs(t, err, inventory.ErrInvalidReservationRef)

		v := requireViolation(t, err)
		assert.Equal(t, inventory.VReservationRef, v.Rule)
		assert.Nil(t, v.Index, "参照は明細に帰着しないので位置を持たない")
	})

	t.Run("異常系: 在庫項目が無い場合と在庫不足は FieldViolation にしない", func(t *testing.T) {
		t.Parallel()

		var v *inventory.FieldViolation

		missing := []inventory.ReservationLine{
			{SKU: mustSKU(t, "SKU-UNKNOWN"), Quantity: mustQuantity(t, 1)},
		}
		err := svc.Allocate(items, mustRef(t, "ORDER-1"), missing, time.Minute)
		require.ErrorIs(t, err, inventory.ErrStockItemNotFound)
		assert.False(t, errors.As(err, &v), "404 系は入力フィールドの問題ではない")

		tooMany := []inventory.ReservationLine{
			{SKU: mustSKU(t, "SKU-A"), Quantity: mustQuantity(t, 999)},
		}
		err = svc.Allocate(items, mustRef(t, "ORDER-1"), tooMany, time.Minute)
		require.ErrorIs(t, err, inventory.ErrInsufficientStock)
		assert.False(t, errors.As(err, &v), "409 系は入力フィールドの問題ではない")
	})
}

func TestFieldViolation_ErrorPassesThroughWrappedMessage(t *testing.T) {
	t.Parallel()

	_, err := inventory.NewQuantity(-1)
	v := requireViolation(t, err)

	assert.Equal(t, v.Err.Error(), v.Error())
	assert.Contains(t, v.Error(), "0 以上")
	assert.Equal(t, v.Err, v.Unwrap())
}
