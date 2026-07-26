// Money の代数的性質（P-4 / R-5〜R-10）。
//
// 例示テスト（money_test.go の TestNewMoney / TestMoney_ZeroValue）は「この入力ならこの結果」を
// 固定する。可換律・結合律・単位元則は入力の**すべての組み合わせ**に対する主張なので、
// 例示をいくら並べても言い尽くせない。そこだけを生成器つきのテストが受け持つ。
//
// 生成域はオーバーフローしない範囲に絞る。主張したいのは代数的法則であって int64 の限界では
// ないので、振り切って「ドメインのバグではない失敗」を報告させても意味がない。

package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
)

// currencies は生成に使う通貨コードの固定集合。
//
// 自由文字列にしないのは、2 つの金額の通貨が偶然一致することがまれになり、同一通貨を前提と
// する可換律・結合律（R-6 / R-7）が「通貨違いで弾かれただけ」に化けて空転するためである。
// 少数の集合から選べば、一致する経路も食い違う経路も確実に踏む。
var currencies = []string{"JPY", "USD", "EUR"}

const (
	// maxMoneyAmount は生成する金額の上限。結合律の 3 項加算と maxMulFactor 倍を
	// 重ねても int64 に収まる（2^40 × 100 ≒ 1.1e14 << 9.2e18）。
	maxMoneyAmount = int64(1) << 40
	// maxMulFactor は R-9 で掛ける係数の上限。反復加算と突き合わせるので大きくしすぎない。
	maxMulFactor = 100
)

// newMoneyGen は通貨を固定した Money の生成器を返す（金額は非負でオーバーフローしない範囲）。
func newMoneyGen(currency string) *rapid.Generator[domain.Money] {
	return rapid.Custom(func(t *rapid.T) domain.Money {
		amount := rapid.Int64Range(0, maxMoneyAmount).Draw(t, "amount")
		m, err := domain.NewMoney(amount, currency)
		require.NoError(t, err, "生成器が不正な Money を作った")
		return m
	})
}

// newDistinctCurrencies は互いに異なる 2 つの通貨コードを引く（R-10 用）。
func newDistinctCurrencies(t *rapid.T) (first, second string) {
	first = rapid.SampledFrom(currencies).Draw(t, "currency")
	second = rapid.SampledFrom(currencies).
		Filter(func(c string) bool { return c != first }).
		Draw(t, "otherCurrency")
	return first, second
}

// TestMoney_RoundTrips は R-5（往復）を主張する。
func TestMoney_RoundTrips(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		amount := rapid.Int64Range(0, maxMoneyAmount).Draw(t, "amount")
		currency := rapid.SampledFrom(currencies).Draw(t, "currency")

		m, err := domain.NewMoney(amount, currency)
		require.NoError(t, err, "非負金額と非空通貨からは生成できる")
		assert.Equal(t, amount, m.Amount(), "Amount は入力の金額を返す")
		assert.Equal(t, currency, m.Currency(), "Currency は入力の通貨を返す")
	})
}

// TestMoney_AddIsCommutative は R-6（Add の可換律）を主張する。
func TestMoney_AddIsCommutative(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		currency := rapid.SampledFrom(currencies).Draw(t, "currency")
		a := newMoneyGen(currency).Draw(t, "a")
		b := newMoneyGen(currency).Draw(t, "b")

		ab, err := a.Add(b)
		require.NoError(t, err, "同一通貨の加算は成功する")
		ba, err := b.Add(a)
		require.NoError(t, err, "同一通貨の加算は成功する")
		assert.Equal(t, ab, ba, "a.Add(b) と b.Add(a) は一致する")
	})
}

// TestMoney_AddIsAssociative は R-7（Add の結合律）を主張する。
func TestMoney_AddIsAssociative(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		currency := rapid.SampledFrom(currencies).Draw(t, "currency")
		a := newMoneyGen(currency).Draw(t, "a")
		b := newMoneyGen(currency).Draw(t, "b")
		c := newMoneyGen(currency).Draw(t, "c")

		ab, err := a.Add(b)
		require.NoError(t, err, "同一通貨の加算は成功する")
		left, err := ab.Add(c)
		require.NoError(t, err, "同一通貨の加算は成功する")

		bc, err := b.Add(c)
		require.NoError(t, err, "同一通貨の加算は成功する")
		right, err := a.Add(bc)
		require.NoError(t, err, "同一通貨の加算は成功する")

		assert.Equal(t, left, right, "(a+b)+c と a+(b+c) は一致する")
	})
}

// TestMoney_ZeroIsAdditiveIdentity は R-8（ゼロ値が加法の単位元）を主張する。
//
// R-8 と R-10（通貨違いは拒否）は一見矛盾する。ゼロ値は通貨が空なのに加算が通るからである。
// これは「注文合計をゼロ値から足し込めるようにする」ための意図的な特別扱いであり、
// 両方を主張して初めてこの設計判断がテストに固定される。片方だけを書くと、特別扱いが
// 事故で消えても（あるいは通貨検査が事故で消えても）気づけない。
func TestMoney_ZeroIsAdditiveIdentity(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		currency := rapid.SampledFrom(currencies).Draw(t, "currency")
		a := newMoneyGen(currency).Draw(t, "a")

		zero := domain.Money{}
		require.True(t, zero.IsZero(), "Money{} はゼロ値")

		right, err := a.Add(zero)
		require.NoError(t, err, "ゼロ値との加算は通貨が空でも成功する")
		assert.Equal(t, a, right, "a.Add(Money{}) は a")

		left, err := zero.Add(a)
		require.NoError(t, err, "ゼロ値との加算は通貨が空でも成功する")
		assert.Equal(t, a, left, "Money{}.Add(a) は a")
	})
}

// TestMoney_MulEqualsRepeatedAdd は R-9（Mul と Add の整合）を主張する。
//
// 係数は 1 以上に限る。Mul は NewMoney を経由せず値を直接構築するため n が負なら
// 不変条件（amount >= 0）に反する Money を作れてしまうが、唯一の呼び出し元
// OrderLine.Subtotal() は Quantity.Int()（常に 1 以上）しか渡さないので到達しない。
// 本 PR はこれを性質の対象外とし、バグ候補として報告するに留める（黙って実装を変えない）。
func TestMoney_MulEqualsRepeatedAdd(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		currency := rapid.SampledFrom(currencies).Draw(t, "currency")
		a := newMoneyGen(currency).Draw(t, "a")
		n := rapid.IntRange(1, maxMulFactor).Draw(t, "n")

		sum := a
		for i := 1; i < n; i++ {
			next, err := sum.Add(a)
			require.NoError(t, err, "同一通貨の加算は成功する")
			sum = next
		}
		assert.Equal(t, sum, a.Mul(n), "a.Mul(n) は a を n 回加算した結果に一致する")
	})
}

// TestMoney_AddRejectsCurrencyMismatch は R-10（通貨違いの拒否）を主張する。
func TestMoney_AddRejectsCurrencyMismatch(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		curA, curB := newDistinctCurrencies(t)
		// 双方が非ゼロでなければ単位元則（R-8）が優先されて加算が通るので、金額は 1 以上にする。
		a, err := domain.NewMoney(rapid.Int64Range(1, maxMoneyAmount).Draw(t, "amountA"), curA)
		require.NoError(t, err, "生成器が不正な Money を作った")
		b, err := domain.NewMoney(rapid.Int64Range(1, maxMoneyAmount).Draw(t, "amountB"), curB)
		require.NoError(t, err, "生成器が不正な Money を作った")

		got, err := a.Add(b)
		require.ErrorIs(t, err, domain.ErrInvalidMoney, "通貨違いの加算は ErrInvalidMoney")
		assert.True(t, got.IsZero(), "失敗時の返り値はゼロ値")
	})
}
