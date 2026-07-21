// Package correlation は、リクエストを横断して追跡するための相関 ID を
// context.Context に載せ替えるためのヘルパーを提供する。
//
// 重要な設計方針として、context.Context に載せてよいのは相関 ID のような
// 「リクエストスコープの付帯情報」だけである。トランザクションハンドルのような
// 「制御に関わるもの」を context に隠して受け渡してはならない。トランザクション
// 境界は uow パッケージが明示的に所有する。
package correlation

import "context"

// ctxKey は相関 ID を context に格納するための非公開キー型。
// 非公開の空構造体をキーにすることで、他パッケージのキーと衝突しない。
type ctxKey struct{}

// WithID は相関 ID を持つ新しい context を返す。
func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext は context から相関 ID を取り出す。存在すれば ok は true。
func FromContext(ctx context.Context) (id string, ok bool) {
	v, ok := ctx.Value(ctxKey{}).(string)
	return v, ok
}

// FromContextOrEmpty は相関 ID を取り出し、無ければ空文字列を返す。
func FromContextOrEmpty(ctx context.Context) string {
	id, _ := FromContext(ctx)
	return id
}
