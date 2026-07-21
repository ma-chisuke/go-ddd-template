// Package id は、衝突しにくい不透明な識別子を生成する小さなユーティリティを提供する。
// 集約の ID や相関 ID の採番など、ドメインに依存しない用途で使う。
package id

import (
	"crypto/rand"
	"encoding/hex"
)

// New は 128 ビットの乱数を 16 進文字列（32 文字）にして返す。
//
// 乱数源には暗号学的に安全な crypto/rand を用いる。crypto/rand の読み取りが
// 失敗するのは OS の乱数生成器が壊れている場合など回復不能な状況に限られるため、
// ここでは戻り値にエラーを持たせず、その場合のみ panic する。これは「予期される
// 失敗」ではなく致命的な異常であり、通常の制御フローで扱うべきものではない。
func New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 到達しない想定。到達した場合は乱数源が壊れており、継続不能。
		panic("id: crypto/rand の読み取りに失敗しました: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
