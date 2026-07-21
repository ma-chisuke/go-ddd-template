package id_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/example/go-ddd-template/shared/id"
)

var hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestNew(t *testing.T) {
	a := id.New()
	assert.Regexp(t, hex32, a, "New() は 32 桁の 16 進文字列を返すべき")
	// 連続生成でほぼ確実に異なる値になる。
	b := id.New()
	assert.NotEqual(t, a, b, "New() が同じ値を返した")
}
