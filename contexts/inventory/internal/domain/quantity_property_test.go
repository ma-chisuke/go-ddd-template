// 在庫の数量 Quantity の代数的性質（P-5 / R-11〜R-17）。
//
// 在庫の Quantity は**0 以上**を値域とし、注文コンテキストの Quantity（1 以上）とは
// 別の型・別の値域である。この非対称そのものが「値オブジェクトはコンテキストごとに所有する」
// ことの教材であり、性質テストも共有しない。
//
// 生成域はオーバーフローしない範囲に絞る。主張したいのは代数的法則であって int の限界ではない。

package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
)

// maxQuantity は生成する数量の上限。結合律の 3 項加算を重ねても int に収まる範囲に留める。
const maxQuantity = 1 << 20

// newQuantityGen は 0 以上の Quantity の生成器を返す。
func newQuantityGen() *rapid.Generator[domain.Quantity] {
	return rapid.Custom(func(t *rapid.T) domain.Quantity {
		q, err := domain.NewQuantity(rapid.IntRange(0, maxQuantity).Draw(t, "n"))
		require.NoError(t, err, "生成器が不正な Quantity を作った")
		return q
	})
}

// TestQuantity_RoundTripsAndRejectsNegative は R-11（正）と R-12（負）を主張する。
func TestQuantity_RoundTripsAndRejectsNegative(t *testing.T) {
	t.Parallel()

	t.Run("性質: 0 以上の整数は NewQuantity と Int で往復する", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			n := rapid.IntRange(0, maxQuantity).Draw(t, "n")
			q, err := domain.NewQuantity(n)
			require.NoError(t, err, "0 以上は在庫の数量として有効（0 は利用可能在庫を表す）")
			assert.Equal(t, n, q.Int(), "Int は入力をそのまま返す")
		})
	})

	t.Run("性質: 負の整数は ErrInvalidQuantity とゼロ値を返す", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			n := rapid.IntRange(-maxQuantity, -1).Draw(t, "n")
			q, err := domain.NewQuantity(n)
			require.ErrorIs(t, err, domain.ErrInvalidQuantity, "負数は番兵で拒否される")
			assert.Equal(t, domain.Quantity{}, q, "失敗時の返り値はゼロ値")
		})
	})
}

// TestQuantity_AddIsCommutative は R-13（Add の可換律）を主張する。
func TestQuantity_AddIsCommutative(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		a := newQuantityGen().Draw(t, "a")
		b := newQuantityGen().Draw(t, "b")
		assert.Equal(t, a.Add(b), b.Add(a), "a.Add(b) と b.Add(a) は一致する")
	})
}

// TestQuantity_AddIsAssociative は R-14（Add の結合律）を主張する。
func TestQuantity_AddIsAssociative(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		a := newQuantityGen().Draw(t, "a")
		b := newQuantityGen().Draw(t, "b")
		c := newQuantityGen().Draw(t, "c")
		assert.Equal(t, a.Add(b).Add(c), a.Add(b.Add(c)), "(a+b)+c と a+(b+c) は一致する")
	})
}

// TestQuantity_SubInvertsAdd は R-15（逆元）を主張する。
//
// Sub は結果が負になるとエラーを返すが、q.Add(x) は必ず x 以上なので**この経路では常に成功する**。
// 「成功すること」と「結果が q に戻ること」の両方を主張するのが要点で、エラーを握りつぶす
// 実装や 0 に丸める実装はここで落ちる。
func TestQuantity_SubInvertsAdd(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		q := newQuantityGen().Draw(t, "q")
		x := newQuantityGen().Draw(t, "x")

		got, err := q.Add(x).Sub(x)
		require.NoError(t, err, "加えた分を引き戻す操作は負にならないので成功する")
		assert.Equal(t, q, got, "q.Add(x).Sub(x) は q に戻る")
	})
}

// TestQuantity_GreaterThanIsTrichotomous は R-16（三分律）を主張する。
//
// a > b / b > a / a == b の**ちょうど 1 つ**が真であること。個別に真偽を見るのではなく
// 「真になった数が 1」を数えるのは、たとえば GreaterThan が常に false を返す実装でも
// 「a > b が偽」という個別の主張だけなら満たせてしまうからである。
func TestQuantity_GreaterThanIsTrichotomous(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		a := newQuantityGen().Draw(t, "a")
		b := newQuantityGen().Draw(t, "b")

		trueCount := 0
		for _, holds := range []bool{a.GreaterThan(b), b.GreaterThan(a), a == b} {
			if holds {
				trueCount++
			}
		}
		assert.Equal(t, 1, trueCount, "a > b・b > a・a == b のちょうど 1 つが真")
	})
}

// TestQuantity_GreaterThanIsTransitive は R-17（推移律）を主張する。
//
// 前提（a > b かつ b > c）を**構成して**満たす。3 つを独立に引いて「前提が成り立つときだけ
// 結論を見る」形にすると、前提がまれにしか成り立たず主張が空転する（真空的に真になる）。
// 差分を 1 以上に取ることで、毎回 a > b > c が成り立つ三つ組だけを作る。
func TestQuantity_GreaterThanIsTransitive(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		base := rapid.IntRange(0, maxQuantity).Draw(t, "base")
		gapLow := rapid.IntRange(1, maxQuantity).Draw(t, "gapLow")
		gapHigh := rapid.IntRange(1, maxQuantity).Draw(t, "gapHigh")

		c, err := domain.NewQuantity(base)
		require.NoError(t, err, "生成器が不正な Quantity を作った")
		b, err := domain.NewQuantity(base + gapLow)
		require.NoError(t, err, "生成器が不正な Quantity を作った")
		a, err := domain.NewQuantity(base + gapLow + gapHigh)
		require.NoError(t, err, "生成器が不正な Quantity を作った")

		require.True(t, a.GreaterThan(b), "前提: a > b")
		require.True(t, b.GreaterThan(c), "前提: b > c")
		assert.True(t, a.GreaterThan(c), "a > b かつ b > c ならば a > c")
	})
}
