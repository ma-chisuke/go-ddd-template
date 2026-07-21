package interfaces

import (
	"net/http"

	"github.com/example/go-ddd-template/shared/correlation"
	"github.com/example/go-ddd-template/shared/id"
)

// correlationHeader はリクエスト／レスポンスで相関 ID を運ぶ HTTP ヘッダ名。
const correlationHeader = "X-Correlation-ID"

// CorrelationMiddleware は相関 ID を確立する HTTP ミドルウェア。
// リクエストヘッダに相関 ID があればそれを引き継ぎ、無ければ新規採番して
// context に載せ、レスポンスヘッダにも反映する。
//
// 重要: context に載せてよいのはこうしたリクエストスコープの付帯情報だけであり、
// トランザクションハンドルのような制御関心を context に隠して運んではならない。
func CorrelationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cid := r.Header.Get(correlationHeader)
		if cid == "" {
			cid = id.New()
		}
		ctx := correlation.WithID(r.Context(), cid)
		w.Header().Set(correlationHeader, cid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
