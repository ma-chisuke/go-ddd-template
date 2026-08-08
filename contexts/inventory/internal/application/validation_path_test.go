package application_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
)

// このファイルは「アプリケーション層が入力 DTO 上の位置を付与する」ことを固定する
// （FR-4.5）。在庫側で難しいのは、数量 0 の判定が集約側（ReservationService.Allocate）で
// 起きるため、位置がドメインの違反（Index）から来る経路が別に存在することである。

// requireSingle は err からアプリケーション層の ValidationError を取り出し、
// 違反がちょうど 1 件であることを確認して返す。
func requireSingle(t *testing.T, err error) application.FieldViolation {
	t.Helper()
	var ve *application.ValidationError
	require.ErrorAs(t, err, &ve, "ValidationError として取り出せること")
	require.Len(t, ve.Violations, 1)
	return ve.Violations[0]
}

// newReserveOnlyFixture は予約系ユースケース一式をインメモリで組み立てる（在庫は空）。
func newReserveOnlyFixture(t *testing.T) reserveFixture {
	t.Helper()
	stockRows := memory.NewStockRows()
	work := memory.NewUnitOfWork(stockRows, memory.NewStores())
	return newReserveFixture(t, work, stockRows)
}

func TestValidationPath_SingleValueUseCases(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cases := []struct {
		name     string
		call     func(f reserveFixture) error
		wantPath string
		wantCode string
		wantErr  error
	}{
		{
			name: "境界: Replenish は空の SKU を Sku として指す",
			call: func(f reserveFixture) error {
				_, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: " ", Quantity: 1})
				return err
			},
			wantPath: "Sku",
			wantCode: domain.VSKU.Code,
			wantErr:  domain.ErrInvalidSKU,
		},
		{
			name: "境界: Replenish は負の数量を Quantity として指す（値オブジェクトで弾かれる）",
			call: func(f reserveFixture) error {
				_, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "SKU-A", Quantity: -1})
				return err
			},
			wantPath: "Quantity",
			wantCode: domain.VQuantity.Code,
			wantErr:  domain.ErrInvalidQuantity,
		},
		{
			name: "境界: Replenish は 0 の数量を Quantity として指す（値オブジェクトを通過し集約で弾かれる）",
			call: func(f reserveFixture) error {
				_, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "SKU-A", Quantity: 0})
				return err
			},
			wantPath: "Quantity",
			wantCode: domain.VQuantity.Code,
			wantErr:  domain.ErrInvalidQuantity,
		},
		{
			name: "境界: QueryStock は空の SKU を Sku として指す",
			call: func(f reserveFixture) error {
				_, err := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "\t"})
				return err
			},
			wantPath: "Sku",
			wantCode: domain.VSKU.Code,
			wantErr:  domain.ErrInvalidSKU,
		},
		{
			name:     "境界: Reserve は空の参照を Ref として指す",
			call:     func(f reserveFixture) error { return f.reserver.Reserve(ctx, application.ReserveInput{Ref: "  "}) },
			wantPath: "Ref",
			wantCode: domain.VReservationRef.Code,
			wantErr:  domain.ErrInvalidReservationRef,
		},
		{
			name:     "境界: Confirm は空の参照を Ref として指す",
			call:     func(f reserveFixture) error { return f.confirmer.Confirm(ctx, "") },
			wantPath: "Ref",
			wantCode: domain.VReservationRef.Code,
			wantErr:  domain.ErrInvalidReservationRef,
		},
		{
			name:     "境界: Release は空の参照を Ref として指す",
			call:     func(f reserveFixture) error { return f.releaser.Release(ctx, "") },
			wantPath: "Ref",
			wantCode: domain.VReservationRef.Code,
			wantErr:  domain.ErrInvalidReservationRef,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.call(newReserveOnlyFixture(t))
			require.ErrorIs(t, err, tc.wantErr, "番兵は維持される（規則 R-15）")
			v := requireSingle(t, err)
			assert.Equal(t, tc.wantPath, v.Path)
			assert.Equal(t, tc.wantCode, v.Code)
		})
	}
}

// 明細の添字。値オブジェクトの走査（アプリ層）と集約の走査（Allocate）の 2 経路があり、
// どちらも同じ表記のパスを出すことを固定する。
func TestValidationPath_ReserveLineIndex(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// 3 SKU を補充してから、行を 1 つずつ壊す。
	newStocked := func(t *testing.T) reserveFixture {
		t.Helper()
		f := newReserveOnlyFixture(t)
		for _, sku := range []string{"SKU-A", "SKU-B", "SKU-C"} {
			_, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: sku, Quantity: 10})
			require.NoError(t, err)
		}
		return f
	}
	okLines := func() []application.ReserveLine {
		return []application.ReserveLine{
			{SKU: "SKU-A", Quantity: 1},
			{SKU: "SKU-B", Quantity: 1},
			{SKU: "SKU-C", Quantity: 1},
		}
	}

	t.Run("境界: SKU が空の行を指す（アプリ層のループが位置を付ける）", func(t *testing.T) {
		t.Parallel()

		for _, broken := range []int{0, 1, 2} {
			t.Run(fmt.Sprintf("境界: %d 行目の SKU が空でも同じ添字を指す", broken), func(t *testing.T) {
				t.Parallel()

				lines := okLines()
				lines[broken].SKU = "  "

				err := newStocked(t).reserver.Reserve(ctx, application.ReserveInput{Ref: "ORDER-1", Lines: lines})
				require.ErrorIs(t, err, domain.ErrInvalidSKU)
				assert.Equal(t, application.FieldViolation{
					Path: fmt.Sprintf("Lines[%d].Sku", broken),
					Code: domain.VSKU.Code,
				}, requireSingle(t, err))
			})
		}
	})

	t.Run("境界: 数量 0 の行を指す（ドメインの Index が位置を運ぶ）", func(t *testing.T) {
		t.Parallel()

		for _, broken := range []int{0, 1, 2} {
			t.Run(fmt.Sprintf("境界: %d 行目の数量が 0 でも同じ添字を指す", broken), func(t *testing.T) {
				t.Parallel()

				lines := okLines()
				// 0 は値オブジェクトを通過するので、位置は Allocate（集約側）でしか分からない。
				lines[broken].Quantity = 0

				err := newStocked(t).reserver.Reserve(ctx, application.ReserveInput{Ref: "ORDER-1", Lines: lines})
				require.ErrorIs(t, err, domain.ErrInvalidQuantity)
				assert.Equal(t, application.FieldViolation{
					Path: fmt.Sprintf("Lines[%d].Quantity", broken),
					Code: domain.VQuantity.Code,
				}, requireSingle(t, err))
			})
		}
	})
}

// locate の透過。検証以外の失敗が ValidationError に化けないことを固定する。
func TestValidationPath_NonValidationErrorsPassThrough(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var ve *application.ValidationError

	t.Run("異常系: 在庫項目が無いと 404 系になる", func(t *testing.T) {
		t.Parallel()

		f := newReserveOnlyFixture(t)
		_, err := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "SKU-UNKNOWN"})
		require.ErrorIs(t, err, domain.ErrStockItemNotFound)
		assert.False(t, errors.As(err, &ve), "リポジトリ由来のエラーは検証エラーに化けない")
	})

	t.Run("異常系: 予約時に在庫項目が無いと 404 系になる", func(t *testing.T) {
		t.Parallel()

		f := newReserveOnlyFixture(t)
		err := f.reserver.Reserve(ctx, application.ReserveInput{
			Ref:   "ORDER-1",
			Lines: []application.ReserveLine{{SKU: "SKU-UNKNOWN", Quantity: 1}},
		})
		require.ErrorIs(t, err, domain.ErrStockItemNotFound)
		assert.False(t, errors.As(err, &ve), "在庫項目なしは検証エラーに化けない")
	})

	t.Run("異常系: 在庫不足は 409 系になる", func(t *testing.T) {
		t.Parallel()

		f := newReserveOnlyFixture(t)
		_, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "SKU-A", Quantity: 1})
		require.NoError(t, err)

		err = f.reserver.Reserve(ctx, application.ReserveInput{
			Ref:   "ORDER-1",
			Lines: []application.ReserveLine{{SKU: "SKU-A", Quantity: 99}},
		})
		require.ErrorIs(t, err, domain.ErrInsufficientStock)
		assert.False(t, errors.As(err, &ve), "在庫不足は状態の矛盾であり検証エラーではない")
	})

	t.Run("異常系: 予約が無い Confirm は 404 系になる", func(t *testing.T) {
		t.Parallel()

		f := newReserveOnlyFixture(t)
		err := f.confirmer.Confirm(ctx, "ORDER-UNKNOWN")
		require.ErrorIs(t, err, domain.ErrReservationNotFound)
		assert.False(t, errors.As(err, &ve), "予約なしは検証エラーに化けない")
	})
}
