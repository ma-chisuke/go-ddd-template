// 注文明細を構成する値オブジェクトの性質（P-1 の一部 / R-1・R-2、P-3 / R-3・R-4）。
//
// 生成器（newIdentifierGen / newBlankGen）は order_property_test.go にある。同じ
// package domain_test なので共有でき、識別子の生成規則が 1 箇所に留まる。

package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
)

// TestSKU_RoundTripsAndRejectsBlank は注文コンテキストの SKU について R-1（正）と
// R-2（負）を主張する。
//
// 在庫コンテキストの SKU は**別パッケージの別の型**であり、性質テストを共有しない。
func TestSKU_RoundTripsAndRejectsBlank(t *testing.T) {
	t.Parallel()

	t.Run("性質: 空白を前後に持たない非空文字列は New と String で往復する", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			s := newIdentifierGen().Draw(t, "s")
			sku, err := domain.NewSKU(s)
			require.NoError(t, err, "非空文字列からは生成できる")
			assert.Equal(t, s, sku.String(), "String は入力をそのまま返す")
		})
	})

	t.Run("性質: 空白のみの文字列は ErrInvalidSKU とゼロ値を返す", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			s := newBlankGen().Draw(t, "s")
			sku, err := domain.NewSKU(s)
			require.ErrorIs(t, err, domain.ErrInvalidSKU, "空白のみは番兵で拒否される")
			assert.Equal(t, domain.SKU{}, sku, "失敗時の返り値はゼロ値")
		})
	})
}

// TestQuantity_RoundTripsAndRejectsNonPositive は注文行の Quantity について
// R-3（正）と R-4（負）を主張する。
//
// **値域が 1 以上**である点が在庫コンテキストの Quantity（0 以上）との違いであり、
// この非対称そのものが「値オブジェクトはコンテキストごとに所有する」ことの教材である。
// 0 は注文行としては不正だが在庫としては有効なので、境界は 1 と 0 の間にある。
func TestQuantity_RoundTripsAndRejectsNonPositive(t *testing.T) {
	t.Parallel()

	t.Run("性質: 1 以上の整数は NewQuantity と Int で往復する", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			n := rapid.IntRange(1, maxLineQuantity).Draw(t, "n")
			qty, err := domain.NewQuantity(n)
			require.NoError(t, err, "1 以上は注文行の数量として有効")
			assert.Equal(t, n, qty.Int(), "Int は入力をそのまま返す")
		})
	})

	// 分類は「境界」ではなく「性質」である。D-2 の判定は上から順に評価して最初に該当した
	// ものを採るので、生成器で入力を振っている時点で 1 番目の「性質」で確定する。
	t.Run("性質: 0 以下の整数は ErrInvalidQuantity とゼロ値を返す", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			n := rapid.IntRange(-maxLineQuantity, 0).Draw(t, "n")
			qty, err := domain.NewQuantity(n)
			require.ErrorIs(t, err, domain.ErrInvalidQuantity, "0 以下は番兵で拒否される")
			assert.Equal(t, domain.Quantity{}, qty, "失敗時の返り値はゼロ値")
		})
	})
}
