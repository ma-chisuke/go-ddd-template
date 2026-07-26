package application

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
)

// locate の安全性は、ユースケース経由のテスト（validation_path_test.go）では踏めない
// 分岐を持つ。ここでは非公開関数を直接呼び、その分岐を明示的に固定する。

func TestLocate_PassesThroughNonDomainErrors(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("リポジトリの失敗")

	got := locate("Lines[0]", sentinel)

	assert.Same(t, sentinel, got, "元のエラーをそのまま返す（新しい値で包まない）")
	var ve *ValidationError
	assert.False(t, errors.As(got, &ve), "検証エラーに化けない")
}

func TestLocate_DoesNotDoubleWrap(t *testing.T) {
	t.Parallel()

	_, err := domain.NewSKU("")
	first := locate("Lines[0]", err)
	require.IsType(t, &ValidationError{}, first)

	second := locate("Lines[9]", first)

	assert.Same(t, first, second)
	var ve *ValidationError
	require.ErrorAs(t, second, &ve)
	assert.Equal(t, "Lines[0].Sku", ve.Violations[0].Path, "最初の位置が保たれる")
}

// 上書き表 dtoPaths に無いフィールドは、機械的な変換（先頭 1 文字を大文字にする）で
// パス断片にする。**ドメインに Rule を 1 行足すだけでこの層は動く**ことを示す。
func TestLocate_UnknownFieldUsesMechanicalConversion(t *testing.T) {
	t.Parallel()

	newRule := domain.Rule{Field: "somethingNew", Code: "something_new", Err: domain.ErrInvalidSKU}

	var ve *ValidationError
	require.ErrorAs(t, locate("", newRule.Violated("新しい規則に違反しました")), &ve)
	assert.Equal(t, "SomethingNew", ve.Violations[0].Path, "dtoPaths への追記なしで写る")
	assert.Equal(t, "something_new", ve.Violations[0].Code)
}

// 上書き表に載っているフィールド（予約参照）は DTO 上の名前へ写る。
func TestLocate_OverriddenFieldUsesTable(t *testing.T) {
	t.Parallel()

	_, err := domain.NewReservationRef("")

	var ve *ValidationError
	require.ErrorAs(t, locate("", err), &ve)
	assert.Equal(t, "Ref", ve.Violations[0].Path, "ドメインは reservationRef、DTO は Ref")
}

// ドメインが位置を運んできたら（Rule.ViolatedAt）、呼び出し側が渡した前置より明細の
// パスが優先される。これが ReservationService.Allocate（走査が集約側にある）の経路である。
func TestLocate_DomainIndexOverridesPrefix(t *testing.T) {
	t.Parallel()

	err := domain.VQuantity.ViolatedAt(2, "予約数量は 1 以上でなければなりません")

	var ve *ValidationError
	// 前置は空（Allocate の呼び出し側はトップ階層として呼ぶ）。
	require.ErrorAs(t, locate("", err), &ve)
	assert.Equal(t, "Lines[2].Quantity", ve.Violations[0].Path)
}

func TestLocate_NilInput(t *testing.T) {
	t.Parallel()

	assert.NoError(t, locate("", nil), "nil は nil のまま")
}

// ValidationError.Error() が包んだ文言をそのまま返すこと。
// ログ出力（NewError の WarnContext / ErrorContext）がこの文言に依存している。
func TestValidationError_ErrorPassesThroughWrappedMessage(t *testing.T) {
	t.Parallel()

	_, err := domain.NewQuantity(-1)
	located := locate("", err)

	assert.Equal(t, err.Error(), located.Error(), "元の文言を変えない")
	assert.ErrorIs(t, located, domain.ErrInvalidQuantity, "番兵まで Unwrap が繋がる")
}
