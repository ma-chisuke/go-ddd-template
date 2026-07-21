package testutil

import (
	"errors"
	"testing"
)

// Equal は got と want が等しいことを表明する。等しくなければテストを失敗させる。
// 比較可能(comparable)な任意の型に使える汎用ヘルパー。
func Equal[T comparable](tb testing.TB, got, want T, msg string) {
	tb.Helper()
	if got != want {
		tb.Fatalf("%s: got=%v, want=%v", msg, got, want)
	}
}

// NoError はエラーが無いことを表明する。エラーがあればテストを失敗させる。
func NoError(tb testing.TB, err error, msg string) {
	tb.Helper()
	if err != nil {
		tb.Fatalf("%s: 想定外のエラー: %v", msg, err)
	}
}

// ErrorIs は err が target をラップしていること(errors.Is)を表明する。
func ErrorIs(tb testing.TB, err, target error, msg string) {
	tb.Helper()
	if !errors.Is(err, target) {
		tb.Fatalf("%s: エラー = %v, want (errors.Is) %v", msg, err, target)
	}
}

// True は条件が真であることを表明する。
func True(tb testing.TB, cond bool, msg string) {
	tb.Helper()
	if !cond {
		tb.Fatalf("%s: 条件が偽でした(真であるべき)", msg)
	}
}
