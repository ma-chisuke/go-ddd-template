package domain_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
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
func requireViolation(t *testing.T, err error) *domain.FieldViolation {
	t.Helper()
	var v *domain.FieldViolation
	require.ErrorAs(t, err, &v, "FieldViolation として取り出せること")
	return v
}

func TestFieldViolation_ValueObjects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  func() error
		// want は違反が名乗るべき検証規則。Rule ごと比較するので、Field / Code / 番兵の
		// 3 つが同時に固定される（3 つを別々に書き並べる必要はもう無い）。
		want domain.Rule
	}{
		{
			name: "境界: NewQuantity(0) は quantity を名乗る",
			err:  func() error { _, err := domain.NewQuantity(0); return err },
			want: domain.VQuantity,
		},
		{
			name: "境界: NewMoney(-1, JPY) は amount を名乗る",
			err:  func() error { _, err := domain.NewMoney(-1, "JPY"); return err },
			want: domain.VMoneyAmount,
		},
		{
			name: "境界: NewMoney(100, 空) は currency を名乗る",
			err:  func() error { _, err := domain.NewMoney(100, "  "); return err },
			want: domain.VMoneyCurrency,
		},
		{
			name: "境界: NewSKU(空) は sku を名乗る",
			err:  func() error { _, err := domain.NewSKU("   "); return err },
			want: domain.VSKU,
		},
		{
			name: "境界: NewCustomerID(空) は customerId を名乗る",
			err:  func() error { _, err := domain.NewCustomerID(" "); return err },
			want: domain.VCustomerID,
		},
		{
			name: "境界: NewOrderID(空) は orderId を名乗る",
			err:  func() error { _, err := domain.NewOrderID(""); return err },
			want: domain.VOrderID,
		},
		{
			name: "境界: NewReservationRef(空) は reservationRef を名乗る",
			err:  func() error { _, err := domain.NewReservationRef(""); return err },
			want: domain.VReservationRef,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

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
	t.Parallel()

	_, amountErr := domain.NewMoney(-1, "JPY")
	_, currencyErr := domain.NewMoney(100, "")

	// 前提: 番兵は同一である（だから番兵だけでは区別できない）。
	require.ErrorIs(t, amountErr, domain.ErrInvalidMoney)
	require.ErrorIs(t, currencyErr, domain.ErrInvalidMoney)

	amount := requireViolation(t, amountErr)
	currency := requireViolation(t, currencyErr)

	assert.NotEqual(t, amount.Rule.Field, currency.Rule.Field, "Field が区別されること")
	assert.NotEqual(t, amount.Rule.Code, currency.Rule.Code, "Code が区別されること")
	assert.Equal(t, domain.VMoneyAmount, amount.Rule, "amount 側の規則")
	assert.Equal(t, domain.VMoneyCurrency, currency.Rule, "currency 側の規則")
}

func TestFieldViolation_AggregateRuleEmptyOrder(t *testing.T) {
	t.Parallel()

	id, err := domain.NewOrderID("ORDER-1")
	require.NoError(t, err)
	customer, err := domain.NewCustomerID("CUST-1")
	require.NoError(t, err)

	_, err = domain.NewOrder(id, customer, nil)
	require.ErrorIs(t, err, domain.ErrEmptyOrder, "番兵は維持される（規則 R-15）")

	v := requireViolation(t, err)
	assert.Equal(t, domain.VEmptyOrder, v.Rule)
}

// 単一フィールドに帰着しない違反は FieldViolation にしない（規則 R-14 / FR-3.4）。
// 「違反フィールドが 0 件」と「特定できなかった」を区別するため、ここで
// FieldViolation を返してしまわないことを固定する。
func TestFieldViolation_NotAttributableToOneField(t *testing.T) {
	t.Parallel()

	t.Run("異常系: 通貨をまたぐ加算は FieldViolation にしない", func(t *testing.T) {
		t.Parallel()

		jpy, err := domain.NewMoney(100, "JPY")
		require.NoError(t, err)
		usd, err := domain.NewMoney(100, "USD")
		require.NoError(t, err)

		_, err = jpy.Add(usd)
		require.ErrorIs(t, err, domain.ErrInvalidMoney)

		var v *domain.FieldViolation
		assert.False(t, errors.As(err, &v), "単一フィールドに帰着しないので FieldViolation にしない")
	})

	t.Run("異常系: Confirmed でない注文の取消は FieldViolation にしない", func(t *testing.T) {
		t.Parallel()

		id, err := domain.NewOrderID("ORDER-1")
		require.NoError(t, err)
		customer, err := domain.NewCustomerID("CUST-1")
		require.NoError(t, err)
		sku, err := domain.NewSKU("SKU-A")
		require.NoError(t, err)
		qty, err := domain.NewQuantity(1)
		require.NoError(t, err)
		price, err := domain.NewMoney(100, "JPY")
		require.NoError(t, err)

		o, err := domain.NewOrder(id, customer, []domain.OrderLine{domain.NewOrderLine(sku, qty, price)})
		require.NoError(t, err)
		require.NoError(t, o.Cancel(), "1 回目の取消は成功する")

		err = o.Cancel() // 2 回目は Confirmed ではないので失敗する
		require.ErrorIs(t, err, domain.ErrOrderNotConfirmed)

		var v *domain.FieldViolation
		assert.False(t, errors.As(err, &v), "409 系は入力フィールドの問題ではない")
	})
}

// Error() が番兵の文言をそのまま返すこと（既存のログ出力とエラー文言を変えないための契約）。
func TestFieldViolation_ErrorPassesThroughWrappedMessage(t *testing.T) {
	t.Parallel()

	_, err := domain.NewQuantity(0)
	v := requireViolation(t, err)

	assert.Equal(t, v.Err.Error(), v.Error())
	assert.Contains(t, v.Error(), "1 以上")
	assert.Equal(t, v.Err, v.Unwrap())
}
