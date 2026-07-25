package order_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

// このファイルは「ドメインが自分の語彙でフィールドを名乗る」という契約を固定する。
//
// 検証する不変条件は 2 つある。
//  1. 後方互換（規則 R-15）: errors.Is が従来どおり番兵に一致し続けること。
//     既存の value_objects_test.go / order_test.go は無変更でこれを検証しており、
//     本ファイルはそこに errors.As による Field / Code の固定を足すだけである。
//  2. FR-4.3 の中核: NewMoney の amount と currency が区別されること。番兵は双方とも
//     ErrInvalidMoney で同一なので、この区別が壊れても errors.Is ベースのテストは通る。
//     つまりこのテストが無いと FR-4 の目的そのものが検証されない。

// requireViolation は err からドメインの FieldViolation を取り出す。
func requireViolation(t *testing.T, err error) *order.FieldViolation {
	t.Helper()
	var v *order.FieldViolation
	require.ErrorAs(t, err, &v, "FieldViolation として取り出せること")
	return v
}

func TestFieldViolation_ValueObjects(t *testing.T) {
	cases := []struct {
		name string
		err  func() error
		// want は違反が名乗るべき検証規則。Rule ごと比較するので、Field / Code / 番兵の
		// 3 つが同時に固定される（3 つを別々に書き並べる必要はもう無い）。
		want order.Rule
	}{
		{
			name: "NewQuantity(0) は quantity を名乗る",
			err:  func() error { _, err := order.NewQuantity(0); return err },
			want: order.VQuantity,
		},
		{
			name: "NewMoney(-1, JPY) は amount を名乗る",
			err:  func() error { _, err := order.NewMoney(-1, "JPY"); return err },
			want: order.VMoneyAmount,
		},
		{
			name: "NewMoney(100, 空) は currency を名乗る",
			err:  func() error { _, err := order.NewMoney(100, "  "); return err },
			want: order.VMoneyCurrency,
		},
		{
			name: "NewSKU(空) は sku を名乗る",
			err:  func() error { _, err := order.NewSKU("   "); return err },
			want: order.VSKU,
		},
		{
			name: "NewCustomerID(空) は customerId を名乗る",
			err:  func() error { _, err := order.NewCustomerID(" "); return err },
			want: order.VCustomerID,
		},
		{
			name: "NewOrderID(空) は orderId を名乗る",
			err:  func() error { _, err := order.NewOrderID(""); return err },
			want: order.VOrderID,
		},
		{
			name: "NewReservationRef(空) は reservationRef を名乗る",
			err:  func() error { _, err := order.NewReservationRef(""); return err },
			want: order.VReservationRef,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.err()
			// 1) 後方互換: errors.Is は従来どおり番兵に一致する（規則 R-15）。
			require.ErrorIs(t, err, tc.want.Err, "番兵まで Unwrap が繋がること")
			// 2) 構造化: 違反が名乗る規則が期待どおり（Field / Code / 番兵の同時固定）。
			v := requireViolation(t, err)
			assert.Equal(t, tc.want, v.Rule)
			assert.Nil(t, v.Index, "値オブジェクトの違反は位置を持たない")
		})
	}
}

// FR-4.3 の回帰テスト。amount と currency が同じ番兵を共有していても別物として
// 名乗れることを、両者を同時に比較して固定する。
func TestFieldViolation_NewMoneyDistinguishesAmountAndCurrency(t *testing.T) {
	_, amountErr := order.NewMoney(-1, "JPY")
	_, currencyErr := order.NewMoney(100, "")

	// 前提: 番兵は同一である（だから番兵だけでは区別できない）。
	require.ErrorIs(t, amountErr, order.ErrInvalidMoney)
	require.ErrorIs(t, currencyErr, order.ErrInvalidMoney)

	amount := requireViolation(t, amountErr)
	currency := requireViolation(t, currencyErr)

	assert.NotEqual(t, amount.Rule.Field, currency.Rule.Field, "Field が区別されること")
	assert.NotEqual(t, amount.Rule.Code, currency.Rule.Code, "Code が区別されること")
	assert.Equal(t, order.VMoneyAmount, amount.Rule, "amount 側の規則")
	assert.Equal(t, order.VMoneyCurrency, currency.Rule, "currency 側の規則")
}

func TestFieldViolation_AggregateRuleEmptyOrder(t *testing.T) {
	id, err := order.NewOrderID("ORDER-1")
	require.NoError(t, err)
	customer, err := order.NewCustomerID("CUST-1")
	require.NoError(t, err)

	_, err = order.NewOrder(id, customer, nil)
	require.ErrorIs(t, err, order.ErrEmptyOrder, "番兵は維持される（規則 R-15）")

	v := requireViolation(t, err)
	assert.Equal(t, order.VEmptyOrder, v.Rule)
}

// 単一フィールドに帰着しない違反は FieldViolation にしない（規則 R-14 / FR-3.4）。
// 「違反フィールドが 0 件」と「特定できなかった」を区別するため、ここで
// FieldViolation を返してしまわないことを固定する。
func TestFieldViolation_NotAttributableToOneField(t *testing.T) {
	t.Run("異常系: 通貨をまたぐ加算は FieldViolation にしない", func(t *testing.T) {
		jpy, err := order.NewMoney(100, "JPY")
		require.NoError(t, err)
		usd, err := order.NewMoney(100, "USD")
		require.NoError(t, err)

		_, err = jpy.Add(usd)
		require.ErrorIs(t, err, order.ErrInvalidMoney)

		var v *order.FieldViolation
		assert.False(t, errors.As(err, &v), "単一フィールドに帰着しないので FieldViolation にしない")
	})

	t.Run("異常系: Confirmed でない注文の取消は FieldViolation にしない", func(t *testing.T) {
		id, err := order.NewOrderID("ORDER-1")
		require.NoError(t, err)
		customer, err := order.NewCustomerID("CUST-1")
		require.NoError(t, err)
		sku, err := order.NewSKU("SKU-A")
		require.NoError(t, err)
		qty, err := order.NewQuantity(1)
		require.NoError(t, err)
		price, err := order.NewMoney(100, "JPY")
		require.NoError(t, err)

		o, err := order.NewOrder(id, customer, []order.OrderLine{order.NewOrderLine(sku, qty, price)})
		require.NoError(t, err)
		require.NoError(t, o.Cancel(), "1 回目の取消は成功する")

		err = o.Cancel() // 2 回目は Confirmed ではないので失敗する
		require.ErrorIs(t, err, order.ErrOrderNotConfirmed)

		var v *order.FieldViolation
		assert.False(t, errors.As(err, &v), "409 系は入力フィールドの問題ではない")
	})
}

// Error() が番兵の文言をそのまま返すこと（既存のログ出力とエラー文言を変えないための契約）。
func TestFieldViolation_ErrorPassesThroughWrappedMessage(t *testing.T) {
	_, err := order.NewQuantity(0)
	v := requireViolation(t, err)

	assert.Equal(t, v.Err.Error(), v.Error())
	assert.Contains(t, v.Error(), "1 以上")
	assert.Equal(t, v.Err, v.Unwrap())
}
