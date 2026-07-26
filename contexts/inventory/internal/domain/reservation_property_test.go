// 在庫の予約参照 ReservationRef の往復（P-2 の一部 / R-1・R-2）。
//
// 生成器（newIdentifierGen / newBlankGen）は stock_item_property_test.go にある。
//
// 注文コンテキストにも同名の ReservationRef があるが**別パッケージの別の型**であり、
// 性質テストを共有しない。在庫側にとってこれは「呼び出し側が供給する不透明な相関 ID」であって、
// 注文番号としての意味を持たない。

package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
)

// TestReservationRef_RoundTripsAndRejectsBlank は R-1（正）と R-2（負）を主張する。
//
// 正と負は対で 1 つの性質である。正だけなら「常に成功する New」でも、負だけなら
// 「常に失敗する New」でも満たせてしまう。
func TestReservationRef_RoundTripsAndRejectsBlank(t *testing.T) {
	t.Parallel()

	t.Run("性質: 空白を前後に持たない非空文字列は New と String で往復する", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			s := newIdentifierGen().Draw(t, "s")
			ref, err := domain.NewReservationRef(s)
			require.NoError(t, err, "非空文字列からは生成できる")
			assert.Equal(t, s, ref.String(), "String は入力をそのまま返す")
		})
	})

	t.Run("性質: 空白のみの文字列は ErrInvalidReservationRef とゼロ値を返す", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			s := newBlankGen().Draw(t, "s")
			ref, err := domain.NewReservationRef(s)
			require.ErrorIs(t, err, domain.ErrInvalidReservationRef, "空白のみは番兵で拒否される")
			assert.True(t, ref.IsZero(), "失敗時の返り値はゼロ値")
		})
	})
}
