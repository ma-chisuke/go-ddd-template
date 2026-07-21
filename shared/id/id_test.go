package id_test

import (
	"regexp"
	"testing"

	"github.com/example/go-ddd-template/shared/id"
)

var hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestNew(t *testing.T) {
	a := id.New()
	if !hex32.MatchString(a) {
		t.Fatalf("New() = %q, want 32 桁の 16 進文字列", a)
	}
	// 連続生成でほぼ確実に異なる値になる。
	if b := id.New(); a == b {
		t.Fatalf("New() が同じ値を返した: %q", a)
	}
}
