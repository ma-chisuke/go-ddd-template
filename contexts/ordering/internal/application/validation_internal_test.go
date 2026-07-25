package application

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

// locate の安全性は、ユースケース経由のテスト（validation_path_test.go）では踏めない
// 分岐を持つ。ここでは非公開関数を直接呼び、その分岐を明示的に固定する。
//
// 内部テスト（package application）にしているのは、locate が非公開であり、かつこれらが
// 「外から観測できない安全網」だからである。外から観測できる振る舞いは
// validation_path_test.go（package application_test）が受け持つ。

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

	_, err := order.NewQuantity(0)
	first := locate("Lines[0]", err)
	require.IsType(t, &ValidationError{}, first)

	// 2 度目の locate は前置を上書きせず、1 度目の結果をそのまま返す。
	second := locate("Lines[9]", first)

	assert.Same(t, first, second)
	var ve *ValidationError
	require.ErrorAs(t, second, &ve)
	require.Len(t, ve.Violations, 1)
	assert.Equal(t, "Lines[0].Quantity", ve.Violations[0].Path, "最初の位置が保たれる")
}

// 上書き表 dtoPaths に無いフィールドは、機械的な変換（先頭 1 文字を大文字にする）で
// パス断片にする。**ドメインに Rule を 1 行足すだけでこの層は動く**ことを示す。
func TestLocate_UnknownFieldUsesMechanicalConversion(t *testing.T) {
	t.Parallel()

	// ドメインに新しい規則を 1 行足した状況を模す。
	newRule := order.Rule{Field: "somethingNew", Code: "something_new", Err: order.ErrInvalidSKU}

	var ve *ValidationError
	require.ErrorAs(t, locate("Lines[0]", newRule.Violated("新しい規則に違反しました")), &ve)
	require.Len(t, ve.Violations, 1)
	assert.Equal(t, "Lines[0].SomethingNew", ve.Violations[0].Path, "dtoPaths への追記なしで写る")
	assert.Equal(t, "something_new", ve.Violations[0].Code)
}

// 上書き表に載っているフィールド（金額）は入れ子のパスへ写る。
// 平らな入力 DTO と入れ子の API の差を dtoPaths が吸収していることを固定する。
func TestLocate_OverriddenFieldUsesTable(t *testing.T) {
	t.Parallel()

	_, err := order.NewMoney(-1, "JPY")

	var ve *ValidationError
	require.ErrorAs(t, locate("Lines[0]", err), &ve)
	assert.Equal(t, "Lines[0].UnitPrice.Amount", ve.Violations[0].Path)
}

// ドメインが位置を運んできたら（Rule.ViolatedAt）、呼び出し側が渡した前置より明細の
// パスが優先される。注文コンテキストには現在その規則が無いが、機構は在庫側と同一なので、
// 規則を足した瞬間に動くことをここで固定しておく。
func TestLocate_DomainIndexOverridesPrefix(t *testing.T) {
	t.Parallel()

	err := order.VQuantity.ViolatedAt(2, "明細の数量が不正です")

	var ve *ValidationError
	require.ErrorAs(t, locate("", err), &ve)
	assert.Equal(t, "Lines[2].Quantity", ve.Violations[0].Path)
}

func TestLocate_NilAndEmptyPrefix(t *testing.T) {
	t.Parallel()

	assert.Nil(t, locate("", nil), "nil は nil のまま")

	_, err := order.NewCustomerID("")
	var ve *ValidationError
	require.ErrorAs(t, locate("", err), &ve)
	assert.Equal(t, "CustomerId", ve.Violations[0].Path, "前置が空ならフィールド名だけになる")
}

// ValidationError.Error() が包んだ文言をそのまま返すこと。
// ログ出力（NewError の WarnContext / ErrorContext）がこの文言に依存している。
func TestValidationError_ErrorPassesThroughWrappedMessage(t *testing.T) {
	t.Parallel()

	_, err := order.NewQuantity(0)
	located := locate("Lines[0]", err)

	assert.Equal(t, err.Error(), located.Error(), "元の文言を変えない")
	assert.ErrorIs(t, located, order.ErrInvalidQuantity, "番兵まで Unwrap が繋がる")
}
