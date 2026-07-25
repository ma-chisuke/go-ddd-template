package application_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
	"github.com/example/go-ddd-template/contexts/ordering/port"
	"github.com/example/go-ddd-template/shared/uow"
)

// このファイルは「アプリケーション層が入力 DTO 上の位置を付与する」ことを固定する
// （FR-4.5）。既存の validation_test.go は errors.Is（番兵）だけを見ており、位置が
// 付いているか・正しい行を指しているかは検証していない。

// requireViolations は err からアプリケーション層の ValidationError を取り出す。
func requireViolations(t *testing.T, err error) []application.FieldViolation {
	t.Helper()
	var ve *application.ValidationError
	require.ErrorAs(t, err, &ve, "ValidationError として取り出せること")
	require.NotEmpty(t, ve.Violations, "違反が 1 件以上あること")
	return ve.Violations
}

// requireSingle は違反がちょうど 1 件であることを確認して返す。
func requireSingle(t *testing.T, err error) application.FieldViolation {
	t.Helper()
	vs := requireViolations(t, err)
	require.Len(t, vs, 1)
	return vs[0]
}

func TestValidationPath_PlaceOrder(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name     string
		in       application.PlaceOrderInput
		wantPath string
		wantCode string
		wantErr  error
	}{
		{
			name:     "境界: 顧客 ID が空なら CustomerId を指す",
			in:       application.PlaceOrderInput{CustomerID: "  ", Lines: []application.PlaceOrderLine{{SKU: "SKU-A", Quantity: 1, UnitPriceAmount: 100, Currency: "JPY"}}},
			wantPath: "CustomerId",
			wantCode: order.VCustomerID.Code,
			wantErr:  order.ErrInvalidCustomerID,
		},
		{
			name:     "境界: 明細が空なら Lines を指す（集約規則）",
			in:       application.PlaceOrderInput{CustomerID: "CUST-1"},
			wantPath: "Lines",
			wantCode: order.VEmptyOrder.Code,
			wantErr:  order.ErrEmptyOrder,
		},
		{
			name:     "境界: 1 行目の SKU が空なら Lines[0].Sku を指す",
			in:       application.PlaceOrderInput{CustomerID: "CUST-1", Lines: []application.PlaceOrderLine{{SKU: " ", Quantity: 1, UnitPriceAmount: 100, Currency: "JPY"}}},
			wantPath: "Lines[0].Sku",
			wantCode: order.VSKU.Code,
			wantErr:  order.ErrInvalidSKU,
		},
		{
			name:     "境界: 1 行目の数量が 0 なら Lines[0].Quantity を指す",
			in:       application.PlaceOrderInput{CustomerID: "CUST-1", Lines: []application.PlaceOrderLine{{SKU: "SKU-A", Quantity: 0, UnitPriceAmount: 100, Currency: "JPY"}}},
			wantPath: "Lines[0].Quantity",
			wantCode: order.VQuantity.Code,
			wantErr:  order.ErrInvalidQuantity,
		},
		{
			name:     "境界: 1 行目の金額が負なら Lines[0].UnitPrice.Amount を指す",
			in:       application.PlaceOrderInput{CustomerID: "CUST-1", Lines: []application.PlaceOrderLine{{SKU: "SKU-A", Quantity: 1, UnitPriceAmount: -1, Currency: "JPY"}}},
			wantPath: "Lines[0].UnitPrice.Amount",
			wantCode: order.VMoneyAmount.Code,
			wantErr:  order.ErrInvalidMoney,
		},
		{
			name:     "境界: 1 行目の通貨が空なら Lines[0].UnitPrice.Currency を指す",
			in:       application.PlaceOrderInput{CustomerID: "CUST-1", Lines: []application.PlaceOrderLine{{SKU: "SKU-A", Quantity: 1, UnitPriceAmount: 100, Currency: ""}}},
			wantPath: "Lines[0].UnitPrice.Currency",
			wantCode: order.VMoneyCurrency.Code,
			wantErr:  order.ErrInvalidMoney,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// reserver に EXPECT を置かない = 検証で弾かれるので在庫予約は呼ばれない。
			f := newMemFixture(t)
			_, err := f.place.Handle(ctx, tc.in)

			require.ErrorIs(t, err, tc.wantErr, "番兵は維持される（規則 R-15）")
			v := requireSingle(t, err)
			assert.Equal(t, tc.wantPath, v.Path)
			assert.Equal(t, tc.wantCode, v.Code)
		})
	}
}

// 添字が「たまたま 0」で通ってしまわないよう、壊す行を動かして検証する。
// 実装が for _, l := range に戻れば（＝位置を落とせば）このテストが落ちる。
func TestValidationPath_PlaceOrderReportsTheBrokenLine(t *testing.T) {
	ctx := context.Background()
	ok := func(sku string) application.PlaceOrderLine {
		return application.PlaceOrderLine{SKU: sku, Quantity: 1, UnitPriceAmount: 100, Currency: "JPY"}
	}

	for _, broken := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("境界: 壊れているのが %d 行目でも同じ添字を指す", broken), func(t *testing.T) {
			lines := []application.PlaceOrderLine{ok("SKU-A"), ok("SKU-B"), ok("SKU-C")}
			lines[broken].Quantity = 0

			f := newMemFixture(t)
			_, err := f.place.Handle(ctx, application.PlaceOrderInput{CustomerID: "CUST-1", Lines: lines})

			v := requireSingle(t, err)
			assert.Equal(t, application.FieldViolation{
				Path: fmt.Sprintf("Lines[%d].Quantity", broken),
				Code: order.VQuantity.Code,
			}, v)
		})
	}
}

func TestValidationPath_CancelAndGetOrder(t *testing.T) {
	ctx := context.Background()

	t.Run("境界: CancelOrder は空白のみの ID を OrderId として指す", func(t *testing.T) {
		f := newMemFixture(t)
		err := f.cancel.Handle(ctx, "   ")
		require.ErrorIs(t, err, order.ErrInvalidOrderID)
		v := requireSingle(t, err)
		assert.Equal(t, "OrderId", v.Path)
		assert.Equal(t, order.VOrderID.Code, v.Code)
	})

	t.Run("境界: GetOrder は空白のみの ID を OrderId として指す", func(t *testing.T) {
		f := newMemFixture(t)
		_, err := f.get.Handle(ctx, "   ")
		require.ErrorIs(t, err, order.ErrInvalidOrderID)
		v := requireSingle(t, err)
		assert.Equal(t, "OrderId", v.Path)
	})
}

// locate の透過。検証以外の失敗が ValidationError に化けないことを固定する。
// ここが壊れると、リポジトリ障害や版衝突に対してリクエスターへ「あなたの入力が悪い」と
// 嘘をつくことになる。
func TestValidationPath_NonValidationErrorsPassThrough(t *testing.T) {
	ctx := context.Background()
	var ve *application.ValidationError

	t.Run("異常系: 存在しない注文の取消は 404 系になる", func(t *testing.T) {
		f := newMemFixture(t)
		err := f.cancel.Handle(ctx, "MISSING")
		require.ErrorIs(t, err, order.ErrOrderNotFound)
		assert.False(t, errors.As(err, &ve), "リポジトリ由来のエラーは検証エラーに化けない")
	})

	t.Run("異常系: 在庫予約の拒否は 409 系になる", func(t *testing.T) {
		f := newMemFixture(t)
		f.reserver.EXPECT().
			Reserve(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(port.ErrReservationRejected)

		_, err := f.place.Handle(ctx, sampleInput())
		require.ErrorIs(t, err, port.ErrReservationRejected)
		assert.False(t, errors.As(err, &ve), "ACL 由来のエラーは検証エラーに化けない")
	})

	t.Run("並行: 版衝突が再試行上限を超えると 409 系になる", func(t *testing.T) {
		store := memory.NewStore()
		stores := memory.NewStores()
		// 再試行上限（既定 3）を超える回数だけ衝突を注入し、UoW を必ず失敗させる。
		work := &flakyUoW{inner: memory.NewUnitOfWork(store, stores), failsLeft: 10}
		f := newMemFixtureWith(t, work, store, stores)
		f.reserver.EXPECT().Reserve(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		f.reserver.EXPECT().Release(gomock.Any(), gomock.Any()).Return(nil)

		_, err := f.place.Handle(ctx, sampleInput())
		require.ErrorIs(t, err, uow.ErrConcurrencyConflict)
		assert.False(t, errors.As(err, &ve), "版衝突は検証エラーに化けない")
	})
}
