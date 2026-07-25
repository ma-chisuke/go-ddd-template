// Package problem は RFC 9457 (Problem Details for HTTP APIs) のエラー応答を組み立てる
// ための、ドメインにもフレームワークにも依存しない建材を収める。
//
// 置いてよいもの:
//   - 種別（type）URI のサフィックスと、それに対応する title（[types.go]）
//   - 契約検証（400 系）の code 語彙と reason 表（[vocab.go]）
//   - フィールドパスの表記規則（[JoinPath]）
//
// 置いてはならないもの:
//   - ドメイン検証（422 系）の code 語彙。これはコンテキストが所有する（制約 C-6 / 規則 R-7）。
//     同じ invalid_quantity でも、注文コンテキストでは「1 以上」、在庫コンテキストでは
//     「0 以上」を意味する。共有すると値域の違いが消える。
//   - ProblemDetails の組み立て。ogen が生成する ProblemDetails は契約ごとに別の Go 型であり、
//     shared モジュールはそれらを跨げない。組み立ては各コンテキストのインターフェース層に残す。
//
// ogen 固有の抽出処理は、依存を持ち込まないようサブパッケージ
// [github.com/example/go-ddd-template/shared/problem/ogenproblem] に分けている
// （shared/uow → shared/uow/pgxuow と同じ分け方）。
package problem

import "strings"

// Param は 1 件のフィールド違反（フィールドのパスと、機械可読な違反理由）。
//
// ogen 生成型 InvalidParam へ写す前の、生成型に依存しない中間表現である。reason を
// 持たないのは、reason の引き方が語彙によって違う（契約検証は [ReasonOf]、ドメイン検証は
// コンテキストごとの表）ためで、その解決は各コンテキストのインターフェース層が行う。
type Param struct {
	// Name は違反したフィールドのパス（ドット + 角括弧記法。規則 R-8）。
	Name string
	// Code は機械可読な違反理由。契約検証なら [vocab.go] の語彙。
	Code string
}

// JoinPath はパスの断片をフィールドパス表記へ連結する（規則 R-8）。
//
// 添字の断片（"[0]" のように角括弧で始まるもの）はドットを挟まず直前の断片へ連結し、
// それ以外はドットで繋ぐ。空の断片は無視する。
//
//	JoinPath([]string{"lines", "[0]", "unitPrice", "amount"}) == "lines[0].unitPrice.amount"
//
// この表記を選んだ理由（FD-Q1=A）: JSON Pointer（/lines/0/quantity）よりも、多くの
// API クライアントと利用者にとって馴染みのある形だからである。RFC 9457 は入れ子構造の
// 表記を規定していないため、API 側が決めて文書化する必要がある。
func JoinPath(segs []string) string {
	var b strings.Builder
	for _, s := range segs {
		if s == "" {
			continue
		}
		if b.Len() > 0 && !strings.HasPrefix(s, "[") {
			b.WriteByte('.')
		}
		b.WriteString(s)
	}
	return b.String()
}
